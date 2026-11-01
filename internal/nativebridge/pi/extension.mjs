import { spawn } from "node:child_process";

const ENTRY = "agentmux.native.delivery";
const MESSAGE = "agentmux.instruction";
const TERMINAL = new Set(["completed", "failed", "cancelled", "uncertain"]);
const MAX_RESULT = 1024 * 1024;

// Exec with argv and JSON stdin: peer content never reaches a shell, flags,
// slash-command expansion, or the terminal editor.
export function cliTransport(binary, options = {}) {
  const pending = new Set();
  const call = (operation, args) => new Promise((resolve, reject) => {
    const child = spawn(binary, ["managed", "call", operation, "--stdin"], { stdio: ["pipe", "pipe", "pipe"] });
    pending.add(child);
    const output = [];
    let outputBytes = 0, done = false;
    const finish = (err, result) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      pending.delete(child);
      if (err) reject(err); else resolve(result);
    };
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      finish(new Error("Agentmux broker timed out"));
    }, options.timeout ?? 10000);
    child.on("error", () => finish(new Error("Agentmux broker could not be reached")));
    child.stdin.on("error", () => {});
    child.stdout.on("data", data => {
      if (done) return;
      outputBytes += data.length;
      if (outputBytes > 8 * 1024 * 1024) {
        child.kill("SIGKILL");
        finish(new Error("Agentmux broker response exceeded its limit"));
        return;
      }
      output.push(data);
    });
    // Drain diagnostics without retaining or displaying potential credential data.
    child.stderr.on("data", () => {});
    child.on("close", code => {
      if (done) return;
      try {
        const envelope = JSON.parse(Buffer.concat(output).toString("utf8"));
        if (envelope.operation !== operation) throw new Error();
        if (envelope.ok === false && typeof envelope.error?.code === "string" && /^[a-z_]{1,64}$/.test(envelope.error.code)) {
          const fault = new Error("Agentmux broker rejected the operation");
          fault.code = envelope.error.code; fault.definite = true;
          finish(fault); return;
        }
        if (code !== 0 || envelope.ok !== true) throw new Error();
        finish(undefined, envelope.result);
      } catch { finish(new Error("Agentmux broker rejected the operation")); }
    });
    child.stdin.end(JSON.stringify(args));
  });
  call.close = () => { for (const child of pending) child.kill("SIGKILL"); };
  return call;
}

function textOf(message) {
  if (typeof message?.content === "string") return message.content.slice(0, MAX_RESULT + 1);
  return (message?.content ?? []).filter(part => part.type === "text" && typeof part.text === "string")
    .map(part => part.text).join("\n").slice(0, MAX_RESULT + 1);
}

function boundedResult(text, limit) {
  if (Buffer.byteLength(text) <= limit) return { text, truncated: false };
  const notice = "\n[Agentmux result truncated; full answer remains in the native conversation.]";
  const suffix = Buffer.byteLength(notice) < limit ? notice : "";
  const budget = limit - Buffer.byteLength(suffix);
  let low = 0, high = text.length;
  while (low < high) {
    const middle = Math.ceil((low + high) / 2);
    if (Buffer.byteLength(text.slice(0, middle)) <= budget) low = middle; else high = middle - 1;
  }
  const prefix = text.slice(0, low).replace(/[\uD800-\uDBFF]$/, "");
  return { text: prefix + suffix, truncated: true };
}

function prompt(delivery) {
  let kind = "Agent-authored instruction", origin = "This message is from another agent, not the human owner.";
  if (delivery.from === "$owner") {
    kind = "Owner-authored instruction";
    origin = "The workspace owner sent this through Agentmux; it remains separate from direct terminal input.";
  }
  if (delivery.kind !== "instruction") {
    kind = "Observation, not an instruction grant";
    if (delivery.kind === "boundaries") origin = "The coordination service supplied this current owner role and boundary notice.";
  }
  return `[Agentmux ${kind} from ${JSON.stringify(delivery.from)}; delivery ${delivery.id}; task ${delivery.task_id ?? ""}]\n` +
    origin + " Current broker permissions remain authoritative.\n\n" + delivery.text;
}

// Exported separately for model-free contract tests. Production uses the exact
// same event handlers and transport, not a test-only alternate implementation.
export function createBridge(pi, call, options = {}) {
  let ctx, session, registered = false, closed = true, timer, ticking = false;
  let unsupportedReason = "";
  let uiPrompt = false, active, fenced, pendingEvents = [], lastStatus = "";
  let resultLimit = 48 * 1024;
  const seen = new Map();
  const interval = options.interval ?? 750;
  const schedule = () => {
    if (!closed && options.autoSchedule !== false) timer = setTimeout(() => { void tick().finally(schedule); }, interval);
  };
  const show = text => {
    if (ctx?.mode === "tui" && typeof ctx.ui?.setStatus === "function" && lastStatus !== text) {
      lastStatus = text;
      ctx.ui.setStatus("agentmux", text);
    }
  };
  const save = event => {
    seen.set(event.id, event);
    pi.appendEntry(ENTRY, event);
  };
  const queueEvent = event => {
    if (fenced?.id === event.id) {
      save({ ...event, phase: "uncertain", observed_phase: event.phase, fenced: true,
        reason: "Broker fenced this attempt; native evidence retained for owner resolution" });
      return;
    }
    save(event);
    pendingEvents.push({ session, ...event });
  };
  const acknowledge = event => {
    // A completed result may already be durable while an earlier running
    // acknowledgement is in flight. Never overwrite it with an older phase.
    if (seen.get(event.id)?.phase === event.phase) save({ ...event, reported: true });
  };
  const reportEvent = async event => {
    const { id, phase, native_ref, result, reason } = event;
    try { await call("native.event", { session, id, phase, native_ref, result, reason }); }
    catch (error) {
      if (error.definite && ["conflict", "forbidden", "not_found"].includes(error.code)) fence(id);
      throw error;
    }
    acknowledge(event);
    const index = pendingEvents.indexOf(event);
    if (index >= 0) pendingEvents.splice(index, 1);
  };
  const fence = id => {
    const previous = seen.get(id) ?? { id, phase: "uncertain" };
    fenced = { id };
    save({ ...previous, phase: "uncertain", observed_phase: previous.phase, fenced: true, reported: false,
      reason: "Broker fenced this attempt; resolve the uncertain task before another native delivery" });
    pendingEvents = pendingEvents.filter(event => event.id !== id);
  };
  const readiness = () => {
    if (unsupportedReason) return { state: "offline", reason: unsupportedReason };
    if (ctx?.mode !== "tui") return { state: "offline", reason: "Pi bridge requires its native terminal UI" };
    if (!ctx.isProjectTrusted()) return { state: "approval", reason: "Waiting for native project trust" };
    if (uiPrompt) return { state: "approval", reason: "Waiting for native approval or dialog" };
    if (!ctx.isIdle()) return { state: "busy", reason: "Native conversation is running" };
    if (ctx.hasPendingMessages()) return { state: "busy", reason: "Native messages are already queued" };
    if (ctx.ui.getEditorText().length > 0) return { state: "draft", reason: "Preserving your terminal draft" };
    return { state: "idle" };
  };
  const flushEvents = async () => {
    while (pendingEvents.length && !closed) {
      await reportEvent(pendingEvents[0]);
    }
  };
  const begin = async () => {
    const item = active;
    if (!item) return;
    const ready = readiness();
    if (ready.state !== "idle") { show(`Agentmux: ${ready.reason}`); return; }
    // Claim/report round trips can race with human typing. Recheck immediately
    // before injection, after every awaited operation, and never touch the draft.
    if (!item.submitting) {
      try { await call("native.event", { session, id: item.delivery.id, phase: "submitting" }); }
      catch (error) {
        if (error.definite && ["conflict", "forbidden", "not_found"].includes(error.code)) fence(item.delivery.id);
        throw error;
      }
      if (closed || active !== item) return;
      item.submitting = true;
      save({ id: item.delivery.id, phase: "submitting", reported: true });
    }
    if (readiness().state !== "idle") return;
    item.injected = true;
    item.injectedAt = Date.now();
    show(`Agentmux: instruction from ${item.delivery.from}`);
    pi.sendMessage({ customType: MESSAGE, content: prompt(item.delivery), display: true,
      details: { delivery_id: item.delivery.id, task_id: item.delivery.task_id, from: item.delivery.from } },
    { triggerTurn: true, deliverAs: "followUp" });
    // A draft may have delayed injection after the pre-submit broker barrier.
    // Only a successful native call proves submission. Retain this receipt for
    // retry without repeating sendMessage, and preserve any synchronous start
    // or settlement evidence emitted by the native call itself.
    if (seen.get(item.delivery.id)?.phase === "submitting") {
      queueEvent({ id: item.delivery.id, phase: "submitted" });
    }
    await flushEvents();
  };
  async function tick() {
    if (closed || ticking || !ctx) return;
    ticking = true;
    try {
      if (!registered) {
        const supported = !unsupportedReason;
        const registration = await call("native.register", { adapter: "pi", session,
          capabilities: { instructions: supported, observations: supported, completion: supported, completion_evidence: supported ? "native-events" : "", cancellation: supported, preserves_draft: supported, native_queue: supported } });
        if (closed) return;
        if (Number.isInteger(registration?.max_result_bytes) && registration.max_result_bytes > 0) resultLimit = Math.min(MAX_RESULT, registration.max_result_bytes);
        registered = true;
      }
      if (fenced) {
        const state = (await call("native.status", { session }))?.status;
        if (closed) return;
        if (!state || state.active_id === fenced.id || state.state === "uncertain") {
          show("Agentmux: uncertain delivery needs owner resolution; it will not be replayed");
          return;
        }
        if (active?.delivery.id === fenced.id) {
          if (active.injected && (!ctx.isIdle() || ctx.hasPendingMessages())) {
            if (active.started && !active.humanIntervened && !active.cancelling) { active.cancelling = true; ctx.abort(); }
            show("Agentmux: old attempt resolved; waiting for a safe native boundary");
            return;
          }
          active = undefined;
        }
        const id = fenced.id;
        save({ ...seen.get(id), reported: true, owner_resolved: true });
        pendingEvents = pendingEvents.filter(event => event.id !== id);
        fenced = undefined;
      }
      await flushEvents();
      if (closed) return;
      const state = (await call("native.status", { session }))?.status;
      if (closed) return;
      if (active) await call("native.peek", { session, ...readiness() });
      if (closed) return;
      if (state?.cancel && active) {
        active.cancelling = true;
        if (!active.injected) {
          queueEvent({ id: active.delivery.id, phase: "cancelled", reason: "Cancelled before native delivery" });
          active = undefined;
        } else if (active.started && !active.humanIntervened) {
          ctx.abort();
        } else {
          show("Agentmux: cancellation awaits a safe native boundary");
        }
        return;
      }
      if (active) {
        if (!active.injected) await begin();
        else if (!active.started && Date.now() - active.injectedAt > (options.startTimeout ?? 15000)) {
          queueEvent({ id: active.delivery.id, phase: "uncertain", reason: "Pi did not confirm the submitted turn; it will not be replayed" });
          active = undefined;
        }
        return;
      }
      const ready = readiness();
      const result = await call("native.peek", { session, ...ready });
      if (closed) return;
      if (ready.state !== "idle") { show(`Agentmux: ${ready.reason}`); return; }
      if (!result?.candidate) { show(state?.reason ? `Agentmux: ${state.reason}` : "Agentmux: ready"); return; }
      if (readiness().state !== "idle") return;
      const claimed = await call("native.claim", { session, candidate: result.candidate });
      if (closed || !claimed?.delivery) return;
      const delivery = claimed.delivery;
      if (typeof delivery.id !== "string" || !delivery.id || typeof delivery.text !== "string" || typeof delivery.from !== "string") throw new Error("Invalid Agentmux delivery");
      if (seen.has(delivery.id)) {
        queueEvent({ id: delivery.id, phase: "uncertain", reason: "Previously submitted delivery will not be replayed" });
        return;
      }
      save({ id: delivery.id, phase: "claimed" });
      active = { delivery, submitting: false, injected: false, started: false, humanIntervened: false, result: "", outcome: "" };
      await begin();
    } catch {
      if (!closed) show(fenced ? "Agentmux: delivery fenced; resolve the uncertain task" : "Agentmux: broker unavailable; delivery paused");
    } finally { ticking = false; }
  }
  pi.on("session_start", (_event, current) => {
    ctx = current; closed = false;
    const required = [ctx?.isProjectTrusted, ctx?.isIdle, ctx?.hasPendingMessages, ctx?.abort,
      ctx?.ui?.getEditorText, ctx?.ui?.setStatus, ctx?.sessionManager?.getSessionId, ctx?.sessionManager?.getBranch,
      pi.sendMessage, pi.appendEntry];
    if (ctx?.mode !== "tui" || required.some(value => typeof value !== "function")) {
      unsupportedReason = "Pi native delivery APIs unavailable; manual coordination remains available";
      session = typeof ctx?.sessionManager?.getSessionId === "function" ? ctx.sessionManager.getSessionId() : `pi-unsupported-${process.pid}`;
      schedule(); return;
    }
    session = ctx.sessionManager.getSessionId();
    for (const entry of ctx.sessionManager.getBranch()) if (entry.type === "custom" && entry.customType === ENTRY && entry.data?.id) seen.set(entry.data.id, entry.data);
    for (const [id, record] of seen) {
      if (record.fenced && !record.owner_resolved) fenced = { id };
      else if (!TERMINAL.has(record.phase)) queueEvent({ id, phase: "uncertain", reason: "Native extension restarted during a delivery; it will not be replayed" });
      else if (!record.reported) pendingEvents.push({ session, ...record });
    }
    schedule();
  });
  pi.on("ui_prompt_start", () => { uiPrompt = true; });
  pi.on("ui_prompt_end", () => { uiPrompt = false; });
  pi.on("input", event => { if (event.source === "interactive" && active?.injected) active.humanIntervened = true; });
  pi.on("before_agent_start", event => {
    if (active && event.messageType === "custom" && event.message?.customType === MESSAGE && event.message.details?.delivery_id === active.delivery.id) {
      active.started = true;
      queueEvent({ id: active.delivery.id, phase: "running", native_ref: session });
    }
  });
  pi.on("message_end", event => {
    if (!active?.started || event.message?.role !== "assistant") return;
    active.result = textOf(event.message);
    active.stopReason = event.message.stopReason;
  });
  pi.on("agent_before_settle", event => { if (active?.started) active.outcome = event.outcome; });
  pi.on("agent_settled", event => {
    if (!active?.started) return;
    const item = active;
    let phase = "completed", reason = "";
    if (event.aborted || item.cancelling || item.outcome === "aborted" || item.stopReason === "aborted") { phase = "cancelled"; reason = "Native turn was cancelled"; }
    else if (item.outcome !== "completed" || item.stopReason !== "stop") { phase = "failed"; reason = "Native turn did not produce a successful final answer"; }
    // Pi settles after queued human steering/follow-ups too. Those answers
    // cannot be attributed solely to the peer request or automatically shared.
    const withheld = phase === "completed" && item.humanIntervened;
    const result = boundedResult(withheld
      ? "[Agentmux: Result withheld because human input intervened. See the native conversation.]"
      : item.result, resultLimit);
    if (withheld) reason = "Human input intervened; the native run settled, but its answer was withheld from automatic peer sharing";
    else if (phase === "completed" && result.truncated) reason = "Result truncated to the broker limit; full answer remains in the native conversation";
    queueEvent({ id: item.delivery.id, phase, native_ref: session, result: phase === "completed" ? result.text : "", reason });
    active = undefined;
    show(`Agentmux: ${phase}`);
  });
  pi.on("session_shutdown", async () => {
    closed = true; clearTimeout(timer);
    ctx?.ui?.setStatus?.("agentmux", undefined);
    if (active) {
      // The durable marker prevents an uncertain turn from being replayed when
      // Pi resumes. No new model turn is initiated during shutdown.
      queueEvent({ id: active.delivery.id, phase: "uncertain", reason: "Native session closed during delivery; it will not be replayed" });
      active = undefined;
    }
    // Bound quit latency, but flush completed results and lost native turns
    // where possible. Unacknowledged records remain durable for resume.
    let quitTimer;
    await Promise.race([(async () => {
      while (pendingEvents.length) {
        await reportEvent(pendingEvents[0]);
      }
    })().catch(() => {}), new Promise(resolve => { quitTimer = setTimeout(resolve, 250); })]);
    clearTimeout(quitTimer);
    call.close?.();
  });
  return { tick, readiness, getActive: () => active, getPendingEvents: () => pendingEvents.slice() };
}

export default function agentmuxExtension(pi) {
  const binary = process.env.AGENTMUX_CLI;
  if (!binary || !process.env.AGENTMUX_CREDENTIAL_FILE || !process.env.AGENTMUX_SOCKET) return;
  createBridge(pi, cliTransport(binary));
}

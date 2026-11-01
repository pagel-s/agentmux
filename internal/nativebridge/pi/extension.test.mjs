import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createBridge, cliTransport } from "./extension.mjs";

function fixture(options = {}) {
  const handlers = new Map(), calls = [], entries = [...(options.entries ?? [])], sent = [], statuses = [];
  let draft = "", idle = true, trusted = true, pending = false, cancel = false, aborts = 0, candidate = "request-1", status = {};
  const delivery = { id: "delivery-1", task_id: "task-1", from: "codex", to: "pi", kind: "instruction", text: "Write a stanza; $(do not execute)\n/quit", ...options.delivery };
  const ctx = {
    mode: "tui", isProjectTrusted: () => trusted, isIdle: () => idle, hasPendingMessages: () => pending,
    abort: () => { aborts++; },
    ui: { getEditorText: () => draft, setStatus: (key, text) => statuses.push(text) },
    sessionManager: { getSessionId: () => "pi-session", getBranch: () => entries },
  };
  if (options.missingAPI) delete ctx[options.missingAPI];
  const pi = {
    on: (name, handler) => handlers.set(name, handler),
    appendEntry: (customType, data) => entries.push({ type: "custom", customType, data }),
    sendMessage: (message, opts) => sent.push({ message, opts }),
  };
  const call = async (operation, args) => {
    calls.push({ operation, args });
    if (options.onCall) await options.onCall(operation, args, state);
    if (operation === "native.status") return { status: { cancel, mode: "automatic", ...status } };
    if (operation === "native.register") return { max_result_bytes: options.resultLimit ?? 65536 };
    if (operation === "native.peek") return { candidate };
    if (operation === "native.claim") { candidate = ""; return { delivery }; }
    return {};
  };
  const bridge = createBridge(pi, call, { autoSchedule: false, ...options });
  const emit = async (name, event = {}) => handlers.get(name)?.(event, ctx);
  const state = { bridge, call, calls, entries, sent, statuses, ctx, delivery, emit,
    setDraft: v => { draft = v; }, setIdle: v => { idle = v; }, setTrusted: v => { trusted = v; },
    setPending: v => { pending = v; }, setCancel: v => { cancel = v; }, getAborts: () => aborts,
    setStatus: v => { status = v; }, setCandidate: v => { candidate = v; },
    events: () => calls.filter(c => c.operation === "native.event").map(c => c.args),
    start: async () => {
      await emit("before_agent_start", { messageType: "custom", message: sent.at(-1).message });
    },
    finish: async (outcome = "completed", stopReason = "stop", aborted = false) => {
      await emit("message_end", { message: { role: "assistant", content: [{ type: "thinking", thinking: "private" }, { type: "text", text: "A finished stanza" }], stopReason } });
      await emit("agent_end", {});
      await emit("agent_before_settle", { outcome });
      await emit("agent_settled", { aborted });
    },
  };
  emit("session_start");
  return state;
}

test("delivers an attributed native turn and completes only after real settlement", async () => {
  const f = fixture();
  await f.bridge.tick();
  assert.equal(f.sent.length, 1);
  const { message, opts } = f.sent[0];
  assert.match(message.content, /not the human owner/);
  assert.ok(message.content.includes(f.delivery.text));
  assert.equal(message.details.delivery_id, "delivery-1");
  assert.deepEqual(opts, { triggerTurn: true, deliverAs: "followUp" });
  assert.deepEqual(f.events().map(e => e.phase), ["submitted"]);
  await f.start();
  await f.emit("message_end", { message: { role: "assistant", content: [{ type: "text", text: "intermediate" }], stopReason: "stop" } });
  await f.emit("agent_end");
  await f.bridge.tick();
  assert.deepEqual(f.events().map(e => e.phase), ["submitted", "running"]);
  await f.finish();
  await f.bridge.tick();
  assert.deepEqual(f.events().map(e => e.phase), ["submitted", "running", "completed"]);
  assert.equal(f.events().at(-1).result, "A finished stanza");
  assert.ok(!JSON.stringify(f.events()).includes("private"));
});

test("missing native readiness APIs retain manual coordination without false completion support", async () => {
  const f = fixture({ missingAPI: "isProjectTrusted" });
  await f.bridge.tick();
  const caps = f.calls.find(c => c.operation === "native.register").args.capabilities;
  assert.equal(caps.instructions, false);
  assert.equal(caps.completion, false);
  assert.equal(caps.completion_evidence, "");
  assert.equal(f.sent.length, 0);
  assert.ok(!f.calls.some(c => c.operation === "native.claim"));
  const peek = f.calls.find(c => c.operation === "native.peek");
  assert.equal(peek.args.state, "offline");
  assert.match(peek.args.reason, /manual coordination/);
  await f.emit("session_shutdown");
});

for (const [reason, block, clear] of [
  ["draft", f => f.setDraft("unsent human text"), f => f.setDraft("")],
  ["busy", f => f.setIdle(false), f => f.setIdle(true)],
  ["pending native input", f => f.setPending(true), f => f.setPending(false)],
  ["trust", f => f.setTrusted(false), f => f.setTrusted(true)],
  ["approval", f => f.emit("ui_prompt_start"), f => f.emit("ui_prompt_end")],
]) test(`preserves ${reason} until the native boundary is available`, async () => {
  const f = fixture();
  await block(f);
  await f.bridge.tick();
  assert.equal(f.sent.length, 0);
  assert.ok(!f.calls.some(c => c.operation === "native.claim"));
  await clear(f);
  await f.bridge.tick();
  assert.equal(f.sent.length, 1);
});

test("draft typed during claim or submitted acknowledgement remains intact and delays injection", async () => {
  for (const race of ["native.claim", "native.event"]) {
    let raced = false;
    const f = fixture({ onCall: (op, args, state) => {
      if (!raced && op === race) { state.setDraft("human typed during RPC"); raced = true; }
    } });
    await f.bridge.tick();
    assert.equal(f.sent.length, 0);
    assert.equal(f.ctx.ui.getEditorText(), "human typed during RPC");
    f.setDraft("");
    await f.bridge.tick();
    assert.equal(f.sent.length, 1);
  }
});

test("cancels only its own started turn and waits for cancellation settlement", async () => {
  const f = fixture(); await f.bridge.tick(); await f.start();
  f.setIdle(false); f.setCancel(true); await f.bridge.tick();
  assert.equal(f.getAborts(), 1);
  assert.ok(!f.events().some(e => e.phase === "cancelled"));
  await f.finish("aborted", "aborted", true); await f.bridge.tick();
  assert.equal(f.events().at(-1).phase, "cancelled");
});

test("does not abort a human turn intervening in the peer request", async () => {
  const f = fixture(); await f.bridge.tick(); await f.start();
  await f.emit("input", { source: "interactive", text: "human intervention" });
  f.setCancel(true); await f.bridge.tick();
  assert.equal(f.getAborts(), 0);
  assert.match(f.statuses.at(-1), /safe native boundary/);
  await f.finish(); await f.bridge.tick();
  assert.equal(f.events().at(-1).phase, "cancelled");
});

test("withholds later human answers from the peer result after actual interactive input", async () => {
  const f = fixture(); await f.bridge.tick(); await f.start();
  await f.emit("input", { source: "interactive", text: "PRIVATE_OWNER_FOLLOWUP" });
  await f.emit("message_end", { message: { role: "assistant", content: [{ type: "text", text: "PRIVATE_OWNER_ANSWER" }], stopReason: "stop" } });
  await f.emit("agent_before_settle", { outcome: "completed" });
  await f.emit("agent_settled", { aborted: false });
  await f.bridge.tick();
  const event = f.events().at(-1);
  assert.equal(event.phase, "completed");
  assert.match(event.result, /withheld because human input intervened/);
  assert.match(event.reason, /withheld from automatic peer sharing/);
  assert.ok(!JSON.stringify(f.events()).includes("PRIVATE_OWNER"));
  assert.ok(!JSON.stringify(f.entries).includes("PRIVATE_OWNER"));
});

test("unsent drafts and extension input do not suppress the correlated peer answer", async () => {
  const f = fixture(); await f.bridge.tick(); await f.start();
  f.setDraft("Unsent private draft");
  await f.emit("input", { source: "extension", text: "custom extension content" });
  await f.finish(); await f.bridge.tick();
  assert.equal(f.events().at(-1).result, "A finished stanza");
  assert.equal(f.ctx.ui.getEditorText(), "Unsent private draft");
});

for (const [outcome, stop] of [["error", "error"], ["completed", "length"], ["completed", "toolUse"]]) {
  test(`does not call ${outcome}/${stop} a completed reply`, async () => {
    const f = fixture(); await f.bridge.tick(); await f.start();
    await f.finish(outcome, stop); await f.bridge.tick();
    assert.equal(f.events().at(-1).phase, "failed");
  });
}

test("resume never replays a submitted uncertain delivery", async () => {
  const f = fixture({ entries: [{ type: "custom", customType: "agentmux.native.delivery", data: { id: "delivery-1", phase: "submitted", reported: true } }] });
  await f.bridge.tick();
  assert.equal(f.sent.length, 0);
  assert.ok(f.events().some(e => e.phase === "uncertain"));
});

test("retries a durable completed report after reload without another native turn", async () => {
  const f = fixture({ entries: [{ type: "custom", customType: "agentmux.native.delivery", data: { id: "previous", phase: "completed", result: "durable reply" } }] });
  await f.bridge.tick();
  const report = f.events().find(e => e.id === "previous");
  assert.equal(report.result, "durable reply");
  assert.equal(report.phase, "completed");
  assert.ok(!Object.hasOwn(report, "reported"));
});

test("lost broker acknowledgement never causes a second native submission", async () => {
  let failures = 1;
  const f = fixture({ onCall: (op, args) => {
    if (op === "native.event" && args.phase === "completed" && failures-- > 0) throw new Error("temporary broker failure");
  } });
  await f.bridge.tick(); await f.start(); await f.finish();
  await f.bridge.tick(); await f.bridge.tick();
  assert.equal(f.sent.length, 1);
  assert.equal(f.events().filter(e => e.phase === "completed").length, 2);
});

test("shutdown reports uncertainty without invoking a new turn", async () => {
  const f = fixture(); await f.bridge.tick();
  await f.emit("session_shutdown"); await f.bridge.tick();
  assert.equal(f.sent.length, 1);
  assert.equal(f.events().at(-1).phase, "uncertain");
});

test("keeps readiness heartbeat active and bounds final UTF-8 replies to broker limit", async () => {
  const f = fixture({ resultLimit: 32 }); await f.bridge.tick(); await f.start(); f.setIdle(false);
  await f.bridge.tick();
  assert.ok(f.calls.some(c => c.operation === "native.peek" && c.args.state === "busy"));
  await f.emit("message_end", { message: { role: "assistant", content: [{ type: "text", text: "🌌".repeat(40) }], stopReason: "stop" } });
  await f.emit("agent_before_settle", { outcome: "completed" });
  await f.emit("agent_settled", { aborted: false });
  await f.bridge.tick();
  const event = f.events().at(-1);
  assert.equal(event.phase, "completed");
  assert.ok(Buffer.byteLength(event.result) <= 32);
  assert.match(event.reason, /truncated/);
});

test("marks truncation even when the broker accepts its maximum-sized result", async () => {
  const limit = 1024 * 1024;
  const f = fixture({ resultLimit: limit }); await f.bridge.tick(); await f.start();
  await f.emit("message_end", { message: { role: "assistant", content: [{ type: "text", text: "x".repeat(limit + 100) }], stopReason: "stop" } });
  await f.emit("agent_before_settle", { outcome: "completed" });
  await f.emit("agent_settled", { aborted: false });
  await f.bridge.tick();
  const event = f.events().at(-1);
  assert.ok(Buffer.byteLength(event.result) <= limit);
  assert.match(event.reason, /truncated/);
  assert.match(event.result, /full answer remains/);
});

test("CLI transport rejects malformed responses without exposing diagnostics", async () => {
  const call = cliTransport(process.execPath, { timeout: 500 });
  await assert.rejects(call("native.status", { text: "$(never a shell command)" }), /broker rejected/);
  call.close();
});

test("owner instructions and broker boundary notices have truthful source attribution", async () => {
  for (const [kind, expected] of [["instruction", "Owner-authored instruction"], ["boundaries", "coordination service supplied"]]) {
    const f = fixture({ delivery: { from: "$owner", kind } }); await f.bridge.tick();
    assert.match(f.sent[0].message.content, new RegExp(expected));
    assert.ok(!f.sent[0].message.content.includes("from another agent"));
  }
});

test("definite fenced report pauses until owner resolution then accepts only new work", async () => {
  let rejectRunning = true;
  const f = fixture({ onCall: (op, args) => {
    if (op === "native.event" && args.phase === "running" && rejectRunning) throw Object.assign(new Error("fenced after broker recovery"), { code: "conflict", definite: true });
  } });
  await f.bridge.tick(); await f.start();
  f.setStatus({ active_id: "delivery-1", state: "uncertain" });
  await f.bridge.tick();
  assert.match(f.statuses.at(-1), /fenced/);
  await f.finish();
  await f.bridge.tick(); await f.bridge.tick();
  assert.equal(f.sent.length, 1);
  assert.equal(f.events().filter(e => e.phase === "running").length, 1);
  assert.equal(f.entries.at(-1).data.observed_phase, "completed");
  rejectRunning = false;
  Object.assign(f.delivery, { id: "delivery-2", task_id: "task-2" }); f.setCandidate("new-owner-approved-work");
  f.setStatus({ active_id: "", state: "ready" });
  await f.bridge.tick();
  assert.equal(f.sent.length, 2);
  assert.equal(f.sent[0].message.details.delivery_id, "delivery-1");
  assert.equal(f.sent[1].message.details.delivery_id, "delivery-2");
  assert.ok(f.entries.some(e => e.data.id === "delivery-1" && e.data.owner_resolved));
});

test("fenced active native turn reaches a safe boundary before new owner-resolved work", async () => {
  let rejectRunning = true;
  const f = fixture({ onCall: (op, args) => {
    if (op === "native.event" && args.phase === "running" && rejectRunning) throw Object.assign(new Error("fenced"), { code: "conflict", definite: true });
  } });
  await f.bridge.tick(); await f.start(); f.setIdle(false);
  f.setStatus({ active_id: "delivery-1", state: "uncertain" }); await f.bridge.tick();
  f.setStatus({ active_id: "", state: "ready" });
  await f.bridge.tick();
  assert.equal(f.getAborts(), 1);
  assert.equal(f.sent.length, 1);
  await f.finish("aborted", "aborted", true); f.setIdle(true); rejectRunning = false;
  Object.assign(f.delivery, { id: "delivery-2" }); f.setCandidate("new-work");
  await f.bridge.tick();
  assert.equal(f.sent.length, 2);
  assert.ok(!f.events().some(e => e.id === "delivery-1" && ["completed", "cancelled"].includes(e.phase)));
});

test("fenced marker survives extension reload without retrying an unaccepted receipt", async () => {
  const f = fixture({ entries: [{ type: "custom", customType: "agentmux.native.delivery", data: { id: "delivery-1", phase: "uncertain", fenced: true, reported: false } }] });
  f.setStatus({ active_id: "delivery-1", state: "uncertain" });
  await f.bridge.tick();
  assert.equal(f.sent.length, 0);
  assert.equal(f.events().length, 0);
  assert.match(f.statuses.at(-1), /owner resolution/);
});

test("CLI accepts bounded multi-megabyte broker responses and preserves definite faults", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "amx-pi-transport-"));
  const binary = path.join(dir, "fixture");
  fs.writeFileSync(binary, '#!/usr/bin/python3\nimport json,sys\nargs=json.load(sys.stdin)\nop=sys.argv[3]\nif args.get("reject"):\n print(json.dumps({"operation":op,"ok":False,"error":{"code":"conflict","message":"not exposed"}}));sys.exit(4)\nprint(json.dumps({"operation":op,"ok":True,"result":{"text":"x"*(2*1024*1024)}}))\n', { mode: 0o700 });
  const call = cliTransport(binary);
  try {
    const response = await call("native.status", {});
    assert.equal(response.text.length, 2 * 1024 * 1024);
    await assert.rejects(call("native.event", { reject: true }), error => error.definite === true && error.code === "conflict" && !error.message.includes("not exposed"));
  } finally { call.close(); fs.rmSync(dir, { recursive: true, force: true }); }
});

test("CLI preserves Unicode split across subprocess output chunks", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "amx-pi-unicode-"));
  const binary = path.join(dir, "fixture");
  fs.writeFileSync(binary, '#!/usr/bin/python3\nimport json,sys,time\njson.load(sys.stdin)\nraw=json.dumps({"operation":sys.argv[3],"ok":True,"result":{"text":"🌌"}},ensure_ascii=False).encode()\nsplit=raw.index("🌌".encode())+1\nsys.stdout.buffer.write(raw[:split]);sys.stdout.buffer.flush();time.sleep(.05)\nsys.stdout.buffer.write(raw[split:]);sys.stdout.buffer.flush()\n', { mode: 0o700 });
  const call = cliTransport(binary);
  try { assert.deepEqual(await call("native.claim", {}), { text: "🌌" }); }
  finally { call.close(); fs.rmSync(dir, { recursive: true, force: true }); }
});

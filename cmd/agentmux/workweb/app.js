"use strict";
const $ = (id) => document.getElementById(id);
const capabilityKey = "agentmux-owner-capability";
let token =
  location.hash.slice(1) || sessionStorage.getItem(capabilityKey) || "";
if (location.hash) {
  sessionStorage.setItem(capabilityKey, token);
  history.replaceState(null, "", location.pathname);
}
let snapshot = null,
  selected = "",
  peer = "",
  tab = "activity",
  loading = false,
  connected = false,
  mutating = false,
  replyTo = "",
  memoryVersion = 0,
  memoryNamespace = "",
  boundarySignature = "",
  agentSignature = "",
  taskSignature = "",
  boardFilter = "all",
  observedSignature = "";
const messageDrafts = new Map();
const titleDrafts = new Map(),
  observedDrafts = new Map();
let sessionQuery = "",
  titleEditor = null,
  titleEditorEpoch = 0;
let pendingDraftKind = "";
let memoryEditorNamespace = "",
  memoryEditorEpoch = 0,
  memoryRequest = 0;
const el = (tag, className, text) => {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
};
const notice = (text, error = false) => {
  $("notice").textContent = text;
  $("notice").classList.toggle("action-error", error);
};
const stamp = (text) => {
  const value = new Date(text);
  return Number.isNaN(+value)
    ? ""
    : value.toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      });
};
const providerName = (name) =>
  ({
    claude: "Claude",
    codex: "Codex",
    pi: "Pi",
    antigravity: "Antigravity",
    command: "Command",
  })[name] ||
  name ||
  "Agent";
const projectName = (path) =>
  path
    ? path
        .replace(/[\\/]+$/, "")
        .split(/[\\/]/)
        .pop() || path
    : "";
function sessionTitle(agent) {
  if (!agent) return "Session";
  if (agent.title) return agent.title;
  const project = projectName(agent.config?.workdir || agent.project),
    provider = providerName(agent.config?.harness || agent.harness);
  return project ? project + " · " + provider : provider;
}
function sessionReference(id) {
  const agent = snapshot?.agents?.find((a) => a.id === id);
  return agent ? sessionTitle(agent) + " (" + id + ")" : id;
}
function sessionOrigin(agent) {
  return agent.external ? "External bridge" : "Workspace";
}
// A registered bridge is not evidence that its original native terminal is
// currently observable. Neither a missing observation nor revocation exits it.
function sessionStatus(agent, runtime = {}, observations = []) {
  if (agent.external) {
    const observed =
      !!agent.external_ref &&
      observations.some((session) => session.id === agent.external_ref);
    const active = agent.state === "active" && observed;
    return {
      text:
        "bridge " +
        agent.state +
        " · native " +
        (observed ? "observed" : "not currently observed"),
      active,
      muted: !active,
    };
  }
  const state = runtime.agents?.[agent.id]?.state || agent.state;
  return {
    text: state,
    active: state === "running" || state === "active",
    muted: false,
  };
}
function terminalIdentity(observed) {
  return [
    observed?.tmux_session ? "tmux " + observed.tmux_session : "",
    observed?.pane || "",
    observed?.tty || "",
  ]
    .filter(Boolean)
    .join(" · ");
}
function sessionTime(text, label) {
  if (!text) return "";
  const date = new Date(text);
  return Number.isFinite(+date) && date.getUTCFullYear() > 1970
    ? label +
        " " +
        date.toLocaleString([], {
          month: "short",
          day: "numeric",
          hour: "2-digit",
          minute: "2-digit",
        })
    : "";
}
function matchesSession(item) {
  const text = [
    item.id,
    item.title,
    item.project,
    item.harness,
    item.config?.workdir,
    item.config?.harness,
    item.source,
    item.tmux_session,
    item.tty,
    item.external ? "external" : "workspace",
  ]
    .filter(Boolean)
    .join(" ")
    .toLocaleLowerCase();
  return sessionQuery
    .toLocaleLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((term) => text.includes(term));
}
function emptySearch(list, kind) {
  const box = el(
      "div",
      "empty",
      "No " + kind + " match “" + sessionQuery + "”.",
    ),
    clear = el("button", "quiet", "Clear search");
  clear.onclick = () => {
    $("session-search").value = "";
    sessionQuery = "";
    renderSessionLists();
    renderObserved();
  };
  box.append(document.createElement("br"), clear);
  list.append(box);
}
function renderSessionLists() {
  if (!snapshot) return;
  const all = snapshot.agents || [],
    agents = all.filter(matchesSession),
    list = $("agents");
  $("agent-count").textContent = sessionQuery
    ? agents.length + " / " + all.length
    : all.length;
  $("registered-count").textContent = agents.length + " OF " + all.length;
  const signature = JSON.stringify([
    selected,
    sessionQuery,
    (snapshot.observed || []).map((s) => [
      s.id,
      s.started_at,
      s.tty,
      s.tmux_session,
      s.pane,
    ]),
    agents.map((a) => [
      a.id,
      a.title,
      a.title_version,
      a.created_at,
      a.external,
      a.state,
      a.config.harness,
      a.config.workdir,
      a.config.interaction,
      snapshot.runtime.agents?.[a.id]?.state,
    ]),
  ]);
  if (signature === agentSignature) return;
  agentSignature = signature;
  list.replaceChildren();
  $("registered-sessions").replaceChildren();
  if (!agents.length) {
    emptySearch(list, "sessions");
    emptySearch($("registered-sessions"), "workspace sessions");
    return;
  }
  for (const item of agents) {
    const config = item.config,
      row = el("button", "agent-row" + (item.id === selected ? " active" : "")),
      title = sessionTitle(item),
      observed = (snapshot.observed || []).find(
        (s) => s.id === item.external_ref,
      );
    row.type = "button";
    row.dataset.agent = item.id;
    row.setAttribute("aria-pressed", String(item.id === selected));
    row.setAttribute(
      "aria-label",
      title + " · " + item.id + " · " + providerName(config.harness),
    );
    row.title = [
      title,
      item.id,
      config.workdir,
      sessionOrigin(item),
      sessionTime(item.created_at, "Added"),
      terminalIdentity(observed),
    ]
      .filter(Boolean)
      .join("\n");
    row.append(el("span", "agent-icon", config.harness.slice(0, 2)));
    const copy = el("span", "agent-copy");
    copy.append(
      el("strong", "", title),
      el("small", "", item.id + " · " + providerName(config.harness)),
      el(
        "small",
        "agent-project",
        projectName(config.workdir) || sessionOrigin(item),
      ),
    );
    row.append(copy);
    const status = sessionStatus(
        item,
        snapshot.runtime,
        snapshot.observed || [],
      ),
      dot = el("span", "agent-dot" + (status.active ? "" : " exited"));
    if (status.muted) dot.style.backgroundColor = "var(--faint)";
    dot.title = status.text;
    row.title += "\n" + status.text;
    row.append(dot);
    row.onclick = () => selectAgent(item.id);
    list.append(row);
    const entry = el("article", "session-row"),
      details = el("div"),
      open = el("button", "quiet", "Select");
    details.append(
      el("strong", "", title),
      el(
        "small",
        "",
        item.id +
          " · " +
          providerName(config.harness) +
          " · " +
          (item.external
            ? status.text
            : sessionOrigin(item) + " · " + status.text),
      ),
      el("small", "", config.workdir || "No project directory available"),
    );
    const metadata = [
      sessionTime(item.created_at, "Added"),
      sessionTime(observed?.started_at, "Started"),
      terminalIdentity(observed),
    ]
      .filter(Boolean)
      .join(" · ");
    if (metadata) details.append(el("small", "", metadata));
    open.setAttribute("aria-label", "Select " + title + " (" + item.id + ")");
    open.onclick = () => selectAgent(item.id);
    entry.append(details, open);
    $("registered-sessions").append(entry);
  }
}
function closeTitleEditor(preserve = false) {
  if (titleEditor) {
    if (preserve)
      titleDrafts.set(titleEditor.id, {
        title: $("task-title").value,
        version: titleEditor.version,
      });
    else titleDrafts.delete(titleEditor.id);
  }
  titleEditor = null;
  titleEditorEpoch++;
  $("title-editor").hidden = true;
}
function openTitleEditor() {
  if (!selectionReady()) return;
  const agent = snapshot.agents.find((a) => a.id === selected);
  if (!agent) return;
  const draft = titleDrafts.get(agent.id);
  titleEditor = {
    id: agent.id,
    version: draft?.version ?? agent.title_version ?? 0,
  };
  titleEditorEpoch++;
  $("title-editor-target").textContent = "Task title for " + agent.id;
  $("task-title").value = draft?.title ?? agent.title ?? "";
  $("title-editor").hidden = false;
  $("task-title").focus();
  updateTitleConflict();
}
function updateTitleConflict() {
  const current = snapshot?.agents?.find(
      (agent) => agent.id === titleEditor?.id,
    ),
    conflict = !!current && current.title_version !== titleEditor.version;
  if (titleEditor) titleEditor.conflict = conflict;
  $("title-conflict").hidden = !conflict;
  $("rebase-title").hidden = !conflict;
  $("rebase-title").disabled = !selectionReady() || mutating;
  $("save-title").disabled = !selectionReady() || conflict;
  $("clear-title").disabled = !selectionReady() || conflict;
  if (conflict)
    $("title-conflict").textContent =
      "The saved title changed to “" +
      (current.title || sessionTitle({ ...current, title: "" })) +
      "” (version " +
      current.title_version +
      "). Your draft is preserved. Review it before replacing this version.";
}
async function api(path, body) {
  const controller = new AbortController(),
    timeout = setTimeout(
      () => controller.abort(),
      body === undefined ? 8000 : 30000,
    );
  const options = {
    headers: { Authorization: "Bearer " + token },
    cache: "no-store",
    signal: controller.signal,
  };
  if (body !== undefined) {
    options.method = "POST";
    options.headers["Content-Type"] = "application/json";
    options.body = JSON.stringify(body);
  }
  try {
    const response = await fetch(path, options);
    const raw = await response.text();
    let value;
    try {
      value = JSON.parse(raw);
    } catch {
      const error = new Error(
        response.status === 401
          ? "The private owner link is missing or expired. Open the full link printed by agentmux web."
          : "The local console is unavailable (HTTP " + response.status + ").",
      );
      if (response.status === 401 || response.status === 403)
        error.code = "forbidden";
      throw error;
    }
    if (!response.ok || value.ok === false) {
      const error = new Error(value.error || "Operation failed");
      error.code = value.code;
      throw error;
    }
    return value;
  } catch (error) {
    if (controller.signal.aborted)
      throw new Error(
        body === undefined
          ? "Snapshot request timed out. Refresh to reconnect."
          : "Request timed out. The change may already be saved; refresh before retrying.",
      );
    throw error;
  } finally {
    clearTimeout(timeout);
  }
}
const call = async (operation, args = {}) =>
  (await api("/api/call", { operation, args })).result;
const selectionReady = () =>
  !!snapshot &&
  connected &&
  snapshot.selected === selected &&
  snapshot.boundaries.agent === selected &&
  snapshot.agents.some((agent) => agent.id === selected);
function selectionControls() {
  const ready = selectionReady(),
    agent = snapshot?.agents?.find((a) => a.id === selected);
  for (const id of [
    "send-message",
    "message-kind",
    "pause-agent",
    "focus-terminal",
    "copy-attach",
    "close-agent",
    "connect-pair",
    "isolate-agent",
    "graph-edit",
    "memory-namespace",
    "load-memory",
    "edit-title",
  ]) {
    $(id).disabled =
      !ready ||
      ([
        "send-message",
        "message-kind",
        "pause-agent",
        "focus-terminal",
        "connect-pair",
        "isolate-agent",
        "graph-edit",
      ].includes(id) &&
        agent?.state !== "active") ||
      (id === "pause-agent" && !agent?.config.autonomy?.enabled) ||
      (id === "focus-terminal" && !!agent?.external) ||
      (id === "close-agent" && agent?.state !== "active");
  }
  $("memory-form")
    .querySelectorAll("button")
    .forEach((node) => {
      node.disabled = !ready;
    });
  // An offline draft stays editable in its existing namespace. A pending
  // session switch must not expose the previous session's editor as current.
  $("memory-form")
    .querySelectorAll("input,textarea")
    .forEach((node) => {
      node.disabled = !ready && (connected || snapshot?.selected !== selected);
    });
  document
    .querySelectorAll(
      "#title-editor button[type=submit], #clear-title, #add-session button[type=submit], .session-actions button",
    )
    .forEach((node) => {
      node.disabled = !ready;
    });
  if (!ready)
    document.querySelectorAll(".toggle, [data-owner-write]").forEach((node) => {
      node.disabled = true;
    });
  if (!ready) {
    $("send-target").textContent =
      selected + (connected ? " (loading…)" : " (offline)");
    $("delivery-hint").textContent = connected
      ? "Loading this session's current boundaries before sending."
      : "Last snapshot shown. Reconnect before sending or changing permissions; drafts stay here.";
  }
  updateTitleConflict();
}
function selectAgent(id) {
  if (selected === id) return;
  if (selected)
    messageDrafts.set(selected, {
      text: $("message").value,
      kind: $("message-kind").value,
      reply: replyTo,
    });
  closeTitleEditor(true);
  selected = id;
  clearReply();
  boundarySignature = "";
  const draft = messageDrafts.get(id);
  $("message").value = draft?.text || "";
  pendingDraftKind = draft?.kind || "direct";
  if (draft?.reply) {
    replyTo = draft.reply;
    $("reply-context").textContent = "↳ " + draft.reply;
    $("clear-reply").classList.remove("hidden");
  }
  if (typeof graphSelection !== "undefined") graphSelection = null;
  document.querySelectorAll(".agent-row").forEach((row) => {
    const active = row.dataset.agent === id;
    row.classList.toggle("active", active);
    row.setAttribute("aria-pressed", String(active));
  });
  selectionControls();
  refresh();
}
async function action(fn, success) {
  if (mutating) return;
  if (snapshot && !selectionReady()) {
    notice(
      connected
        ? "Wait for the selected session's boundaries to load before making changes."
        : "Connection lost. Refresh to load current state before making changes. Drafts are preserved.",
      true,
    );
    return;
  }
  mutating = true;
  document.body.classList.add("busy");
  notice("Applying change…");
  try {
    await fn();
    notice(typeof success === "function" ? success() : success);
    await refresh();
  } catch (error) {
    const definite = [
      "conflict",
      "forbidden",
      "invalid",
      "not_found",
      "limit",
      "unsupported",
      "expired",
    ].includes(error.code);
    notice(
      error.message +
        (definite
          ? error.code === "conflict"
            ? " Refresh current state before editing again."
            : ""
          : " Refresh before retrying; delivery may be uncertain."),
      true,
    );
    await refresh();
  } finally {
    mutating = false;
    document.body.classList.remove("busy");
  }
}
async function refresh() {
  if (loading) return;
  loading = true;
  const requested = selected;
  try {
    const next = await api(
      "/api/snapshot?agent=" + encodeURIComponent(requested),
    );
    if (requested !== selected) return;
    if (!snapshot)
      notice("Owner console ready. Direct terminal input stays in tmux.");
    else if (!connected)
      notice("Reconnected. Current state loaded; drafts are preserved.");
    snapshot = next;
    connected = true;
    selected = next.selected;
    $("error-overlay").classList.add("hidden");
    $("connection").textContent = "Connected";
    $("connection").classList.remove("offline");
    render();
    $("updated").textContent = "Updated " + new Date().toLocaleTimeString();
  } catch (error) {
    connected = false;
    boundarySignature = taskSignature = observedSignature = "";
    $("connection").textContent = "Offline";
    $("connection").classList.add("offline");
    notice(
      error.message +
        (snapshot
          ? " Last snapshot shown; changes are disabled and drafts are preserved."
          : ""),
      true,
    );
    selectionControls();
    if (!snapshot) {
      $("error-message").textContent = error.message;
      $("error-overlay").classList.remove("hidden");
    }
  } finally {
    loading = false;
    if (requested !== selected) queueMicrotask(refresh);
  }
}
function render() {
  const agents = snapshot.agents || [],
    a = agents.find((item) => item.id === selected),
    b = snapshot.boundaries;
  $("session").textContent = snapshot.runtime.session || "Work session";
  $("runtime-state").textContent =
    "Runtime · " + (snapshot.runtime.state || "unavailable");
  renderSessionLists();
  if (!a) {
    selectionControls();
    return;
  }
  $("agent-name").textContent = sessionTitle(a);
  $("agent-name").title = sessionTitle(a);
  $("agent-id").textContent = a.id;
  const observed = (snapshot.observed || []).find(
    (s) => s.id === a.external_ref,
  );
  $("agent-origin").textContent = a.external
    ? "External " +
      sessionStatus(a, snapshot.runtime, snapshot.observed || []).text
    : sessionOrigin(a);
  $("agent-started").textContent = [
    sessionTime(a.created_at, "Added"),
    sessionTime(observed?.started_at, "Started"),
  ]
    .filter(Boolean)
    .join(" · ");
  $("agent-terminal").textContent = terminalIdentity(observed);
  $("agent-role").textContent = a.config.role || "No role configured";
  $("agent-harness").textContent =
    a.config.harness.toUpperCase() +
    " / " +
    (a.config.interaction || "managed").toUpperCase();
  $("agent-workdir").textContent = a.config.workdir || "Private workspace";
  $("agent-boundary").textContent =
    (a.config.sandbox || "local") + " · " + a.config.network;
  $("send-target").textContent = a.id;
  $("focus-terminal").disabled = !!a.external;
  $("close-agent").disabled = a.state !== "active";
  $("close-agent").textContent = a.external
    ? "Disconnect bridge"
    : "Close session";
  if (!$("new-project").value) $("new-project").value = a.config.workdir || "";
  $("bridge-panel").classList.toggle("hidden", !snapshot.bridge_setup);
  $("bridge-setup").textContent = snapshot.bridge_setup || "";
  $("copy-attach").textContent =
    a.external && !snapshot.attach_command ? "Copy setup" : "Attach command ↗";
  $("pause-agent").disabled = !a.config.autonomy?.enabled;
  $("pause-agent").textContent = a.config.autonomy?.enabled
    ? "Pause / resume autonomy"
    : "Human-led";
  const interactive = a.config.interaction === "interactive";
  $("delivery-hint").textContent = interactive
    ? "Inbox message only. Attach in tmux to type directly; native prompts and approvals stay in that terminal."
    : "Creates an authenticated instruction task for this managed worker. The worker runs it when ready.";
  $("message-kind").options[0].textContent = interactive
    ? "Direct inbox"
    : "Instruction task";
  const terminal =
    (a.external
      ? "Existing native conversation. Continue in its original terminal. Sessions → Copy setup connects it to coordination without restarting it.\n\n" +
        snapshot.bridge_setup
      : snapshot.preview) ||
    "No terminal output yet. Use Focus in tmux or copy the attach command.";
  if ($("terminal").textContent !== terminal)
    $("terminal").textContent = terminal;
  $("policy-version").textContent = "v" + b.version;
  const deadline = snapshot.activity.runtime_deadline;
  if (
    deadline &&
    Number.isFinite(+new Date(deadline)) &&
    new Date(deadline).getUTCFullYear() > 1970
  ) {
    const remaining = Math.max(
      0,
      Math.floor((new Date(deadline) - Date.now()) / 60000),
    );
    $("deadline").textContent = remaining + "m remaining";
  } else {
    $("deadline").textContent = "Human-led";
  }
  renderFeed();
  renderTasks();
  renderBoundaries();
  renderObserved();
  renderComposer();
  if (typeof renderGraph === "function" && tab === "graph") renderGraph();
  const ns = $("memory-namespace"),
    prior = ns.value;
  const names = (b.memory || [])
    .filter((item) => item.read || item.write)
    .map((item) => item.name);
  if ([...ns.options].map((o) => o.value).join("\n") !== names.join("\n")) {
    ns.replaceChildren(
      ...names.map((name) => {
        const option = el("option", "", scopeLabel(name));
        option.value = name;
        return option;
      }),
    );
    if (names.includes(prior)) ns.value = prior;
  }
  resetMemoryNamespace(ns.value);
  $("memory-form").hidden = names.length === 0;
  if (!names.length)
    $("memory-list").replaceChildren(
      el(
        "p",
        "empty",
        "No shared memory enabled for this session. Connect a collaboration group or grant a namespace in Graph.",
      ),
    );
  if (tab === "memory" && memoryNamespace !== ns.value) loadMemory();
  selectionControls();
}
function renderFeed() {
  const feed = $("feed"),
    messages = (snapshot.activity.messages || []).filter((m) =>
      boardFilter === "team"
        ? !!m.board
        : boardFilter === "agent"
          ? !m.board && (m.from === selected || (m.to || []).includes(selected))
          : true,
    );
  feed.replaceChildren();
  $("feed-count").textContent = messages.length + " RECENT";
  if (!messages.length) {
    feed.append(
      el(
        "p",
        "empty",
        "No coordination yet. Post a team note or ask an agent to read its inbox.",
      ),
    );
    return;
  }
  for (const m of messages) {
    const item = el("article", "feed-item"),
      content = el("div");
    item.append(el("span", "feed-icon", m.board ? "#" : "↗"));
    const heading = el("div", "feed-title");
    heading.append(
      el("strong", "", sessionReference(m.from)),
      el(
        "span",
        "destination",
        "→ " +
          (m.board
            ? "#" + scopeLabel(m.board)
            : (m.to || []).map(sessionReference).join(", ")),
      ),
      el("time", "", stamp(m.created_at)),
    );
    content.append(heading, el("p", "", m.text));
    if (!m.board && (m.from !== "$owner" || m.to?.length === 1)) {
      const reply = el("button", "quiet reply-button", "Reply");
      reply.type = "button";
      reply.onclick = () => setReply(m.id);
      content.append(reply);
    }
    item.append(content);
    feed.append(item);
  }
}
function renderTasks() {
  const tasks = snapshot.activity.tasks || [],
    list = $("tasks");
  $("task-count").textContent = tasks.length;
  const nextTasks = JSON.stringify([
    tasks,
    (snapshot.agents || []).map((a) => [a.id, a.title]),
  ]);
  if (nextTasks === taskSignature) return;
  taskSignature = nextTasks;
  list.replaceChildren();
  if (!tasks.length) {
    list.append(
      el(
        "p",
        "empty",
        "No managed tasks. Human input in native sessions is handled by the CLI itself.",
      ),
    );
    return;
  }
  for (const t of tasks) {
    const item = el("article", "task-item"),
      title = el("div", "task-title");
    title.append(
      el(
        "strong",
        "",
        sessionReference(t.from) + " → " + sessionReference(t.to),
      ),
      el("span", "task-state", t.state + " / " + t.delivery),
      el("code", "", t.id),
    );
    item.append(title, el("p", "", t.text));
    if (t.result) item.append(el("pre", "", t.result));
    if (t.usage) {
      const details = el("details", "reported-usage"),
        summary = el("summary", "", "Provider-reported usage");
      details.append(
        summary,
        el("pre", "", JSON.stringify(t.usage, null, 2).slice(0, 2000)),
      );
      item.append(details);
    }
    const buttons = el("div", "task-buttons"),
      reply = el("button", "quiet", "Reply");
    reply.onclick = () => setReply(t.id);
    buttons.append(reply);
    if (["proposed", "running"].includes(t.state)) {
      const cancel = el("button", "quiet", "Cancel task");
      cancel.dataset.ownerWrite = "true";
      cancel.disabled = !selectionReady();
      cancel.onclick = () =>
        action(
          () => call("task.cancel", { id: t.id, version: t.version }),
          "Cancellation requested. Check the process-stop acknowledgement.",
        );
      buttons.append(cancel);
    }
    item.append(buttons);
    list.append(item);
  }
}
function toggle(value, label, onchange) {
  const button = el("button", "toggle");
  button.type = "button";
  button.setAttribute("role", "switch");
  button.setAttribute("aria-label", label);
  button.setAttribute("aria-checked", String(value));
  button.onclick = onchange;
  return button;
}
function renderBoundaries() {
  const agents = snapshot.agents || [],
    b = snapshot.boundaries,
    select = $("target-agent");
  if (b.agent !== selected) return;
  if (
    !agents.some((a) => a.id === peer && a.state === "active") ||
    peer === selected
  )
    peer =
      agents.find((a) => a.id !== selected && a.state === "active")?.id ||
      selected;
  const next = JSON.stringify([
    selected,
    peer,
    b,
    agents.map((a) => [a.id, a.title]),
  ]);
  if (next === boundarySignature) return;
  boundarySignature = next;
  const options = agents.map((a) => [a.id, sessionReference(a.id)]);
  if (
    JSON.stringify([...select.options].map((o) => [o.value, o.textContent])) !==
    JSON.stringify(options)
  )
    select.replaceChildren(
      ...options.map(([id, label]) => {
        const option = el("option", "", label);
        option.value = id;
        return option;
      }),
    );
  select.value = peer;
  const controls = $("edge-controls");
  controls.replaceChildren();
  const actions = b.targets?.find((t) => t.to === peer)?.actions || [];
  const source = selected,
    recipient = peer;
  for (const [key, label, description] of [
    ["message.send", "Messages", "Notes in the peer inbox"],
    ["ping.send", "Pings", "May wake a subscribed managed worker"],
    ["instruct", "Commands", "Create instruction tasks"],
  ]) {
    const row = el("div", "control-row"),
      name = el("div", "control-name");
    name.append(el("strong", "", label), el("small", "", description));
    const enabled = actions.includes(key);
    const control = toggle(
      enabled,
      source + " → " + recipient + ": " + label,
      () =>
        action(
          () =>
            call("control.update", {
              version: b.version,
              kind: "edge",
              from: source,
              to: recipient,
              action: key,
              allow: !enabled,
            }),
          "Saved " +
            source +
            " → " +
            recipient +
            " " +
            key +
            ". Broker enforcement is immediate.",
        ),
    );
    control.disabled =
      (!!agents.find((a) => a.id === source)?.parent &&
        !agents.find((a) => a.id === source)?.owner_managed) ||
      (!!agents.find((a) => a.id === recipient)?.parent &&
        !agents.find((a) => a.id === recipient)?.owner_managed) ||
      agents.find((a) => a.id === source)?.state !== "active" ||
      agents.find((a) => a.id === recipient)?.state !== "active";
    if (control.disabled)
      control.title = "Spawned agents use their inherited delegation limits.";
    row.append(name, control);
    controls.append(row);
  }
  const scopes = $("scope-controls");
  scopes.replaceChildren();
  for (const [kind, values] of [
    ["board", b.boards || []],
    ["memory", b.memory || []],
  ])
    for (const scope of values) {
      const row = el("div", "scope-row"),
        name = el("div", "scope-name");
      row.dataset.scope = kind + ":" + scope.name;
      name.append(
        el("strong", "", scopeLabel(scope.name)),
        el("small", "", kind),
      );
      row.append(name);
      for (const field of ["read", "write"]) {
        const control = toggle(
          scope[field],
          source + " " + scope.name + ": " + field,
          () =>
            action(
              () =>
                call("control.update", {
                  version: b.version,
                  kind,
                  agent: source,
                  namespace: scope.name,
                  read: field === "read" ? !scope.read : scope.read,
                  write: field === "write" ? !scope.write : scope.write,
                }),
              "Updated " +
                source +
                " access to " +
                scope.name +
                ". Previously read data remains known.",
            ),
        );
        control.disabled =
          (!!agents.find((a) => a.id === source)?.parent &&
            !agents.find((a) => a.id === source)?.owner_managed) ||
          agents.find((a) => a.id === source)?.state !== "active";
        if (control.disabled)
          control.title =
            "Spawned agents use their inherited delegation limits.";
        row.append(control);
      }
      scopes.append(row);
    }
  if (!scopes.childElementCount)
    scopes.append(el("p", "empty", "No shared namespaces configured."));
}
function replyRecipient(id) {
  const task = (snapshot?.activity.tasks || []).find((item) => item.id === id),
    message = (snapshot?.activity.messages || []).find(
      (item) => item.id === id,
    ),
    target = task
      ? task.to
      : message?.from !== "$owner"
        ? message?.from
        : message?.to?.length === 1
          ? message.to[0]
          : "";
  return (snapshot?.agents || []).some(
    (agent) => agent.id === target && agent.state === "active",
  )
    ? target
    : "";
}
function setReply(id) {
  const recipient = replyRecipient(id);
  if (!recipient) {
    notice(
      "The original reply recipient is unavailable. Refresh and review before sending.",
      true,
    );
    return;
  }
  selectAgent(recipient);
  pendingDraftKind = "direct";
  activateTab("activity");
  replyTo = id;
  $("reply-context").textContent = "↳ " + id;
  $("clear-reply").classList.remove("hidden");
  $("message-kind").value = "direct";
  renderComposer();
  $("message").focus();
}
function clearReply() {
  replyTo = "";
  $("reply-context").textContent = "";
  $("clear-reply").classList.add("hidden");
}
async function loadMemory() {
  const namespace = $("memory-namespace").value;
  resetMemoryNamespace(namespace);
  if (!namespace || !selectionReady()) return;
  const request = ++memoryRequest,
    epoch = memoryEditorEpoch;
  try {
    const result = await call("memory.list", { namespace, limit: 100 });
    if (
      request !== memoryRequest ||
      epoch !== memoryEditorEpoch ||
      namespace !== $("memory-namespace").value ||
      !selectionReady()
    )
      return;
    memoryNamespace = namespace;
    const list = $("memory-list");
    list.replaceChildren();
    for (const entry of result.entries || []) {
      const button = el("button", "memory-entry");
      button.append(
        el("strong", "", entry.key),
        el("span", "", "v" + entry.version),
      );
      button.onclick = () => {
        if (
          epoch !== memoryEditorEpoch ||
          namespace !== $("memory-namespace").value ||
          !selectionReady()
        )
          return;
        $("memory-key").value = entry.key;
        $("memory-value").value = entry.value;
        memoryVersion = entry.version;
        $("memory-version").textContent = "Editing version " + memoryVersion;
      };
      list.append(button);
    }
    if (!list.childElementCount)
      list.append(el("p", "empty", "No entries in this shared namespace."));
  } catch (error) {
    notice(error.message, true);
  }
}
function resetMemoryNamespace(namespace) {
  if (memoryEditorNamespace === namespace) return;
  memoryEditorNamespace = namespace;
  memoryEditorEpoch++;
  memoryRequest++;
  memoryNamespace = "";
  memoryVersion = 0;
  $("memory-key").value = "";
  $("memory-value").value = "";
  $("memory-version").textContent = "New entry · version 0";
  $("memory-list").replaceChildren();
}
$("refresh").onclick = refresh;
$("retry").onclick = refresh;
$("clear-reply").onclick = clearReply;
$("target-agent").onchange = (event) => {
  peer = event.target.value;
  if (graphSelection?.kind === "edge")
    graphSelection = { ...graphSelection, target: peer };
  renderBoundaries();
  graphInspector();
  drawGraph();
};
$("copy-attach").onclick = async () => {
  try {
    await navigator.clipboard.writeText(
      snapshot.attach_command || snapshot.bridge_setup,
    );
    notice(
      "Attach command copied. Run it in your terminal to enter " +
        selected +
        ".",
    );
  } catch {
    notice(snapshot?.attach_command || "No session selected");
  }
};
$("focus-terminal").onclick = () =>
  action(
    () => api("/api/focus", { target: selected }),
    "Focused " +
      selected +
      " in the existing tmux session. F1 returns to the workbench.",
  );
$("pause-agent").onclick = () => {
  const id = selected;
  action(
    async () => {
      const result = await call("autonomy.status", { agent: id });
      const version = result.status.version,
        mode = result.status.mode === "paused" ? "running" : "paused";
      await call("autonomy.control", { agent: id, version, mode });
    },
    "Autonomy updated for " +
      id +
      ". In-flight work requires task cancellation.",
  );
};
function activateTab(next) {
  tab = next;
  document
    .querySelectorAll("[data-tab]")
    .forEach((node) =>
      node.classList.toggle("active", node.dataset.tab === tab),
    );
  document
    .querySelectorAll(".view")
    .forEach((node) =>
      node.classList.toggle("active", node.id === "view-" + tab),
    );
  $("composer").classList.toggle("hidden", tab !== "activity");
  if (tab === "memory") loadMemory();
  if (tab === "graph" && typeof renderGraph === "function") renderGraph();
}
document
  .querySelectorAll("[data-tab]")
  .forEach(
    (button) => (button.onclick = () => activateTab(button.dataset.tab)),
  );
$("board-filter").onchange = (event) => {
  boardFilter = event.target.value;
  renderFeed();
};
$("composer").onsubmit = (event) => {
  event.preventDefault();
  const text = $("message").value;
  if (!text.trim() || !selectionReady()) return;
  const recipient = snapshot.selected,
    reference = replyTo,
    destination = $("message-kind").value,
    board = destination.startsWith("board:") ? destination.slice(6) : "";
  if (!board && reference && replyRecipient(reference) !== recipient) {
    notice(
      "The original reply recipient is unavailable or changed. Refresh and review the reply before sending.",
      true,
    );
    return;
  }
  let confirmation = "Posted to #" + board + ".";
  action(
    async () => {
      if (board) await call("board.post", { board, text });
      else {
        const result = await api("/api/send", {
          target: recipient,
          text,
          reply_to: reference,
        });
        confirmation =
          result.kind +
          " queued for " +
          sessionReference(result.target) +
          (result.kind === "Message"
            ? ". Native terminal input was not changed."
            : ". The managed worker runs this instruction when ready.");
      }
      const draft = messageDrafts.get(recipient);
      if (
        draft?.text === text &&
        draft.reply === reference &&
        draft.kind === destination
      )
        messageDrafts.delete(recipient);
      if (
        selected === recipient &&
        $("message").value === text &&
        replyTo === reference &&
        $("message-kind").value === destination
      ) {
        $("message").value = "";
        clearReply();
      }
    },
    () => confirmation,
  );
};
$("message").onkeydown = (event) => {
  if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
    event.preventDefault();
    $("composer").requestSubmit();
  }
};
$("load-memory").onclick = loadMemory;
$("memory-namespace").onchange = () => {
  resetMemoryNamespace($("memory-namespace").value);
  loadMemory();
};
$("memory-key").oninput = () => {
  memoryVersion = 0;
  $("memory-version").textContent =
    "New key · version 0. Select an existing entry before updating it.";
};
$("memory-value").oninput = () => {
  $("memory-version").textContent =
    "Unsaved changes · based on version " + memoryVersion;
};
$("memory-form").onsubmit = (event) => {
  event.preventDefault();
  if (!selectionReady()) return;
  const namespace = $("memory-namespace").value,
    key = $("memory-key").value,
    value = $("memory-value").value,
    version = memoryVersion,
    epoch = memoryEditorEpoch;
  action(async () => {
    const result = await call("memory.put", { namespace, key, value, version });
    if (
      epoch === memoryEditorEpoch &&
      namespace === $("memory-namespace").value &&
      key === $("memory-key").value &&
      version === memoryVersion
    ) {
      memoryVersion = result.entry.version;
      $("memory-version").textContent =
        $("memory-value").value === value
          ? "Saved version " + memoryVersion
          : "Unsaved changes · based on version " + memoryVersion;
    }
    if (epoch === memoryEditorEpoch) await loadMemory();
  }, "Shared memory saved with version checking.");
};
refresh();
setInterval(() => {
  if (!document.hidden) refresh();
}, 1500);
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) refresh();
});

function renderComposer() {
  if (!snapshot) return;
  if (!selectionReady()) {
    selectionControls();
    return;
  }
  const a = (snapshot.agents || []).find((a) => a.id === selected),
    select = $("message-kind"),
    prior = select.value;
  const boards = (snapshot.boundaries.boards || [])
    .filter((s) => s.read || s.write)
    .map((s) => s.name);
  const values = ["direct", ...boards.map((b) => "board:" + b)];
  if (
    [...select.options].map((o) => o.value).join("\n") !== values.join("\n")
  ) {
    select.replaceChildren(
      ...values.map((value) => {
        const option = el(
          "option",
          "",
          value === "direct"
            ? a?.config.interaction === "interactive"
              ? "Direct inbox"
              : "Instruction task"
            : "#" + scopeLabel(value.slice(6)),
        );
        option.value = value;
        return option;
      }),
    );
    if (values.includes(prior)) select.value = prior;
  }
  if (pendingDraftKind) {
    if (values.includes(pendingDraftKind)) select.value = pendingDraftKind;
    pendingDraftKind = "";
  }
  const board = select.value.startsWith("board:") ? select.value.slice(6) : "";
  $("send-target").textContent = board
    ? "#" + scopeLabel(board)
    : sessionReference(selected);
  $("delivery-hint").textContent = board
    ? "Shared board. Only sessions allowed to read this namespace receive its context."
    : a?.config.interaction === "interactive"
      ? "Inbox message. Direct conversation and native approvals remain in the agent terminal."
      : "Instruction task for this managed worker. The worker runs it when ready.";
}
function renderObserved() {
  const connected = new Set(
    (snapshot.agents || [])
      .filter((a) => a.external && a.state === "active")
      .map((a) => a.external_ref),
  );
  const observed = (snapshot.observed || []).filter(
    (s) => !connected.has(s.id),
  );
  const filtered = observed.filter(matchesSession);
  const signature = JSON.stringify([sessionQuery, filtered]);
  if (signature === observedSignature) return;
  observedSignature = signature;
  const list = $("observed-sessions");
  list.replaceChildren();
  if (!observed.length) {
    list.append(
      el(
        "p",
        "empty",
        "No other identifiable live native terminals found. Detection uses process names and TTYs; generic node wrappers may be undiscoverable.",
      ),
    );
    return;
  }
  if (!filtered.length) {
    emptySearch(list, "observed terminals");
    return;
  }
  for (const s of filtered) {
    const row = el("article", "session-row"),
      copy = el("div");
    copy.append(
      el("strong", "", s.title || sessionTitle(s)),
      el(
        "small",
        "",
        providerName(s.harness) + " · " + s.id + " · observed only",
      ),
      el("small", "", s.project || "Project unavailable"),
    );
    const metadata = [sessionTime(s.started_at, "Started"), terminalIdentity(s)]
      .filter(Boolean)
      .join(" · ");
    if (metadata) copy.append(el("small", "", metadata));
    const controls = el("div", "session-actions"),
      title = el("input"),
      name = el("input"),
      details = el("details"),
      summary = el("summary", "", "Stable ID");
    const draft = observedDrafts.get(s.id);
    title.value = draft?.title || "";
    title.placeholder = "Task title (optional)";
    title.maxLength = 120;
    title.setAttribute("aria-label", "Task title for observed " + s.id);
    name.value = draft?.name || "";
    name.placeholder = "Generated automatically";
    name.setAttribute("aria-label", "Agentmux ID for " + s.id);
    const saveDraft = () =>
      observedDrafts.set(s.id, { title: title.value, name: name.value });
    title.oninput = saveDraft;
    name.oninput = saveDraft;
    details.append(summary, name);
    const connect = el("button", "", "Connect");
    connect.onclick = () => {
      const id = name.value,
        taskTitle = title.value,
        observedID = s.id,
        initialSelection = selected;
      action(async () => {
        const result = await api("/api/connect", {
          observed_id: observedID,
          name: id,
          title: taskTitle,
        });
        observedDrafts.delete(observedID);
        if (selected === initialSelection) {
          selectAgent(result.id);
          $("bridge-setup").textContent = result.setup;
          $("bridge-panel").classList.remove("hidden");
        }
      }, "Bridge ready. Copy setup and paste it in the original native session. Its conversation was not changed.");
    };
    controls.append(title, details, connect);
    row.append(copy, controls);
    list.append(row);
  }
}
$("message-kind").onchange = renderComposer;
$("add-session").onsubmit = (event) => {
  event.preventDefault();
  const name = $("new-name").value,
    title = $("new-title").value,
    harness = $("new-harness").value,
    project = $("new-project").value,
    role = $("new-role").value,
    sandbox = $("new-sandbox").value,
    initialSelection = selected;
  action(async () => {
    const result = await api("/api/add", {
      name,
      title,
      harness,
      project,
      role,
      sandbox,
    });
    if (selected === initialSelection) selectAgent(result.id);
    if ($("new-name").value === name) $("new-name").value = "";
    if ($("new-title").value === title) $("new-title").value = "";
  }, "Session added. Collaboration stays off until you allow it.");
};
$("copy-bridge").onclick = async () => {
  try {
    await navigator.clipboard.writeText($("bridge-setup").textContent);
    notice(
      "Setup copied. Paste it once into the existing native conversation.",
    );
  } catch {
    notice("Select and copy the setup text below.");
  }
};
$("close-agent").onclick = () => {
  const id = selected;
  action(
    () => call("agent.stop", { id }),
    "Closed Agentmux session " +
      id +
      ". External terminals, if any, remain in their original window.",
  );
};
$("connect-pair").onclick = () => {
  const from = selected,
    to = peer,
    version = snapshot.boundaries.version;
  action(async () => {
    const result = await call("control.update", {
      version,
      kind: "collaboration",
      agents: [from, to],
      enabled: true,
    });
    notice(
      "Connected " +
        from +
        " and " +
        to +
        ". Shared scope: " +
        result.group?.id,
    );
  }, "Collaboration enabled with a dedicated board and memory. Native terminals stay interactive.");
};
$("isolate-agent").onclick = () => {
  const id = selected,
    version = snapshot.boundaries.version;
  action(
    () =>
      call("control.update", { version, kind: "independent", agents: [id] }),
    "Session is independent. Native interaction continues; accepted tasks and previously read context remain.",
  );
};

function scopeLabel(name) {
  const group = (snapshot?.groups || []).find(
    (g) => g.board === name || g.memory === name,
  );
  return group ? group.agents.map(sessionReference).join(" + ") : name;
}

$("session-search").oninput = () => {
  sessionQuery = $("session-search").value.trim();
  renderSessionLists();
  renderObserved();
};
$("edit-title").onclick = openTitleEditor;
$("cancel-title").onclick = () => closeTitleEditor(false);
$("clear-title").onclick = () => {
  $("task-title").value = "";
  $("title-editor").requestSubmit();
};
$("rebase-title").onclick = () => {
  if (mutating || !selectionReady() || !titleEditor) return;
  const current = snapshot.agents.find((agent) => agent.id === titleEditor.id);
  if (!current) return;
  titleEditor.version = current.title_version;
  updateTitleConflict();
};
$("task-title").onkeydown = (event) => {
  if (event.key === "Escape") {
    event.preventDefault();
    closeTitleEditor(false);
  }
};
$("title-editor").onsubmit = (event) => {
  event.preventDefault();
  if (!titleEditor || titleEditor.conflict || !selectionReady()) return;
  const { id, version } = titleEditor,
    title = $("task-title").value,
    epoch = titleEditorEpoch;
  action(
    async () => {
      const result = await call("agent.label", { id, title, version }),
        nextVersion = result.agent.title_version;
      const draft = titleDrafts.get(id);
      if (draft?.version === version) {
        if (draft.title === title) titleDrafts.delete(id);
        else titleDrafts.set(id, { ...draft, version: nextVersion });
      }
      if (titleEditorEpoch === epoch && titleEditor?.id === id) {
        if ($("task-title").value === title) closeTitleEditor(false);
        else titleEditor.version = nextVersion;
      }
    },
    title
      ? "Task title saved for " + id + "."
      : "Task title cleared for " + id + ".",
  );
};

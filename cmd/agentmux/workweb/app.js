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
async function api(path, body) {
  const options = {
    headers: { Authorization: "Bearer " + token },
    cache: "no-store",
  };
  if (body !== undefined) {
    options.method = "POST";
    options.headers["Content-Type"] = "application/json";
    options.body = JSON.stringify(body);
  }
  const response = await fetch(path, options);
  const raw = await response.text();
  let value;
  try {
    value = JSON.parse(raw);
  } catch {
    throw new Error(
      response.status === 401
        ? "The private owner link is missing or expired. Open the full link printed by agentmux web."
        : "The local console is unavailable (HTTP " + response.status + ").",
    );
  }
  if (!response.ok || value.ok === false)
    throw new Error(value.error || "Operation failed");
  return value;
}
const call = async (operation, args = {}) =>
  (await api("/api/call", { operation, args })).result;
const selectionReady = () =>
  !!snapshot &&
  snapshot.selected === selected &&
  snapshot.boundaries.agent === selected;
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
    .querySelectorAll("input,textarea,button")
    .forEach((node) => {
      node.disabled = !ready;
    });
  if (!ready) {
    $("send-target").textContent = selected + " (loading…)";
    $("delivery-hint").textContent =
      "Loading this session's current boundaries before sending.";
  }
}
function selectAgent(id) {
  if (selected === id) return;
  if (selected)
    messageDrafts.set(selected, {
      text: $("message").value,
      kind: $("message-kind").value,
      reply: replyTo,
    });
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
      "Wait for the selected session's boundaries to load before making changes.",
      true,
    );
    return;
  }
  mutating = true;
  document.body.classList.add("busy");
  notice("Applying change…");
  try {
    await fn();
    notice(success);
    await refresh();
  } catch (error) {
    notice(
      error.message + " Refresh before retrying; delivery may be uncertain.",
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
    snapshot = next;
    selected = next.selected;
    $("error-overlay").classList.add("hidden");
    $("connection").textContent = "Connected";
    $("connection").classList.remove("offline");
    render();
    $("updated").textContent = "Updated " + new Date().toLocaleTimeString();
  } catch (error) {
    $("connection").textContent = "Offline";
    $("connection").classList.add("offline");
    notice(error.message, true);
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
  $("agent-count").textContent = agents.length;
  const list = $("agents"),
    nextAgents = JSON.stringify([
      selected,
      agents.map((a) => [
        a.id,
        a.state,
        a.config.interaction,
        snapshot.runtime.agents?.[a.id]?.state,
      ]),
    ]);
  if (nextAgents !== agentSignature) {
    agentSignature = nextAgents;
    list.replaceChildren();
    for (const item of agents) {
      const config = item.config,
        row = el(
          "button",
          "agent-row" + (item.id === selected ? " active" : ""),
        );
      row.type = "button";
      row.dataset.agent = item.id;
      row.setAttribute("aria-pressed", String(item.id === selected));
      row.append(el("span", "agent-icon", config.harness.slice(0, 2)));
      const copy = el("span", "agent-copy");
      copy.append(
        el("strong", "", item.id),
        el(
          "small",
          "",
          item.external ? "external bridge" : config.interaction || "managed",
        ),
      );
      row.append(copy);
      const status = snapshot.runtime.agents?.[item.id]?.state || item.state;
      const dot = el(
        "span",
        "agent-dot" +
          (status !== "running" && status !== "active" ? " exited" : ""),
      );
      dot.title = status;
      row.append(dot);
      row.onclick = () => selectAgent(item.id);
      list.append(row);
    }
  }
  if (!a) return;
  $("agent-name").textContent = a.id;
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
      el("strong", "", m.from),
      el(
        "span",
        "destination",
        "→ " + (m.board ? "#" + scopeLabel(m.board) : (m.to || []).join(", ")),
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
  const nextTasks = JSON.stringify(tasks);
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
      el("strong", "", t.from + " → " + t.to),
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
  const next = JSON.stringify([selected, peer, b, agents.map((a) => a.id)]);
  if (next === boundarySignature) return;
  boundarySignature = next;
  if (
    [...select.options].map((o) => o.value).join("\n") !==
    agents.map((a) => a.id).join("\n")
  )
    select.replaceChildren(
      ...agents.map((a) => {
        const option = el("option", "", a.id);
        option.value = a.id;
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
function setReply(id) {
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
    board = $("message-kind").value.startsWith("board:")
      ? $("message-kind").value.slice(6)
      : "";
  action(
    async () => {
      if (board) await call("board.post", { board, text });
      else
        await api("/api/send", {
          target: recipient,
          text,
          reply_to: reference,
        });
      if (messageDrafts.get(recipient)?.text === text)
        messageDrafts.delete(recipient);
      if (selected === recipient && $("message").value === text) {
        $("message").value = "";
        clearReply();
      }
    },
    board
      ? "Posted to #" + board + "."
      : "Queued for " + recipient + ". Native terminal input was not changed.",
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
      $("memory-version").textContent = "Saved version " + memoryVersion;
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
  $("send-target").textContent = board ? "#" + scopeLabel(board) : selected;
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
  const signature = JSON.stringify(observed);
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
  for (const s of observed) {
    const row = el("article", "session-row"),
      copy = el("div");
    copy.append(
      el("strong", "", s.harness + " · " + (s.title || s.tty || s.id)),
      el("small", "", s.project || "Original terminal"),
      el("small", "", s.id + " · observed only"),
    );
    const controls = el("div", "session-actions"),
      name = el("input");
    name.value = s.harness + "_" + s.pid;
    name.setAttribute("aria-label", "Agentmux ID for " + s.id);
    const connect = el("button", "", "Connect");
    connect.onclick = () => {
      const id = name.value;
      action(async () => {
        const result = await api("/api/connect", {
          observed_id: s.id,
          name: id,
        });
        selectAgent(result.id);
        $("bridge-setup").textContent = result.setup;
        $("bridge-panel").classList.remove("hidden");
      }, "Bridge ready. Copy setup and paste it in the original native session. Its conversation was not changed.");
    };
    controls.append(name, connect);
    row.append(copy, controls);
    list.append(row);
  }
}
$("message-kind").onchange = renderComposer;
$("add-session").onsubmit = (event) => {
  event.preventDefault();
  const name = $("new-name").value,
    harness = $("new-harness").value,
    project = $("new-project").value,
    role = $("new-role").value,
    sandbox = $("new-sandbox").value;
  action(async () => {
    const result = await api("/api/add", {
      name,
      harness,
      project,
      role,
      sandbox,
    });
    selectAgent(result.id);
    $("new-name").value = "";
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
  return group ? group.agents.join(" + ") : name;
}

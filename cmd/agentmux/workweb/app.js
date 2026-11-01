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
  observedSignature = "",
  teamsSignature = "",
  feedSignature = "",
  lifecycleSignature = "";
let teamReview = null;
let researchReview = null;
let researchSavedOffline = false;
const teamInviteDrafts = new Map();
const teamEditDrafts = new Map();
const legacyGroupChoices = new Map();
let legacyGroupReview = null;
const messageDrafts = new Map();
const roleDrafts = new Map();
let roleEditingID = "";
const titleDrafts = new Map(),
  observedDrafts = new Map();
let sessionQuery = "",
  titleEditor = null,
  titleEditorEpoch = 0;
let pendingDraftKind = "";
let taskHistory = { tasks: null, cursor: "", hasMore: false, query: "", to: "", state: "", loading: false, epoch: 0, updatedAt: 0, error: "" };
let workspaceListings = [], workspaceLoading = false, workspaceLoadedAt = 0;
let relaunchReview = null, capacityView = null, capacityRequest = 0;
let resourceSnapshot = null, resourceReview = null;
const resourceDrafts = new Map();
let memoryEditorNamespace = "",
  memoryMetadataSelection = "",
  memoryEditorEpoch = 0,
  memoryRequest = 0,
  memoryBaseline = { namespace: "", key: "", value: "" },
  memoryReview = null,
  memoryReviewRequest = 0;
const memoryDrafts = new Map(),
  memoryLastDrafts = new Map(),
  memoryEntries = new Map();
const el = (tag, className, text) => {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
};
function captureViewState(root) {
  const active = document.activeElement,
    inside = !!active && root.contains(active),
    item = inside ? active.closest("[data-item-key]") : null;
  return {
    focusKey: inside ? active.dataset?.focusKey || "" : "",
    itemKey: item?.dataset.itemKey || "",
    inside,
    open: new Set(
      [...root.querySelectorAll("[data-disclosure-key]")]
        .filter((node) => node.open)
        .map((node) => node.dataset.disclosureKey),
    ),
    scroller: root.closest(".graph-canvas,.inspector,.view"),
    scrollTop: root.closest(".graph-canvas,.inspector,.view")?.scrollTop || 0,
    scrollLeft: root.closest(".graph-canvas,.inspector,.view")?.scrollLeft || 0,
  };
}
function restoreViewState(root, state) {
  for (const node of root.querySelectorAll("[data-disclosure-key]"))
    node.open = state.open.has(node.dataset.disclosureKey);
  if (state.inside) {
    const control = [...root.querySelectorAll("[data-focus-key]")].find(
        (node) => node.dataset.focusKey === state.focusKey && !node.disabled,
      ),
      item = [...root.querySelectorAll("[data-item-key]")].find(
        (node) => node.dataset.itemKey === state.itemKey,
      ),
      target = control || item || root;
    if (!control) target.setAttribute("tabindex", "-1");
    target.focus({ preventScroll: true });
  }
  if (state.scroller) {
    state.scroller.scrollTop = state.scrollTop;
    state.scroller.scrollLeft = state.scrollLeft;
  }
}
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
  if (agent.parent && !agent.owner_managed && agent.template)
    return agent.template + " · " + provider;
  return project ? project + " · " + provider : provider;
}
function sessionReference(id) {
  const agent = snapshot?.agents?.find((a) => a.id === id);
  return agent ? sessionTitle(agent) + " (" + id + ")" : id;
}
function sessionOrigin(agent) {
  return agent.external ? "External bridge" : "Workspace";
}
function sessionLineage(agent) {
  if (!agent.parent) return "";
  if (agent.owner_managed) return "Owner-created session";
  return (
    "Spawned by " +
    sessionReference(agent.parent) +
    (agent.template ? " · template " + agent.template : "") +
    " · depth " +
    agent.depth
  );
}
function activeDescendants(agents, id) {
  const descendants = new Set(),
    parents = new Set([id]);
  let changed = true;
  while (changed) {
    changed = false;
    for (const agent of agents) {
      if (
        agent.id !== id &&
        agent.state === "active" &&
        parents.has(agent.parent) &&
        !descendants.has(agent.id)
      ) {
        descendants.add(agent.id);
        parents.add(agent.id);
        changed = true;
      }
    }
  }
  return [...descendants];
}
function nativeHeadless(agent) {
  if (typeof snapshot?.headless === "boolean")
    return snapshot.headless && (!agent || agent.id === snapshot.selected);
  const pane = snapshot?.runtime?.agents?.[agent?.id];
  return (
    !!(snapshot?.research || snapshot?.research_error) &&
    !(
      snapshot.runtime?.attempt_id &&
      pane?.state === "running" &&
      /^%\d+$/.test(pane.pane_id)
    )
  );
}
function researchHeadless(agent) {
  return (
    !!(snapshot?.research || snapshot?.research_error) && nativeHeadless(agent)
  );
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
    item.parent,
    item.template,
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
    renderSessionLists(true);
    renderObserved();
    $("session-search").focus();
  };
  box.append(document.createElement("br"), clear);
  list.append(box);
}
function renderSessionLists(announceSearch = false) {
  if (!snapshot) return;
  const all = snapshot.agents || [],
    list = $("agents");
  if (researchSavedOffline && !all.length) {
    if (announceSearch)
      $("session-search-status").textContent =
        "Session registry not loaded. Search results are unavailable.";
    $("agent-count").textContent = "—";
    $("registered-count").textContent = "NOT LOADED";
    list.replaceChildren(
      el(
        "p",
        "empty",
        "Agent registry not loaded. Only saved research metadata is available.",
      ),
    );
    $("registered-sessions").replaceChildren(
      el("p", "empty", "Agent sessions have not been loaded."),
    );
    return;
  }
  const agents = all.filter(matchesSession);
  if (announceSearch) {
    const message = sessionQuery
      ? agents.length +
        " of " +
        all.length +
        " sessions match “" +
        sessionQuery +
        "”."
      : all.length + (all.length === 1 ? " session shown." : " sessions shown.");
    if ($("session-search-status").textContent !== message)
      $("session-search-status").textContent = message;
  }
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
  const rosterState = captureViewState(list),
    registeredState = captureViewState($("registered-sessions"));
  list.replaceChildren();
  $("registered-sessions").replaceChildren();
  if (!agents.length) {
    emptySearch(list, "sessions");
    emptySearch($("registered-sessions"), "workspace sessions");
    restoreViewState(list, rosterState);
    restoreViewState($("registered-sessions"), registeredState);
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
    row.dataset.focusKey = item.id;
    row.setAttribute("aria-pressed", String(item.id === selected));
    row.setAttribute(
      "aria-label",
      [title, item.id, item.title ? providerName(config.harness) : ""]
        .filter(Boolean)
        .join(" · "),
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
      el(
        "small",
        "",
        item.id + (item.title ? " · " + providerName(config.harness) : ""),
      ),
    );
    const project = projectName(config.workdir);
    if (!project || (project !== item.id && project !== title && !title.startsWith(project + " · ")))
      copy.append(el("small", "agent-project", project || sessionOrigin(item)));
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
    open.dataset.focusKey = item.id;
    open.onclick = () => selectAgent(item.id);
    entry.append(details, open);
    $("registered-sessions").append(entry);
  }
  restoreViewState(list, rosterState);
  restoreViewState($("registered-sessions"), registeredState);
}
function closeTitleEditor(preserve = false) {
  const restoreFocus = $("title-editor").contains(document.activeElement);
  if (titleEditor) {
    if (preserve)
      titleDrafts.set(titleEditor.id, {
        title: $("task-title").value,
        version: titleEditor.version,
        baseline: titleEditor.baseline,
      });
    else titleDrafts.delete(titleEditor.id);
  }
  titleEditor = null;
  titleEditorEpoch++;
  $("title-editor").hidden = true;
  if (restoreFocus && !$("edit-title").disabled) $("edit-title").focus();
}
function openTitleEditor() {
  if (!selectionReady()) return;
  const agent = snapshot.agents.find((a) => a.id === selected);
  if (!agent) return;
  const draft = titleDrafts.get(agent.id);
  titleEditor = {
    id: agent.id,
    version: draft?.version ?? agent.title_version ?? 0,
    baseline: draft?.baseline ?? agent.title ?? "",
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
function validSavedResearch(value) {
  const object = (v) => !!v && typeof v === "object" && !Array.isArray(v),
    integer = (v, min = 0) => Number.isSafeInteger(v) && v >= min,
    time = (v) => typeof v === "string" && Number.isFinite(Date.parse(v));
  if (!object(value) || !object(value.progress) || !object(value.control))
    return false;
  const p = value.progress,
    c = value.control;
  return (
    typeof p.trial === "string" &&
    !!p.trial.trim() &&
    p.trial.length <= 256 &&
    integer(p.attempt, 1) &&
    integer(p.revision, 1) &&
    integer(p.round) &&
    integer(c.version, 1) &&
    integer(p.ack_version, 1) &&
    p.ack_version <= c.version &&
    [
      "starting",
      "barrier",
      "paused",
      "running",
      "completed",
      "interrupted",
      "stopped",
    ].includes(p.state) &&
    ["running", "paused", "step", "stopped"].includes(c.mode) &&
    typeof p.condition === "string" &&
    typeof p.phase === "string" &&
    Number.isFinite(p.seed) &&
    time(p.deadline) &&
    time(p.updated_at) &&
    integer(p.model_tasks_reserved) &&
    integer(p.model_tasks_limit) &&
    Array.isArray(p.population) &&
    p.population.length <= 16 &&
    p.population.every((id) => typeof id === "string" && id.length > 0) &&
    object(p.tasks) &&
    Object.keys(p.tasks).length <= 16 &&
    Object.values(p.tasks).every((state) => typeof state === "string") &&
    Array.isArray(c.interventions) &&
    c.interventions.length <= 129 &&
    c.interventions.every(
      (item) =>
        object(item) &&
        integer(item.version, 1) &&
        item.version <= c.version &&
        ["pause", "resume", "step", "stop"].includes(item.action) &&
        typeof item.reason === "string" &&
        typeof item.actor === "string" &&
        integer(item.round) &&
        typeof item.phase === "string" &&
        time(item.at),
    ) &&
    (value.owner_mutations === undefined || integer(value.owner_mutations))
  );
}
function savedResearchSnapshot(research) {
  return {
    selected: "",
    agents: [],
    groups: [],
    teams: [],
    observed: [],
    boundaries: { agent: "", version: 0, targets: [], boards: [], memory: [] },
    activity: {
      messages: [],
      tasks: [],
      events: [],
      runtime_deadline: research.progress.deadline,
    },
    runtime: { agents: {} },
    headless: true,
    research: { ...research, runner_heartbeat_at: "" },
  };
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
      if (value.live === false) error.savedResearch = value.saved_research;
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
    agent = snapshot?.agents?.find((a) => a.id === selected),
    recipient = snapshot?.agents?.find((a) => a.id === peer),
    editable = (item) =>
      !!item && item.state === "active" && (!item.parent || item.owner_managed);
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
      (id === "pause-agent" &&
        !agent?.config.autonomy?.enabled &&
        !researchHeadless(agent)) ||
      (id === "focus-terminal" &&
        (!!agent?.external || nativeHeadless(agent))) ||
      (id === "copy-attach" && nativeHeadless(agent)) ||
      (id === "connect-pair" &&
        (!editable(agent) || !editable(recipient) || peer === selected)) ||
      (id === "isolate-agent" && !editable(agent)) ||
      (id === "close-agent" && agent?.state !== "active") ||
      (id === "send-message" &&
        $("message-kind").value.startsWith("board:") &&
        !(snapshot?.boundaries.boards || []).some(
          (scope) =>
            scope.name === $("message-kind").value.slice(6) && scope.write,
        ));
  }
  $("connect-pair").title = !ready
    ? "Load current boundaries before editing."
    : !editable(agent) || !editable(recipient) || peer === selected
      ? "Choose two active owner-managed sessions. Delegated agents retain inherited limits."
      : "";
  $("isolate-agent").title = editable(agent)
    ? ""
    : "Delegated agents retain inherited limits; manage their parent or stop the agent.";
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
  renderMemoryReview();
  $("review-research").disabled =
    !ready ||
    !!snapshot?.research_error ||
    !researchMutable(snapshot?.research);
  const researchConfirm = $("confirm-research");
  if (researchConfirm)
    researchConfirm.disabled = !researchReviewReady(researchReview);
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
      kind: pendingDraftKind || $("message-kind").value,
      mode: $("message-mode").value || "auto",
      reply: replyTo,
    });
  closeTitleEditor(true);
  if (relaunchReview && relaunchReview.target !== id) { relaunchReview = null; $("relaunch-review").hidden = true; }
  selected = id;
  clearReply();
  boundarySignature = "";
  const draft = messageDrafts.get(id);
  $("message").value = draft?.text || "";
  pendingDraftKind = draft?.kind || "direct";
  $("message-mode").value = draft?.mode || "auto";
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
    const initial = !snapshot;
    if (initial)
      notice(
        next.research
          ? "Research owner console ready. Phase controls act at barriers; tasks, teams and memory stay observable."
          : next.headless
            ? "Managed task console ready. Tasks, lifecycle events and results stay observable."
            : "Owner console ready. Direct terminal input stays in tmux.",
      );
    else if (!connected)
      notice("Reconnected. Current state loaded; drafts are preserved.");
    snapshot = next;
    researchSavedOffline = false;
    connected = true;
    selected = next.selected;
    $("error-overlay").classList.add("hidden");
    $("connection").textContent = "Connected";
    $("connection").classList.remove("offline");
    render();
    if (tab === "tasks" && Date.now() - taskHistory.updatedAt > 10000) loadTaskHistory("refresh");
    if (tab === "sessions" && Date.now() - workspaceLoadedAt > 10000) loadWorkspaces();
    if (tab === "agent" && capacityView?.agent !== selected) loadCapacity();
    if (
      initial &&
      tab === "activity" &&
      nativeHeadless(next.agents.find((agent) => agent.id === next.selected))
    )
      activateTab(next.research ? "research" : "tasks");
    $("updated").textContent = "Updated " + new Date().toLocaleTimeString();
  } catch (error) {
    connected = false;
    boundarySignature = taskSignature = observedSignature = teamsSignature = "";
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
    let bootstrapped = false;
    if (!snapshot && validSavedResearch(error.savedResearch)) {
      snapshot = savedResearchSnapshot(error.savedResearch);
      selected = "";
      researchSavedOffline = true;
      bootstrapped = true;
      render();
      activateTab("research");
      $("error-overlay").classList.add("hidden");
      $("runtime-state").textContent =
        "Broker offline · saved research state: " +
        snapshot.research.progress.state;
      notice(
        "Broker offline · saved research state. Only durable research metadata is available; controls remain disabled.",
        true,
      );
      selectionControls();
    }
    if (!snapshot) {
      $("error-message").textContent = error.message;
      $("error-overlay").classList.remove("hidden");
    }
    if (snapshot?.research && !bootstrapped) {
      const prior = snapshot.research;
      try {
        const saved = await api("/api/research-status");
        const current = snapshot.research,
          recovered = saved.research;
        if (
          saved.live === false &&
          recovered &&
          recovered.progress.trial === prior.progress.trial &&
          recovered.progress.attempt === prior.progress.attempt &&
          current.progress.trial === prior.progress.trial &&
          current.progress.attempt === prior.progress.attempt &&
          recovered.progress.revision >= current.progress.revision &&
          recovered.control.version >= current.control.version
        ) {
          snapshot.research = { ...recovered, runner_heartbeat_at: "" };
          researchSavedOffline = true;
          renderResearch();
          $("runtime-state").textContent =
            "Broker offline · saved research state: " +
            recovered.progress.state;
          notice(
            "Broker offline · saved research state: " +
              recovered.progress.state +
              ". Controls remain disabled; reconnect before making changes.",
            true,
          );
          selectionControls();
        }
      } catch {
        // Retain the original connection error. This read never retries a write
        // or replaces live routing, panes, permissions or agent selection.
      }
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
  $("session").textContent = snapshot.research
    ? snapshot.research.progress.trial +
      " · " +
      snapshot.research.progress.condition +
      " · attempt " +
      snapshot.research.progress.attempt
    : snapshot.runtime.session ||
      (snapshot.headless ? "Managed tasks" : "Work session");
  $("session").title = $("session").textContent;
  $("runtime-state").textContent = snapshot.research
    ? "Research · " + snapshot.research.progress.state
    : "Runtime · " + (snapshot.runtime.state || "unavailable");
  renderSessionLists();
  renderTeams();
  renderLifecycle();
  renderResearch();
  renderGlobalHeading();
  if (!a) {
    if (researchSavedOffline && snapshot.research) renderMetadataOnly();
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
  $("agent-lineage").textContent = sessionLineage(a);
  renderWorkerRuntime(a);
  $("agent-role").textContent = a.config.role || "No role configured";
  renderRoleEditor();
  const relaunchable = a.state === "active" && !a.external && a.config.interaction === "interactive" && snapshot.runtime.relaunch_version === 1 && snapshot.runtime.state === "running" && !!snapshot.runtime.agents?.[a.id] && snapshot.runtime.agents[a.id].state !== "running";
  $("relaunch-agent").hidden = !relaunchable;
  $("relaunch-agent").disabled = !selectionReady() || !relaunchable;
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
    : activeDescendants(agents, a.id).length
      ? "Close " + (activeDescendants(agents, a.id).length + 1) + " sessions"
      : "Close session";
  $("close-agent").title = a.external
    ? "Disconnect this bridge; its original native terminal remains."
    : "Revoke " +
      [a.id, ...activeDescendants(agents, a.id)].join(", ") +
      ". Process termination is observed separately.";
  if (!$("new-project").value) $("new-project").value = a.config.workdir || "";
  $("bridge-panel").classList.toggle("hidden", !snapshot.bridge_setup);
  $("bridge-setup").textContent = snapshot.bridge_setup || "";
  $("copy-attach").textContent =
    a.external && !snapshot.attach_command ? "Copy setup" : "Attach command ↗";
  $("pause-agent").disabled = !a.config.autonomy?.enabled;
  $("pause-agent").textContent = researchHeadless(a)
    ? "Phase controls"
    : a.config.autonomy?.enabled
      ? "Pause / resume autonomy"
      : nativeHeadless(a)
        ? "Managed task"
        : "Human-led";
  const interactive = a.config.interaction === "interactive";
  $("delivery-hint").textContent = interactive
    ? "Inbox message only. Attach in tmux to type directly; native prompts and approvals stay in that terminal."
    : "Creates an authenticated instruction task for this managed worker. The worker runs it when ready.";
  $("message-kind").options[0].textContent = interactive
    ? "Direct inbox"
    : "Instruction task";
  const headless = nativeHeadless(a),
    research = researchHeadless(a);
  $("research-memory-hint").hidden = !research;
  if (headless)
    $("workbench-shortcuts").textContent = research
      ? "Headless research · use Research, Tasks and Teams"
      : "Headless managed run · Tasks and Teams / events";
  else
    $("workbench-shortcuts").replaceChildren(
      el("kbd", "", "F1"),
      document.createTextNode(" terminal workbench"),
      document.createElement("br"),
      el("kbd", "", "Ctrl-b b"),
      document.createTextNode(" shared board"),
    );
  if (headless) $("copy-attach").textContent = "Headless worker";
  $("native-observation-title").textContent = headless
    ? "Worker observation"
    : "Native terminal";
  $("native-observation-label").textContent = headless
    ? research
      ? "HEADLESS RESEARCH · TASKS & TRACES"
      : "HEADLESS MANAGED RUN · TASKS & TRACES"
    : "LIVE OBSERVATION · INPUT IN TMUX";
  const terminal =
    (headless
      ? research
        ? "Headless research worker. Open Tasks for instructions, results, provenance and adapter trace summaries. Research controls apply at phase barriers. This worker has no owned native terminal to attach to."
        : "Headless managed worker. Open Tasks for instructions, results, provenance and adapter trace summaries, and Teams for lifecycle events. No native terminal is attached. This one-shot runner may not dispatch additional owner tasks."
      : a.external
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
  const signature = JSON.stringify([
    boardFilter,
    selected,
    messages,
    (snapshot.agents || []).map((a) => [a.id, a.title]),
    snapshot.groups,
    snapshot.teams,
  ]);
  if (signature === feedSignature) return;
  feedSignature = signature;
  const state = captureViewState(feed);
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
    restoreViewState(feed, state);
    return;
  }
  for (const m of messages) {
    const item = el("article", "feed-item"),
      content = el("div");
    item.dataset.itemKey = m.id;
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
    appendThreadMetadata(content,m);
    if (m.board || m.from !== "$owner" || m.to?.length === 1) {
      const reply = el("button", "quiet reply-button", "Reply");
      reply.type = "button";
      reply.dataset.focusKey = "reply:" + m.id;
      reply.onclick = () => setReply(m.id);
      content.append(reply);
    }
    item.append(content);
    feed.append(item);
  }
  restoreViewState(feed, state);
}
function renderTasks() {
  const tasks = taskHistory.tasks || snapshot.activity.tasks || [],
    list = $("tasks");
  $("task-count").textContent = tasks.length + (taskHistory.hasMore ? "+" : "");
  $("task-history-status").textContent = taskHistory.loading
    ? "Loading task history… Current results and drafts stay here."
    : taskHistory.error
      ? "History could not refresh: " + taskHistory.error + ". Last results retained."
      : taskHistory.tasks === null
        ? "Recent snapshot shown. Open Tasks to load complete searchable history."
        : tasks.length + " matching task(s) loaded · newest first" + (taskHistory.hasMore ? " · older tasks available" : " · end of history") + (taskHistory.query || taskHistory.to || taskHistory.state ? " · filters applied" : "");
  $("load-more-tasks").hidden = !taskHistory.hasMore;
  $("load-more-tasks").disabled = taskHistory.loading || !connected;
  const recipients = $("task-agent-filter"), priorRecipient = recipients.value;
  const recipientOptions = [["", "All recipients"], ...(snapshot.agents || []).map(a => [a.id, sessionReference(a.id)])];
  if (JSON.stringify([...recipients.options].map(o => [o.value,o.textContent])) !== JSON.stringify(recipientOptions)) {
    recipients.replaceChildren(...recipientOptions.map(([id,label]) => { const option = el("option","",label); option.value=id; return option; }));
    recipients.value = priorRecipient || taskHistory.to;
  }
  const nextTasks = JSON.stringify([
    tasks,
    (snapshot.agents || []).map((a) => [a.id, a.title]),
  ]);
  if (nextTasks === taskSignature) return;
  taskSignature = nextTasks;
  const state = captureViewState(list);
  list.replaceChildren();
  if (!tasks.length) {
    list.append(
      el(
        "p",
        "empty",
        taskHistory.tasks === null ? "No tasks in the recent snapshot. Open Tasks to search complete history." : "No tasks match these filters. Human input in native sessions is handled by the CLI itself.",
      ),
    );
    restoreViewState(list, state);
    return;
  }
  for (const t of tasks) {
    const item = el("article", "task-item"),
      title = el("div", "task-title");
    item.dataset.itemKey = t.id;
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
    appendThreadMetadata(item,t);
    if (t.result) item.append(el("pre", "", t.result));
    if (t.usage) {
      const details = el("details", "reported-usage"),
        summary = el("summary", "", "Provider-reported usage");
      details.dataset.disclosureKey = "usage:" + t.id;
      summary.dataset.focusKey = "usage:" + t.id;
      details.append(
        summary,
        el("pre", "", JSON.stringify(t.usage, null, 2).slice(0, 2000)),
      );
      item.append(details);
    }
    if (t.origin || t.trace) {
      const details = el("details", "reported-usage"),
        summary = el("summary", "", "Origin & adapter trace");
      details.dataset.disclosureKey = "origin:" + t.id;
      summary.dataset.focusKey = "origin:" + t.id;
      details.append(summary);
      if (t.origin)
        details.append(
          el("pre", "", JSON.stringify(t.origin, null, 2).slice(0, 4000)),
        );
      if (t.trace)
        details.append(
          el(
            "p",
            "",
            "Recorded trace " +
              t.trace.id +
              " · " +
              t.trace.records +
              " records · " +
              (t.trace.complete ? "complete" : "incomplete") +
              ". Coverage: " +
              t.trace.coverage +
              ". This view has not reverified the trace file.",
          ),
        );
      item.append(details);
    }
    const buttons = el("div", "task-buttons"),
      reply = el("button", "quiet", "Reply");
    reply.dataset.focusKey = "reply:" + t.id;
    reply.onclick = () => setReply(t.id);
    buttons.append(reply);
    if (["proposed", "running"].includes(t.state)) {
      const cancel = el("button", "quiet", "Cancel task");
      cancel.dataset.ownerWrite = "true";
      cancel.dataset.focusKey = "cancel:" + t.id;
      cancel.disabled = !selectionReady();
      cancel.onclick = () =>
        action(
          async () => { await call("task.cancel", { id: t.id, version: t.version }); taskHistory.updatedAt = 0; },
          "Cancellation requested. Check the process-stop acknowledgement.",
        );
      buttons.append(cancel);
    }
    item.append(buttons);
    list.append(item);
  }
  restoreViewState(list, state);
}
async function loadTaskHistory(mode = "refresh") {
  if (!connected || researchSavedOffline || (taskHistory.loading && mode !== "reset")) return;
  const epoch = ++taskHistory.epoch, query = taskHistory.query, to = taskHistory.to, state = taskHistory.state;
  const append = mode === "more", target = append ? 50 : Math.max(50, taskHistory.tasks?.length || 0);
  let cursor = append ? taskHistory.cursor : "", rows = [], hasMore = false;
  taskHistory.loading = true;
  taskHistory.error = "";
  renderTasks();
  try {
    do {
      const page = await call("task.list", { limit:50, order:"desc", after:cursor, query, to, state });
      if (epoch !== taskHistory.epoch) return;
      const incoming = page.tasks || [], nextCursor = page.cursor || cursor;
      rows.push(...incoming);
      hasMore = page.has_more === true;
      if (!incoming.length || nextCursor === cursor) { hasMore = false; break; }
      cursor = nextCursor;
    } while (hasMore && rows.length < target);
    if (epoch !== taskHistory.epoch) return;
    const merged = append ? [...(taskHistory.tasks || []), ...rows] : rows;
    taskHistory.tasks = [...new Map(merged.map(task => [task.id,task])).values()];
    taskHistory.cursor = cursor;
    taskHistory.hasMore = hasMore;
    taskHistory.updatedAt = Date.now();
    taskSignature = "";
  } catch (error) {
    if (epoch === taskHistory.epoch) { taskHistory.error = error.message; taskHistory.updatedAt = Date.now(); }
  } finally {
    if (epoch === taskHistory.epoch) { taskHistory.loading = false; renderTasks(); }
  }
}
function appendThreadMetadata(node,item) {
  if (!item.reply_to && !item.thread_id) return;
  const details=el("details","thread-context"),summary=el("summary","",item.reply_to ? "Reply details" : "Conversation details"),key="thread:"+(item.id || item.thread_id || item.reply_to);
  details.dataset.disclosureKey=key;summary.dataset.focusKey=key;
  details.append(summary);
  if(item.reply_to) details.append(el("p","","Reply to: "),el("code","",item.reply_to));
  if(item.thread_id) details.append(el("p","","Conversation root: "),el("code","",item.thread_id));
  node.append(details);
}
$("task-history-filter").onsubmit = event => {
  event.preventDefault();
  taskHistory.query = $("task-query").value.trim();
  taskHistory.to = $("task-agent-filter").value;
  taskHistory.state = $("task-state-filter").value;
  taskHistory.tasks = []; taskHistory.cursor = ""; taskHistory.hasMore = false;
  loadTaskHistory("reset");
};
$("load-more-tasks").onclick = () => loadTaskHistory("more");
$("reset-task-filter").onclick = () => {
  $("task-query").value = $("task-agent-filter").value = $("task-state-filter").value = "";
  $("task-history-filter").requestSubmit();
};
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
  if (!peer || peer === selected)
    peer =
      agents.find((a) => a.id !== selected && a.state === "active")?.id ||
      selected;
  const next = JSON.stringify([
    selected,
    peer,
    b,
    agents.map((a) => [a.id, a.title, a.state]),
  ]);
  if (next === boundarySignature) return;
  boundarySignature = next;
  const options = agents.map((a) => [
    a.id,
    sessionReference(a.id) + (a.state === "active" ? "" : " · " + a.state),
  ]);
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
  const controlsState = captureViewState(controls),
    scopesState = captureViewState($("scope-controls"));
  controls.replaceChildren();
  const targetBoundary = b.targets?.find((t) => t.to === peer), actions = targetBoundary?.actions || [];
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
    const reason = targetBoundary?.sources?.[key] || targetBoundary?.denied_reasons?.[key];
    if (reason) name.append(el("small", "permission-source", reason));
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
    control.dataset.focusKey = JSON.stringify([source, recipient, key]);
    row.dataset.itemKey = control.dataset.focusKey;
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
    if (reason?.startsWith("explicit owner")) {
      const reset=el("button","quiet","Use underlying policy");
      reset.type="button"; reset.dataset.ownerWrite="true"; reset.dataset.focusKey=JSON.stringify([source,recipient,key,"reset"]); reset.disabled=control.disabled;
      reset.onclick=()=>action(()=>call("control.update",{version:b.version,kind:"edge",from:source,to:recipient,action:key,reset:true}),"Removed this explicit override. Current configuration, delegation and active groups now determine access.");
      name.append(reset);
    }
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
      if (scope.reason) name.append(el("small",scope.suspended?"action-error":"permission-source",scope.reason));
      if (scope.stored_read !== undefined || scope.stored_write !== undefined) name.append(el("small","permission-source","Stored owner intent: read " + !!scope.stored_read + " · write " + !!scope.stored_write + ". Switches show effective access."));
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
        control.dataset.focusKey = JSON.stringify([
          source,
          kind,
          scope.name,
          field,
        ]);
        row.dataset.itemKey = JSON.stringify([source, kind, scope.name]);
        control.disabled =
          scope.source === "team" ||
          scope.suspended ||
          (scope.source === "group" && !scope.read && !scope.write && (scope.reason?.includes("member") || scope.reason?.includes("inactive"))) ||
          (!!agents.find((a) => a.id === source)?.parent &&
            !agents.find((a) => a.id === source)?.owner_managed) ||
          agents.find((a) => a.id === source)?.state !== "active";
        if (control.disabled)
          control.title =
            scope.source === "team"
              ? "Consented team membership controls this scope. Manage invitations and membership in Teams."
              : scope.suspended || scope.source === "group" && (scope.reason?.includes("member") || scope.reason?.includes("inactive"))
                ? scope.reason + ". Reconnect explicitly in Teams before granting access."
              : "Spawned agents use their inherited delegation limits.";
        row.append(control);
      }
      if ((scope.stored_read !== undefined || scope.stored_write !== undefined) && scope.source !== "team") {
        const reset = el("button","quiet","Reset owner override"); reset.type="button"; reset.dataset.ownerWrite="true"; reset.dataset.focusKey=JSON.stringify([source,kind,scope.name,"reset"]);
        reset.onclick=()=>action(()=>call("control.update",{version:b.version,kind,agent:source,namespace:scope.name,reset:true}),"Removed this namespace override. Configuration or current group membership now determines access.");
        name.append(reset);
      }
      scopes.append(row);
    }
  if (!scopes.childElementCount)
    scopes.append(el("p", "empty", "No shared namespaces configured."));
  restoreViewState(controls, controlsState);
  restoreViewState(scopes, scopesState);
}
function replyRecipient(id) {
  const task = [...(taskHistory.tasks || []), ...(snapshot?.activity.tasks || [])].find((item) => item.id === id),
    message = (snapshot?.activity.messages || []).find(
      (item) => item.id === id,
    ),
    target = message?.board ? "" : task
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
  const message = (snapshot?.activity.messages || []).find(item => item.id === id);
  if (message?.board) {
    const from = snapshot.agents.find(agent => agent.id === message.from && agent.state === "active");
    if (from) selectAgent(from.id);
    pendingDraftKind = "board:" + message.board;
    activateTab("activity");
    replyTo = id;
    $("reply-context").textContent = "↳ #" + scopeLabel(message.board) + " · " + id;
    $("clear-reply").classList.remove("hidden");
    $("message-mode").value = "note";
    renderComposer();
    $("message").focus();
    return;
  }
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
function memoryDraftKey(namespace, key) {
  return JSON.stringify([namespace, key]);
}
function memoryDirty() {
  return (
    !!memoryEditorNamespace &&
    (memoryBaseline.namespace !== memoryEditorNamespace ||
      $("memory-key").value !== memoryBaseline.key ||
      $("memory-value").value !== memoryBaseline.value)
  );
}
function stashMemoryDraft() {
  if (!memoryEditorNamespace) return;
  const key = $("memory-key").value,
    identity = memoryDraftKey(memoryEditorNamespace, key);
  if (memoryDirty()) {
    memoryDrafts.set(identity, {
      namespace: memoryEditorNamespace,
      key,
      value: $("memory-value").value,
      version: memoryVersion,
      baseline: { ...memoryBaseline },
    });
    memoryLastDrafts.set(memoryEditorNamespace, identity);
  } else memoryDrafts.delete(identity);
}
function renderMemoryReview() {
  renderMemoryMetadata();
  const review = memoryReview;
  $("memory-review").hidden = !review;
  if (!review) return;
  $("memory-review-target").textContent =
    review.namespace +
    " / " +
    review.key +
    " · " +
    review.state +
    " · version " +
    review.version;
  $("memory-review-draft").textContent = $("memory-value").value;
  $("memory-review-latest").textContent =
    review.state === "live"
      ? review.value
      : "No live value (" + review.state + ").";
  $("adopt-memory-version").textContent =
    "Use version " + review.version + " for my draft";
  $("adopt-memory-version").disabled =
    !selectionReady() ||
    mutating ||
    review.epoch !== memoryEditorEpoch ||
    review.namespace !== memoryEditorNamespace ||
    review.key !== $("memory-key").value;
}
function renderMemoryMetadata() {
  const panel=$("memory-metadata"),namespace=memoryEditorNamespace,key=$("memory-key").value;
  panel.hidden=true;
  if(!selectionReady() || memoryMetadataSelection!==selected || !namespace || namespace!==$("memory-namespace").value || memoryVersion<1) return;
  const entry=(memoryEntries.get(namespace) || []).find(item=>item.key===key && item.version===memoryVersion);
  if(!entry) return;
  const signature=JSON.stringify([selected,namespace,key,entry.version,entry.author,entry.sha256,entry.updated_at,entry.sources]);
  panel.hidden=false;
  if(panel.dataset.signature===signature) return;
  panel.dataset.signature=signature;
  const view=captureViewState(panel);
  panel.replaceChildren(el("h3","","Saved metadata · "+namespace+" / "+key+" · version "+entry.version),el("p","session-note","Metadata describes this exact saved version, not unsaved draft edits. Author is the authenticated destination writer; imported source identities are claims, not verified origin."));
  panel.append(el("p","","Destination author: "+(entry.author || "not recorded")),el("code","","Saved value SHA-256: "+(entry.sha256 || "not recorded")));
  if(entry.updated_at) panel.append(el("p","session-note","Saved "+entry.updated_at));
  const sources=entry.sources || [];
  if(sources.length) {
    const details=el("details","memory-source-claims"),summary=el("summary","",sources.length+" source claim(s) · origin not verified");
    details.dataset.disclosureKey=JSON.stringify([namespace,key,entry.version,"sources"]);summary.dataset.focusKey=details.dataset.disclosureKey;
    details.append(summary,el("pre","",JSON.stringify(sources,null,2)));panel.append(details);
  } else panel.append(el("p","session-note","No source claims recorded for this saved version."));
  restoreViewState(panel,view);
}
function memoryEdit(entry, namespace) {
  memoryMetadataSelection=selected;
  memoryEditorEpoch++;
  memoryReviewRequest++;
  memoryReview = null;
  const draft = memoryDrafts.get(memoryDraftKey(namespace, entry.key));
  $("memory-key").value = entry.key;
  $("memory-value").value = draft ? draft.value : entry.value;
  memoryVersion = draft ? draft.version : entry.version;
  memoryBaseline = draft
    ? { ...draft.baseline }
    : { namespace, key: entry.key, value: entry.value };
  $("memory-version").textContent = draft
    ? "Draft kept · based on version " +
      memoryVersion +
      ". Review latest before saving if changed elsewhere."
    : "Editing version " + memoryVersion;
  renderMemoryReview();
}
function renderMemoryEntries(namespace) {
  if (namespace !== memoryEditorNamespace) return;
  const list = $("memory-list"),
    state = captureViewState(list),
    entries = new Map(
      (memoryEntries.get(namespace) || []).map((entry) => [entry.key, entry]),
    );
  for (const draft of memoryDrafts.values())
    if (draft.namespace === namespace && !entries.has(draft.key))
      entries.set(draft.key, { key: draft.key, value: "", version: 0 });
  list.replaceChildren();
  for (const entry of entries.values()) {
    const button = el("button", "memory-entry"),
      hasDraft = memoryDrafts.has(memoryDraftKey(namespace, entry.key));
    button.dataset.focusKey = memoryDraftKey(namespace, entry.key);
    button.append(
      el("strong", "", entry.key || "Untitled draft"),
      el("span", "", "v" + entry.version + (hasDraft ? " · draft" : "")),
    );
    button.disabled = !selectionReady();
    button.onclick = () => {
      if (
        namespace !== memoryEditorNamespace ||
        namespace !== $("memory-namespace").value ||
        !selectionReady()
      )
        return;
      stashMemoryDraft();
      // Consult the current cache, not a possibly stale closure from before a save.
      const latest =
        (memoryEntries.get(namespace) || []).find(
          (item) => item.key === entry.key,
        ) || entry;
      memoryEdit(latest, namespace);
      $("memory-value").focus();
    };
    list.append(button);
  }
  if (!list.childElementCount)
    list.append(el("p", "empty", "No entries in this shared namespace."));
  restoreViewState(list, state);
  renderMemoryMetadata();
}
async function loadMemory() {
  const namespace = $("memory-namespace").value;
  resetMemoryNamespace(namespace);
  if (!namespace || !selectionReady()) return;
  const request = ++memoryRequest, requestedAgent=selected;
  try {
    const entries = new Map();
    let after = "";
    while (true) {
      const result = await call("memory.list", { namespace, limit: 100, after });
      if (
        request !== memoryRequest ||
        requestedAgent !== selected ||
        namespace !== $("memory-namespace").value ||
        !selectionReady()
      )
        return;
      for (const entry of result.entries || []) entries.set(entry.key, entry);
      if (!result.entries?.length || !result.cursor || result.cursor === after)
        break;
      after = result.cursor;
    }
    memoryNamespace = namespace;
    memoryEntries.set(namespace, [...entries.values()]);
    renderMemoryEntries(namespace);
  } catch (error) {
    notice(error.message, true);
  }
}
function resetMemoryNamespace(namespace) {
  if (memoryEditorNamespace === namespace) return;
  stashMemoryDraft();
  memoryEditorNamespace = namespace;
  memoryRequest++;
  memoryNamespace = "";
  const draft = memoryDrafts.get(memoryLastDrafts.get(namespace));
  memoryEdit(draft || { key: "", value: "", version: 0 }, namespace);
  $("memory-list").replaceChildren();
}
async function reviewMemoryLatest() {
  if (!selectionReady() || mutating) return;
  const namespace = memoryEditorNamespace,
    key = $("memory-key").value,
    epoch = memoryEditorEpoch,
    requestedAgent=selected,
    request = ++memoryReviewRequest;
  if (!namespace || !key) return;
  stashMemoryDraft();
  $("memory-version").textContent = "Reviewing latest · draft kept";
  const current = () =>
    request === memoryReviewRequest &&
    requestedAgent === selected &&
    epoch === memoryEditorEpoch &&
    namespace === memoryEditorNamespace &&
    key === $("memory-key").value &&
    selectionReady();
  try {
    const head = await call("memory.head", { namespace, key });
    if (!current()) return;
    memoryMetadataSelection=requestedAgent;
    let value = "", latestEntry=null;
    if (head.state === "live") {
      const result = await call("memory.get", { namespace, key });
      if (result.entry.version !== head.version)
        throw new Error(
          "Memory changed during review. Review latest again; your draft is kept.",
        );
      value = result.entry.value;
      latestEntry={...result.entry};
    }
    if (!current()) return;
    const cached = (memoryEntries.get(namespace) || []).filter(
      (entry) => entry.key !== key,
    );
    if (head.state === "live")
      cached.push(latestEntry);
    memoryEntries.set(namespace, cached);
    renderMemoryEntries(namespace);
    memoryReview = {
      namespace,
      key,
      version: head.version,
      state: head.state,
      value,
      epoch,
    };
    renderMemoryReview();
    $("memory-review").focus();
  } catch (error) {
    if (current()) notice(error.message + " Draft kept.", true);
  }
}
$("skip-workspace").onclick = (event) => {
  event.preventDefault();
  // The URL fragment carries the initial owner capability. A skip action must
  // move focus without replacing that fragment with a navigation anchor.
  $("workspace").focus({ preventScroll: true });
};
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
  if (nativeHeadless(snapshot?.agents?.find((a) => a.id === selected))) {
    notice(
      "This headless worker has no native attach command. Use Tasks and lifecycle events to observe; research phase controls are in Research when available.",
      true,
    );
    return;
  }
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
  if (
    researchHeadless(snapshot?.agents?.find((agent) => agent.id === selected))
  ) {
    activateTab("research");
    return;
  }
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
  renderGlobalHeading();
  document
    .querySelectorAll("[data-tab]")
    .forEach((node) => {
      const active = node.dataset.tab === tab;
      node.classList.toggle("active", active);
      node.setAttribute("aria-pressed", String(active));
    });
  document
    .querySelectorAll(".view")
    .forEach((node) =>
      node.classList.toggle("active", node.id === "view-" + tab),
    );
  $("composer").classList.toggle("hidden", tab !== "activity");
  if (tab === "memory") loadMemory();
  if (tab === "tasks") loadTaskHistory();
  if (tab === "sessions") loadWorkspaces();
  if (tab === "agent") loadCapacity();
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
    mode = $("message-mode").value || "auto",
    board = destination.startsWith("board:") ? destination.slice(6) : "";
  if (
    pendingDraftKind ||
    (destination !== "direct" &&
      (!board ||
        !(snapshot.boundaries.boards || []).some(
          (scope) => scope.name === board && scope.write,
        )))
  ) {
    notice(
      "Original destination unavailable for writing. Draft kept; explicitly choose another destination before sending.",
      true,
    );
    return;
  }
  const referencedBoard = (snapshot.activity.messages || []).find(item=>item.id===reference)?.board;
  if (!board && reference && !referencedBoard && replyRecipient(reference) !== recipient) {
    notice(
      "The original reply recipient is unavailable or changed. Refresh and review the reply before sending.",
      true,
    );
    return;
  }
  if (!board && referencedBoard && !(snapshot.boundaries.boards || []).some(scope=>scope.name===referencedBoard && scope.read)) { notice("This recipient cannot read the referenced board. Keep the reply on its original board or select a permitted reader.",true); return; }
  const researchInstruction =
    // A board-to-direct reply is an explicit audience choice; the broker also
    // verifies that the recipient can read the referenced parent.
    !board &&
    researchHeadless(snapshot.agents.find((agent) => agent.id === recipient));
  const headlessInstruction =
    !board &&
    nativeHeadless(snapshot.agents.find((agent) => agent.id === recipient));
  let confirmation = "Posted to #" + board + ".";
  action(
    async () => {
      if (board) await api("/api/send", { board, text, mode:"note", reply_to:reference });
      else {
        const result = await api("/api/send", {
          target: recipient,
          text,
          reply_to: reference,
          mode,
        });
        confirmation =
          result.kind +
          " queued for " +
          sessionReference(result.target) +
          (result.kind === "Message"
            ? ". Native terminal input was not changed."
            : researchInstruction
              ? ". This extra instruction interrupts the research phase protocol."
              : headlessInstruction
                ? ". Queued only; this one-shot runner may not dispatch additional tasks."
                : ". The managed worker runs this instruction when ready.");
      }
      const draft = messageDrafts.get(recipient);
      if (
        draft?.text === text &&
        draft.reply === reference &&
        (draft.mode || "auto") === mode &&
        draft.kind === destination
      )
        messageDrafts.delete(recipient);
      if (
        selected === recipient &&
        $("message").value === text &&
        replyTo === reference &&
        ($("message-mode").value || "auto") === mode &&
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
  memoryEditorEpoch++;
  memoryReviewRequest++;
  memoryReview = null;
  renderMemoryReview();
  $("memory-version").textContent =
    "New key · version 0. Review latest to check an existing key.";
};
$("memory-value").oninput = () => {
  stashMemoryDraft();
  $("memory-version").textContent =
    "Unsaved changes · based on version " + memoryVersion;
  renderMemoryReview();
};
$("review-memory").onclick = reviewMemoryLatest;
$("close-memory-review").onclick = () => {
  memoryReview = null;
  renderMemoryReview();
  $("review-memory").focus({ preventScroll: true });
};
$("adopt-memory-version").onclick = () => {
  const review = memoryReview;
  if (
    !review ||
    !selectionReady() ||
    mutating ||
    review.epoch !== memoryEditorEpoch ||
    review.namespace !== memoryEditorNamespace ||
    review.key !== $("memory-key").value
  )
    return;
  memoryVersion = review.version;
  memoryBaseline = {
    namespace: review.namespace,
    key: review.key,
    value: review.value,
  };
  memoryReview = null;
  stashMemoryDraft();
  renderMemoryReview();
  $("memory-version").textContent =
    "Reviewed version " +
    memoryVersion +
    " · draft kept, not saved. Save entry to submit.";
  $("memory-value").focus({ preventScroll: true });
};
$("discard-memory-draft").onclick = () => {
  const namespace = memoryEditorNamespace,
    key = $("memory-key").value;
  memoryDrafts.delete(memoryDraftKey(namespace, key));
  memoryLastDrafts.delete(namespace);
  const entry = (memoryEntries.get(namespace) || []).find(
    (item) => item.key === key,
  );
  memoryEdit(entry || { key: "", value: "", version: 0 }, namespace);
  renderMemoryEntries(namespace);
};
$("memory-form").onsubmit = (event) => {
  event.preventDefault();
  if (!selectionReady()) return;
  const namespace = $("memory-namespace").value,
    key = $("memory-key").value,
    value = $("memory-value").value,
    version = memoryVersion,
    epoch = memoryEditorEpoch,
    identity = memoryDraftKey(namespace, key);
  stashMemoryDraft();
  action(async () => {
    let result;
    try {
      result = await call("memory.put", { namespace, key, value, version });
    } catch (error) {
      if (namespace === memoryEditorNamespace && key === $("memory-key").value)
        $("memory-version").textContent =
          "Save failed · draft kept. Review latest for " +
          namespace +
          " / " +
          key +
          " before retrying.";
      throw error;
    }
    memoryRequest++;
    const saved = { ...result.entry, key, value, version: result.entry.version },
      entries = (memoryEntries.get(namespace) || []).filter(
        (entry) => entry.key !== key,
      );
    entries.push(saved);
    memoryEntries.set(namespace, entries);
    const draft = memoryDrafts.get(identity);
    if (draft && draft.version === version) {
      if (draft.value === value) memoryDrafts.delete(identity);
      else
        memoryDrafts.set(identity, {
          ...draft,
          version: saved.version,
          baseline: { namespace, key, value },
        });
    }
    if (
      epoch === memoryEditorEpoch &&
      namespace === memoryEditorNamespace &&
      key === $("memory-key").value &&
      version === memoryVersion
    ) {
      memoryVersion = saved.version;
      memoryBaseline = { namespace, key, value };
      memoryReview = null;
      stashMemoryDraft();
      $("memory-version").textContent =
        $("memory-value").value === value
          ? "Saved version " + memoryVersion
          : "Unsaved changes · based on version " + memoryVersion;
      renderMemoryReview();
    }
    if (namespace === memoryEditorNamespace) renderMemoryEntries(namespace);
  }, "Shared memory saved with version checking.");
};
refresh();
setInterval(() => {
  if (!document.hidden) refresh();
}, 1500);
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) refresh();
});
window.addEventListener("beforeunload", (event) => {
  if (!hasUnsavedWork()) return;
  event.preventDefault();
  event.returnValue = "";
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
  const research = researchHeadless(a),
    headless = nativeHeadless(a);
  const scopes = snapshot.boundaries.boards || [],
    boards = scopes
      .filter((scope) => scope.read || scope.write)
      .map((scope) => scope.name),
    desired = pendingDraftKind || prior || "direct",
    values = ["direct", ...boards.map((board) => "board:" + board)];
  // A retained draft must retain its destination even after its board access
  // disappears. The owner can explicitly choose a new destination instead.
  if (!values.includes(desired)) values.push(desired);
  if (
    [...select.options].map((option) => option.value).join("\n") !==
    values.join("\n")
  ) {
    select.replaceChildren(
      ...values.map((value) => {
        const option = el("option", "", "");
        option.value = value;
        return option;
      }),
    );
  }
  select.value = desired;
  pendingDraftKind = "";
  for (const option of select.options) {
    if (option.value === "direct") continue;
    const name = option.value.startsWith("board:") ? option.value.slice(6) : "",
      scope = scopes.find(
        (scope) => scope.name === name && (scope.read || scope.write),
      );
    option.disabled = !scope?.write;
    option.textContent =
      "#" +
      scopeLabel(name || option.value) +
      (!scope
        ? " · unavailable — choose another destination"
        : !scope.write
          ? " · read only"
          : "");
  }
  select.options[0].textContent =
    a?.config.interaction === "interactive"
      ? "Direct inbox"
      : research
        ? "Extra instruction · interrupts run"
        : "Instruction task";
  const board = select.value.startsWith("board:") ? select.value.slice(6) : "";
  const modeSelect = $("message-mode"), mode = modeSelect.value || "auto";
  modeSelect.disabled = !!board || !selectionReady();
  const instruction = !board && (mode === "instruction" || (mode === "auto" && a?.config.interaction !== "interactive"));
  $("send-target").textContent = board
    ? "#" + scopeLabel(board)
    : sessionReference(selected);
  const available =
    select.value === "direct" ||
    (board && scopes.some((scope) => scope.name === board && scope.write));
  $("send-message").disabled = !available || a?.state !== "active";
  $("delivery-hint").textContent = !available
    ? "Original destination unavailable for writing. Draft kept; explicitly choose another destination before sending."
    : board
      ? research
        ? "Research steering note. Agents see permitted board context at their next read; this does not add an extra instruction task."
        : "Shared board. Only sessions allowed to read this namespace receive its context."
      : !instruction
        ? "Inbox message. Direct conversation and native approvals remain in the agent terminal."
        : a?.config.interaction === "interactive"
          ? "Queues an explicit instruction task for this native session; it is not typed into the terminal. Native sessions must read and act on it explicitly."
        : research
          ? "Extra owner instructions interrupt this research attempt's phase protocol. Use a shared board or memory for steering, or review phase controls in Research."
          : headless
            ? "Queues an instruction; this one-shot runner may not dispatch additional tasks. Inspect Tasks and lifecycle events for delivery."
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
$("message-mode").onchange = renderComposer;
$("add-session").onsubmit = (event) => {
  event.preventDefault();
  const name = $("new-name").value,
    title = $("new-title").value,
    harness = $("new-harness").value,
    project = $("new-project").value,
    nativeOnly = $("new-native-only").checked,
    role = nativeOnly ? "" : $("new-role").value,
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
      native_only:nativeOnly,
    });
    if (selected === initialSelection) selectAgent(result.id);
    if ($("new-name").value === name) $("new-name").value = "";
    if ($("new-title").value === title) $("new-title").value = "";
  }, "Session added. Collaboration stays off until you allow it.");
};
function updateNewNativeProfile() {
  const antigravity = $("new-harness").value === "antigravity";
  if (antigravity) $("new-native-only").checked = true;
  $("new-native-only").disabled = antigravity;
  $("new-role").disabled = $("new-native-only").checked;
  $("new-native-hint").textContent = antigravity
    ? "Antigravity opens its local native interface without injected instructions. Use manual bridge setup, or the Pi bridge for coordinated managed turns."
    : $("new-native-only").checked
      ? "Pure native launch: no Agentmux prompt is passed. Copy bridge setup later if you want manual coordination."
      : "Coordinated native launch preserves your tools, skills and MCPs and adds Agentmux role/inbox context.";
}
$("new-native-only").onchange = updateNewNativeProfile;
$("new-harness").onchange = updateNewNativeProfile;
function renderRoleEditor() {
  const agent = snapshot?.agents?.find(a=>a.id===selected);
  if (!agent) return;
  const draft=roleDrafts.get(agent.id), version=agent.role_version || 0, conflict=!!draft && draft.version!==version;
  if (roleEditingID!==agent.id || !draft) $("role-draft").value=draft?.role ?? agent.config.role ?? "";
  roleEditingID=agent.id;
  $("role-current").textContent="Saved role v"+version+" for "+sessionReference(agent.id)+": "+(agent.config.role || "No role");
  $("role-version").textContent=agent.config.native_only ? "Pure native session: injected roles are disabled." : conflict ? "Saved role changed. Draft retained; review the saved role above before adopting its version." : "Draft based on saved role v"+(draft?.version ?? version)+". Applies to future managed turns; native sessions receive an inbox notice.";
  $("role-draft").disabled=!selectionReady() || !!agent.config.native_only || agent.state!=="active";
  $("save-role").disabled=$("role-draft").disabled || conflict;
  $("rebase-role").hidden=!conflict;
  $("rebase-role").disabled=!selectionReady();
}
$("role-draft").oninput=()=>{
  const agent=snapshot?.agents?.find(a=>a.id===roleEditingID);
  if (!agent) return;
  const old=roleDrafts.get(agent.id);
  roleDrafts.set(agent.id,{role:$("role-draft").value,version:old?.version ?? agent.role_version ?? 0,baseline:old?.baseline ?? agent.config.role ?? ""});
};
$("discard-role").onclick=()=>{roleDrafts.delete(selected);renderRoleEditor();};
$("rebase-role").onclick=()=>{
  const agent=snapshot?.agents?.find(a=>a.id===selected),draft=roleDrafts.get(selected);
  if (!agent || !draft || !selectionReady()) return;
  roleDrafts.set(selected,{...draft,version:agent.role_version || 0,baseline:agent.config.role || ""});renderRoleEditor();
};
$("role-form").onsubmit=event=>{
  event.preventDefault();
  const agent=snapshot?.agents?.find(a=>a.id===selected),draft=roleDrafts.get(selected);
  if (!agent || !selectionReady() || agent.config.native_only || (draft && draft.version!==(agent.role_version || 0))) return;
  const id=agent.id,role=$("role-draft").value,version=draft?.version ?? agent.role_version ?? 0;
  action(async()=>{
    const result=await call("agent.role",{id,role,version}), latest=roleDrafts.get(id);
    if (latest?.role===role) roleDrafts.delete(id);
    else if (latest?.version===version) roleDrafts.set(id,{...latest,version:result.agent.role_version,baseline:role});
  },"Role updated for "+sessionReference(id)+". Native input is unchanged; the next managed turn captures the role and native sessions receive an inbox notice.");
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
  let confirmation = "";
  action(
    async () => {
      const result = await call("agent.stop", { id });
      confirmation =
        "Revocation requested for " +
        id +
        (result.revoked_agents > 1
          ? " and " + (result.revoked_agents - 1) + " descendants"
          : "") +
        ". Process termination is not yet acknowledged; external terminals remain in their original window.";
    },
    () => confirmation,
  );
};
$("connect-pair").onclick = () => {
  const from = selected,
    to = peer,
    version = snapshot.boundaries.version;
  const legacy=(snapshot.groups || []).find(group=>!(group.contributions_version>=1) && group.agents?.length===2 && group.agents.includes(from) && group.agents.includes(to));
  if(legacy) {activateTab("teams");notice("This pair has legacy direct overrides. Review the exact legacy controls and choose a policy in Teams before reconnecting; nothing was changed.",true);return;}
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
  if (group) return group.agents.map(sessionReference).join(" + ");
  const team = (snapshot?.teams || []).find(
    (t) => t.board === name || t.memory === name,
  );
  return team ? team.name + " (" + team.id + ")" : name;
}

$("session-search").oninput = () => {
  sessionQuery = $("session-search").value.trim();
  renderSessionLists(true);
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
  titleEditor.baseline = current.title || "";
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
        else
          titleDrafts.set(id, {
            ...draft,
            version: nextVersion,
            baseline: title,
          });
      }
      if (titleEditorEpoch === epoch && titleEditor?.id === id) {
        if ($("task-title").value === title) closeTitleEditor(false);
        else {
          titleEditor.version = nextVersion;
          titleEditor.baseline = title;
        }
      }
    },
    title
      ? "Task title saved for " + id + "."
      : "Task title cleared for " + id + ".",
  );
};

function teamJoiningHint(agent) {
  return agent?.parent && !agent.capabilities?.join_teams
    ? "joining not delegated"
    : "";
}
function teamOperationReady(review) {
  if (!selectionReady() || snapshot.teams_error || !review) return false;
  const team = (snapshot.teams || []).find((item) => item.id === review.id);
  if (!team || team.state !== "active" || team.version !== review.version)
    return false;
  if (review.operation === "team.dissolve") return true;
  if (review.operation === "team.update") return !!review.name?.trim();
  if (review.operation === "team.accept-owner") return team.pending_owner === "$owner";
  if (review.operation === "team.transfer") return review.agent === "$owner" || (team.members || []).includes(review.agent) && snapshot.agents.some(agent => agent.id === review.agent && agent.state === "active");
  if (review.operation === "team.remove")
    return (
      review.agent !== team.owner &&
      [...(team.members || []), ...(team.invited || [])].includes(review.agent)
    );
  return (
    review.operation === "team.invite" &&
    snapshot.agents.some(
      (agent) => agent.id === review.agent && agent.state === "active",
    ) &&
    ![...(team.members || []), ...(team.invited || [])].includes(review.agent)
  );
}
function reviewTeamChange(operation, team, agent = "", changes = {}) {
  const review = { operation, id: team.id, version: team.version, agent, ...changes };
  if (!teamOperationReady(review)) {
    notice(
      "Team or recipient changed. Refresh and review the current membership.",
      true,
    );
    return;
  }
  teamReview = review;
  teamsSignature = "";
  renderTeams();
}
async function applyTeamReview() {
  const review = teamReview;
  if (!teamOperationReady(review)) {
    notice(
      "Team or recipient changed. Cancel this review and choose the current action again.",
      true,
    );
    return;
  }
  await action(
    async () => {
      const args = { id: review.id, version: review.version };
      if (review.agent) args.agent = review.agent;
      if (review.operation === "team.update") { args.name = review.name; args.description = review.description || ""; }
      const result = await call(review.operation, args);
      if (review.operation === "team.update") {
        const draft = teamEditDrafts.get(review.id);
        if (draft?.version === review.version) {
          const next={...draft,version:result?.team?.version ?? review.version+1,baselineName:review.name,baselineDescription:review.description || ""};
          if (next.name===next.baselineName && next.description===next.baselineDescription && !next.transfer) teamEditDrafts.delete(review.id);
          else teamEditDrafts.set(review.id,next);
        }
      }
      if (review.operation === "team.transfer") {
        const draft=teamEditDrafts.get(review.id);
        if (draft?.version===review.version) {
          const next={...draft,version:result?.team?.version ?? review.version+1,transfer:draft.transfer===review.agent ? "" : draft.transfer};
          if (next.name===next.baselineName && next.description===next.baselineDescription && !next.transfer) teamEditDrafts.delete(review.id);
          else teamEditDrafts.set(review.id,next);
        }
      }
      if (teamReview === review) teamReview = null;
      teamsSignature = "";
    },
    review.operation === "team.invite"
      ? "Invitation recorded. The agent must accept before becoming a member."
      : review.operation === "team.transfer"
        ? "Ownership proposed. The recipient must accept under its own credential before authority changes."
        : "Team updated. Native processes and previously read context remain.",
  );
}
function renderTeams() {
  if (!snapshot) return;
  const teams = snapshot.teams || [],
    groups = snapshot.groups || [],
    ready = selectionReady() && !snapshot.teams_error;
  const signature = JSON.stringify([
    selected,
    selectionReady(),
    teams,
    groups,
    teamReview,
    legacyGroupReview,
    snapshot.legacy_group_edges,
    snapshot.teams_error,
    snapshot.agents.map((a) => [a.id, a.title, a.state]),
  ]);
  if (signature === teamsSignature) return;
  teamsSignature = signature;
  $("teams-count").textContent =
    teams.filter((t) => t.state === "active").length + " ACTIVE";
  const teamList = $("consented-teams"),
    groupList = $("owner-groups");
  const teamState = captureViewState(teamList),
    groupState = captureViewState(groupList);
  teamList.replaceChildren();
  groupList.replaceChildren();
  const reference = (id, key) => {
    const button = el("button", "quiet team-agent", sessionReference(id));
    button.type = "button";
    button.dataset.focusKey = key;
    button.onclick = () => selectAgent(id);
    if (id === "$owner") {
      button.textContent = "Owner ($owner)";
      button.disabled = true;
    }
    return button;
  };
  const scopes = (item) =>
    el(
      "p",
      "team-scopes",
      "Board: " + item.board + " · Memory: " + item.memory,
    );
  if (snapshot.teams_error)
    teamList.append(
      el(
        "p",
        "action-error",
        "Team state unavailable: " + snapshot.teams_error,
      ),
    );
  for (const team of teams) {
    const card = el("article", "team-card"),
      heading = el("div", "team-heading");
    card.dataset.itemKey = team.id;
    heading.append(
      el("h3", "", team.name),
      el("span", "", team.state + " · v" + team.version),
    );
    const copyID = el("button","quiet copy-id","Copy ID");
    copyID.type="button"; copyID.dataset.focusKey="copy:"+team.id; copyID.onclick=()=>copyReviewedText(team.id,"Team ID");
    heading.append(copyID);
    card.append(heading, el("code", "team-id", team.id));
    if (team.description) card.append(el("p","session-note",team.description));
    if (team.pending_owner) card.append(el("p","session-note","Ownership offered to " + sessionReference(team.pending_owner) + ". Current owner keeps authority until acceptance."));
    if (team.pending_owner === "$owner" && team.state === "active") {
      const accept=el("button","quiet","Review accepting ownership"); accept.type="button"; accept.dataset.ownerWrite="true"; accept.disabled=!ready;
      accept.onclick=()=>reviewTeamChange("team.accept-owner",team); card.append(accept);
    }
    const owner = el("div", "team-participants");
    owner.append(
      el("span", "", "Created by"),
      reference(team.owner, "owner:" + team.id),
    );
    card.append(owner);
    for (const [label, ids, pending] of [
      ["Members", team.members || [], false],
      ["Pending consent", team.invited || [], true],
    ]) {
      const row = el("div", "team-participants");
      row.append(el("span", "", label));
      if (!ids.length) row.append(el("small", "", "None"));
      for (const id of ids) {
        const participant = el("span", "team-participant");
        participant.append(reference(id, JSON.stringify([team.id, label, id])));
        const joining = teamJoiningHint(
          snapshot.agents.find((agent) => agent.id === id),
        );
        if (pending && joining) participant.append(el("small", "", joining));
        if (!pending) {
          const access = team.member_access?.[id];
          let status = "Membership recorded · effective access unavailable";
          if (access?.allowed === true)
            status = "Current board + memory access allowed";
          else if (access?.allowed === false)
            status =
              "Membership retained · effective access blocked" +
              (access.reason ? ": " + access.reason : "") +
              (access.blocked_by
                ? " · Blocked by " + sessionReference(access.blocked_by)
                : "");
          participant.append(
            el("small", access?.allowed === false ? "action-error" : "", status),
          );
        }
        if (team.state === "active" && id !== team.owner) {
          const remove = el(
            "button",
            "quiet",
            pending ? "Revoke invite" : "Remove",
          );
          remove.type = "button";
          remove.dataset.ownerWrite = "true";
          remove.dataset.focusKey = JSON.stringify([team.id, "remove", id]);
          remove.disabled = !ready;
          remove.onclick = () => reviewTeamChange("team.remove", team, id);
          participant.append(remove);
        }
        row.append(participant);
      }
      card.append(row);
    }
    card.append(scopes(team));
    if (team.state === "active") {
      const editor = el("details","team-editor"), summary=el("summary","","Edit name, purpose & ownership");
      editor.dataset.disclosureKey="edit:"+team.id; summary.dataset.focusKey="edit:"+team.id;
      const editForm = el("form","team-actions"), name=el("input"), description=el("input"), editSubmit=el("button","","Review changes"), transfer=el("select"), transferButton=el("button","quiet","Propose owner");
      const editDraft = teamEditDrafts.get(team.id);
      const editConflict=!!editDraft && editDraft.version!==team.version;
      name.value=editDraft?.name ?? team.name; name.maxLength=120; name.setAttribute("aria-label","Team name for "+team.id); name.dataset.focusKey="name:"+team.id;
      description.value=editDraft?.description ?? team.description ?? ""; description.maxLength=512; description.placeholder="Team purpose"; description.setAttribute("aria-label","Team purpose for "+team.id); description.dataset.focusKey="description:"+team.id;
      const stashEdit=()=>{
        const old=teamEditDrafts.get(team.id);
        teamEditDrafts.set(team.id,{name:name.value,description:description.value,transfer:transfer.value,version:old?.version ?? team.version,baselineName:old?.baselineName ?? team.name,baselineDescription:old?.baselineDescription ?? team.description ?? ""});
      };
      name.oninput=description.oninput=stashEdit;
      for (const id of ["", "$owner", ...(team.members || []).filter(id=>id!==team.owner)]) { const option=el("option","",id ? sessionReference(id) : "Choose next owner…"); option.value=id; transfer.append(option); }
      transfer.value=editDraft?.transfer || ""; transfer.setAttribute("aria-label","Proposed owner for "+team.id); transfer.dataset.focusKey="transfer:"+team.id; transfer.onchange=stashEdit;
      editSubmit.type="submit"; transferButton.type="button";
      for (const control of [name,description,editSubmit,transfer,transferButton]) { control.disabled=!ready; control.dataset.ownerWrite="true"; }
      editSubmit.disabled=transferButton.disabled=!ready || editConflict;
      const draftReady=()=>{const draft=teamEditDrafts.get(team.id); if (draft && draft.version!==team.version) { notice("Team changed after this draft began. Review current metadata and explicitly adopt its version before saving.",true); return false; } return true;};
      editForm.onsubmit=event=>{event.preventDefault();if(draftReady()) reviewTeamChange("team.update",team,"",{name:name.value,description:description.value});};
      transferButton.onclick=()=>{ if(transfer.value && draftReady()) reviewTeamChange("team.transfer",team,transfer.value); };
      const discard=el("button","quiet","Discard team draft"); discard.type="button"; discard.dataset.focusKey="discard-edit:"+team.id;
      discard.onclick=()=>{teamEditDrafts.delete(team.id);if(teamReview?.id===team.id) teamReview=null;teamsSignature="";renderTeams();};
      editForm.append(name,description,editSubmit,transfer,transferButton,discard); editor.append(summary,el("p","session-note","Current saved v"+team.version+": "+team.name+" · "+(team.description || "No purpose")),editForm);
      if (editConflict) {
        const rebase=el("button","quiet","Use reviewed team version"); rebase.type="button"; rebase.dataset.ownerWrite="true";rebase.disabled=!ready;rebase.dataset.focusKey="rebase-edit:"+team.id;
        rebase.onclick=()=>{const draft=teamEditDrafts.get(team.id);if(!draft || !selectionReady()) return;teamEditDrafts.set(team.id,{...draft,version:team.version,baselineName:team.name,baselineDescription:team.description || ""});if(teamReview?.id===team.id) teamReview=null;teamsSignature="";renderTeams();};
        editor.append(el("p","action-error","Team changed while this draft was open. Draft retained; saving and ownership proposals require explicit review of the saved version above."),rebase);
      }
      card.append(editor);
      const form = el("form", "team-actions"),
        invite = el("select"),
        submit = el("button", "", "Review invite");
      invite.setAttribute(
        "aria-label",
        "Invite an agent to " + team.name + " (" + team.id + ")",
      );
      invite.dataset.focusKey = "invite:" + team.id;
      submit.dataset.focusKey = "invite-review:" + team.id;
      const candidates = snapshot.agents.filter(
        (agent) =>
          agent.state === "active" &&
          ![...(team.members || []), ...(team.invited || [])].includes(
            agent.id,
          ),
      );
      const placeholder = el("option", "", "Choose an active agent…");
      placeholder.value = "";
      invite.append(placeholder);
      for (const agent of candidates) {
        const hint = teamJoiningHint(agent);
        const option = el(
          "option",
          "",
          sessionReference(agent.id) + (hint ? " · " + hint : ""),
        );
        option.value = agent.id;
        invite.append(option);
      }
      const draft = teamInviteDrafts.get(team.id);
      if (candidates.some((agent) => agent.id === draft)) invite.value = draft;
      invite.disabled = !ready;
      invite.dataset.ownerWrite = "true";
      submit.type = "submit";
      submit.dataset.ownerWrite = "true";
      submit.disabled = !ready || !invite.value;
      invite.onchange = () => {
        teamInviteDrafts.set(team.id, invite.value);
        submit.disabled =
          !selectionReady() || !!snapshot.teams_error || !invite.value;
      };
      form.onsubmit = (event) => {
        event.preventDefault();
        reviewTeamChange("team.invite", team, invite.value);
      };
      const dissolve = el("button", "quiet", "Dissolve…");
      dissolve.type = "button";
      dissolve.dataset.ownerWrite = "true";
      dissolve.dataset.focusKey = "dissolve:" + team.id;
      dissolve.disabled = !ready;
      dissolve.onclick = () => reviewTeamChange("team.dissolve", team);
      form.append(invite, submit, dissolve);
      card.append(form);
    }
    if (teamReview?.id === team.id) {
      const review = teamReview,
        panel = el("div", "team-review"),
        valid = teamOperationReady(review);
      const intent =
        review.operation === "team.invite"
          ? "Invite " + sessionReference(review.agent)
          : review.operation === "team.remove"
            ? "Remove " +
              sessionReference(review.agent) +
              " from membership or pending invitations"
            : review.operation === "team.update"
              ? "Update name to “" + review.name + "” and purpose to “" + (review.description || "") + "”"
              : review.operation === "team.accept-owner" ? "Accept ownership as the human owner"
              : review.operation === "team.transfer"
                ? "Offer ownership to " + sessionReference(review.agent) + "; the recipient must consent"
                : "Dissolve this team and end its future shared access";
      panel.append(
        el("strong", "", intent + "?"),
        el(
          "p",
          "",
          "Team " +
            review.id +
            " · reviewed version " +
            review.version +
            (valid
              ? ". Existing knowledge and native processes remain."
              : ". State changed; cancel and review the current membership."),
        ),
      );
      const confirm = el("button", "", "Confirm"),
        cancel = el("button", "quiet", "Cancel");
      confirm.type = cancel.type = "button";
      confirm.dataset.ownerWrite = "true";
      confirm.dataset.focusKey = "confirm:" + team.id;
      cancel.dataset.focusKey = "cancel:" + team.id;
      confirm.disabled = !valid;
      confirm.onclick = applyTeamReview;
      cancel.onclick = () => {
        teamReview = null;
        teamsSignature = "";
        renderTeams();
      };
      panel.append(confirm, cancel);
      card.append(panel);
    }
    teamList.append(card);
  }
  if (!teams.length && !snapshot.teams_error)
    teamList.append(
      el(
        "p",
        "empty",
        "No consented teams yet. Configured agents can create and invite within their delegation limits.",
      ),
    );
  for (const group of groups) {
    const card = el("article", "team-card"),
      heading = el("div", "team-heading");
    card.dataset.itemKey = group.id;
    heading.append(
      el("h3", "", (group.agents || []).map(id => sessionTitle(snapshot.agents.find(a=>a.id===id)) + " ("+id+")").join(" + ") || "Owner collaboration"),
      el("span", "", group.state + " · v" + group.version),
    );
    card.append(heading, el("code", "team-id", group.id));
    const members = el("div", "team-participants");
    members.append(el("span", "", "Current members"));
    for (const id of group.members || [])
      members.append(reference(id, JSON.stringify([group.id, id])));
    if (!group.members?.length) members.append(el("small", "", "None"));
    card.append(members, scopes(group));
    const actions = el("div","team-actions"), reconnect = el("button","quiet",group.state === "active" && group.members?.length === group.agents?.length ? "Disable group" : "Reconnect original members"), commands=el("button","quiet",group.commands ? "Disable group instructions" : "Allow mutual instructions");
    reconnect.type=commands.type="button";
    for (const control of [reconnect,commands]) { control.dataset.ownerWrite="true"; control.disabled=!ready; }
    const groupBoundaryVersion=snapshot.boundaries.version;
    const fullyConnected=group.state==="active" && group.members?.length===group.agents?.length;
    commands.disabled=!ready || !fullyConnected;
    commands.title=fullyConnected ? "Changes mutual instruction rights in this fully connected group." : "Reconnect original members explicitly before changing group instructions.";
    const legacy=!(group.contributions_version >= 1), legacyEdges=snapshot.legacy_group_edges?.[group.id];
    let legacyChoice=null;
    if (legacy) {
      const panel=el("section","legacy-group-policy"), label=el("label","","Review legacy direct permissions"), choice=el("select");
      choice.setAttribute("aria-label","Legacy direct permission policy for "+group.id);choice.dataset.focusKey="legacy-policy:"+group.id;
      for(const [value,text] of [["","Choose a legacy policy…"],["preserve","Preserve all existing direct overrides"],["group","Move old direct allows to this group"]]) {const option=el("option","",text);option.value=value;option.disabled=!value;choice.append(option);}
      choice.value=legacyGroupChoices.get(group.id) || "";legacyChoice=choice;
      label.append(choice);panel.append(label,el("p","session-note","This group predates independent contribution tracking. Existing untagged direct overrides may be historical group grants or deliberate owner intent; Agentmux cannot infer which. Preserve keeps those direct overrides even after disabling this group. Move removes matching ALLOW overrides so the group governs them. Existing DENY overrides remain and require a separate explicit reset in Graph."));
      const edges=el("ul","legacy-edge-list");
      if(Array.isArray(legacyEdges)) {
        for(const edge of legacyEdges) edges.append(el("li","",edge.from+" → "+edge.to+" · "+edge.action+" · "+(edge.allow?"ALLOW (ambiguous origin)":"DENY (retained)")));
        if(!legacyEdges.length) edges.append(el("li","","No matching untagged direct overrides."));
      } else edges.append(el("li","action-error","Current legacy direct controls are unavailable. Refresh before reviewing migration."));
      panel.append(edges);card.append(panel);
      const updateAvailability=()=>{const chosen=["preserve","group"].includes(choice.value);reconnect.disabled=!ready || !chosen || !Array.isArray(legacyEdges);commands.disabled=!ready || !fullyConnected || !chosen || !Array.isArray(legacyEdges);};
      choice.onchange=()=>{legacyGroupChoices.set(group.id,choice.value);if(legacyGroupReview?.id===group.id) legacyGroupReview=null;teamsSignature="";renderTeams();};
      updateAvailability();
      reconnect.textContent="Review legacy policy & "+(fullyConnected?"disable group":"reconnect original members");
      commands.textContent="Review legacy policy & "+(group.commands?"disable instructions":"allow mutual instructions");
    }
    const updateGroup = (enabled, allowCommands) => legacy
      ? reviewLegacyGroupChange(group,enabled,allowCommands,legacyChoice.value,groupBoundaryVersion)
      : action(()=>call("control.update",{version:groupBoundaryVersion,kind:"collaboration",agents:group.agents,enabled,commands:allowCommands}),"Updated only this group's contribution. Other groups and explicit permission overrides remain independent.");
    reconnect.onclick=()=>updateGroup(!(group.state==="active"&&group.members?.length===group.agents?.length),group.commands);
    commands.onclick=()=>updateGroup(true,!group.commands);
    actions.append(reconnect,commands); card.append(actions);
    if(legacyGroupReview?.id===group.id) {
      const review=legacyGroupReview,panel=el("section","team-review"),confirm=el("button","","Apply reviewed legacy policy"),cancel=el("button","quiet","Cancel review"),valid=legacyGroupReviewReady(review);
      panel.append(el("strong","","Legacy policy: "+(review.policy==="preserve"?"preserve all existing direct overrides":"move matching old ALLOW overrides to group contributions")),el("p","","Group "+review.id+" · group v"+review.groupVersion+" · permission v"+review.version+". Requested state: "+(review.enabled?"connected":"disabled")+"; mutual instructions "+(review.commands?"allowed":"disabled")+". Existing DENY controls stay unchanged."),el("p",valid?"session-note":"action-error",valid?"Confirm only after reviewing the exact direct controls above.":"Group, membership or permissions changed. Cancel and review the current state; this review never adopts a newer version."));
      confirm.type=cancel.type="button";confirm.dataset.ownerWrite="true";confirm.disabled=!valid;confirm.dataset.focusKey="legacy-confirm:"+group.id;
      confirm.onclick=applyLegacyGroupReview;cancel.onclick=()=>{legacyGroupReview=null;teamsSignature="";renderTeams();};panel.append(confirm,cancel);card.append(panel);
    }
    groupList.append(card);
  }
  if (!groups.length)
    groupList.append(
      el(
        "p",
        "empty",
        "No owner collaboration groups. Select agents in Graph to allow collaboration.",
      ),
    );
  restoreViewState(teamList, teamState);
  restoreViewState(groupList, groupState);
}

function legacyGroupFingerprint(group) { return JSON.stringify([group.version,group.agents,group.members,group.state,group.commands,group.contributions_version || 0]); }
function legacyGroupReviewReady(review) {
  if(!review || !selectionReady() || snapshot.teams_error || snapshot.boundaries.version!==review.version) return false;
  const group=(snapshot.groups || []).find(item=>item.id===review.id),edges=snapshot.legacy_group_edges?.[review.id];
  return !!group && !(group.contributions_version>=1) && legacyGroupFingerprint(group)===review.fingerprint && Array.isArray(edges) && JSON.stringify(edges)===review.edgeFingerprint;
}
function reviewLegacyGroupChange(group,enabled,commands,policy,version) {
  if(!["preserve","group"].includes(policy)) {notice("Choose how to treat legacy direct overrides before reviewing this change.",true);return;}
  const edges=snapshot.legacy_group_edges?.[group.id];
  if(!Array.isArray(edges)) {notice("Refresh current legacy direct controls before reviewing migration.",true);return;}
  const review={id:group.id,groupVersion:group.version,version,agents:[...group.agents],enabled,commands,policy,fingerprint:legacyGroupFingerprint(group),edgeFingerprint:JSON.stringify(edges)};
  if(!legacyGroupReviewReady(review)) {notice("Legacy group changed. Refresh and review current permissions.",true);return;}
  legacyGroupReview=review;teamsSignature="";renderTeams();
}
async function applyLegacyGroupReview() {
  const review=legacyGroupReview;
  if(!legacyGroupReviewReady(review)) {notice("Legacy policy review is stale. Cancel and review current membership and permissions.",true);return;}
  await action(async()=>{await call("control.update",{version:review.version,kind:"collaboration",agents:review.agents,enabled:review.enabled,commands:review.commands,legacy_policy:review.policy});if(legacyGroupReview===review){legacyGroupReview=null;legacyGroupChoices.delete(review.id);}teamsSignature="";},"Legacy policy migrated with your explicit choice. Existing DENY controls remain; review effective permissions in Graph.");
}

function renderWorkerRuntime(agent) {
  const panel = $("worker-runtime");
  panel.replaceChildren();
  if (snapshot.runtime_error)
    panel.append(
      el(
        "p",
        "action-error",
        "Runtime status unavailable: " + snapshot.runtime_error,
      ),
    );
  if (agent.config.interaction === "interactive") {
    panel.append(
      el("strong", "", "Human-led native session"),
      el(
        "p",
        "",
        "Continue in its terminal. Broker observations do not establish native prompt state or whether the agent read its inbox.",
      ),
    );
    return;
  }
  const status = snapshot.autonomy;
  if (status?.agent === agent.id) {
    panel.append(
      el("strong", "", "Managed worker · " + status.state),
      el(
        "p",
        "",
        (status.reason || "Ready for eligible work") +
          " · " +
          status.inflight +
          " active or uncertain deliveries · " +
          (status.pending_observations ?? status.pending) +
          " queued observations · " +
          (status.pending_instructions ?? 0) +
          " queued instructions",
      ),
    );
    if (status.enabled)
      panel.append(
        el(
          "p",
          "",
          "Autonomy " +
            status.mode +
            " · " +
            status.turns_used +
            "/" +
            status.max_turns +
            " turns · " +
            status.suppressed +
            " suppressed · " +
            status.dropped +
            " dropped",
        ),
      );
    else
      panel.append(
        el(
          "p",
          "",
          nativeHeadless(agent) && !researchHeadless(agent)
            ? "Autonomy disabled. This one-shot runner may not dispatch additional owner tasks."
            : "Autonomy disabled. Owner instruction tasks can still run.",
        ),
      );
    if (status.next_allowed_at)
      panel.append(
        el("p", "", sessionTime(status.next_allowed_at, "Next eligible")),
      );
  } else if (!snapshot.runtime_error)
    panel.append(el("p", "", "Managed worker status not currently available."));
  const history = snapshot.conversation;
  if (history?.agent === agent.id)
    panel.append(
      el(
        "p",
        "",
        "Persistent history · " +
          history.state +
          " · generation " +
          history.generation +
          (history.task_id ? " · task " + history.task_id : ""),
      ),
    );
  panel.append(
    el(
      "small",
      "",
      "Pausing autonomy prevents future automatic turns. Cancel accepted tasks separately." +
        (nativeHeadless(agent)
          ? " This worker has no attached terminal."
          : " Terminal interaction remains in tmux."),
    ),
  );
}
function renderLifecycle() {
  const list = $("lifecycle-events");
  const signature = JSON.stringify([
    snapshot.activity.events || [],
    (snapshot.agents || []).map((agent) => [agent.id, agent.title]),
  ]);
  if (signature === lifecycleSignature) return;
  lifecycleSignature = signature;
  const state = captureViewState(list);
  list.replaceChildren();
  for (const event of snapshot.activity.events || []) {
    const row = el("article", "lifecycle-row"),
      heading = el("div", "lifecycle-heading");
    row.dataset.itemKey = event.id;
    heading.append(
      el("time", "", stamp(event.at)),
      el("strong", "", event.type),
    );
    row.append(
      heading,
      el(
        "p",
        "",
        sessionReference(event.actor) +
          (event.entity_id ? " → " + sessionReference(event.entity_id) : ""),
      ),
    );
    if (event.details && Object.keys(event.details).length) {
      const detail = el("details");
      detail.dataset.disclosureKey = event.id;
      const summary = el("summary", "", "Event details");
      summary.dataset.focusKey = event.id;
      detail.append(
        summary,
        el("pre", "", JSON.stringify(event.details, null, 2).slice(0, 4000)),
      );
      row.append(detail);
    }
    list.append(row);
  }
  if (!list.childElementCount)
    list.append(
      el(
        "p",
        "empty",
        "No recent lifecycle events. This is the bounded broker event page, not a full research archive.",
      ),
    );
  restoreViewState(list, state);
}

function runnerHeartbeat(at, now = Date.now()) {
  const parsed = Date.parse(at),
    age = now - parsed;
  if (!Number.isFinite(parsed) || age < -5000) return "unknown";
  return age > 10000 ? "stale" : "fresh";
}
function researchMutable(research) {
  return (
    !!research &&
    research.control.mode !== "stopped" &&
    !["completed", "interrupted", "stopped"].includes(research.progress.state)
  );
}
function researchReviewReady(review) {
  const research = snapshot?.research;
  return (
    !!review &&
    selectionReady() &&
    !snapshot.research_error &&
    researchMutable(research) &&
    research.control.version === review.version &&
    research.progress.trial === review.trial &&
    research.progress.attempt === review.attempt
  );
}
function renderResearchReview() {
  const panel = $("research-review");
  const state = captureViewState(panel);
  panel.hidden = !researchReview;
  panel.replaceChildren();
  if (!researchReview) {
    if (state.inside) $("review-research").focus();
    return;
  }
  const review = researchReview,
    valid = researchReviewReady(review);
  const heartbeat = runnerHeartbeat(snapshot?.research?.runner_heartbeat_at);
  panel.append(
    el(
      "strong",
      "",
      review.action + " · " + review.trial + " · attempt " + review.attempt,
    ),
    el("p", "", "Reason: " + review.reason),
    el(
      "p",
      "",
      "Reviewed control version " +
        review.version +
        ". Applies at the next phase barrier; accepted tasks are not interrupted.",
    ),
  );
  if (!valid)
    panel.append(
      el(
        "p",
        "action-error",
        "The attempt or control state changed, or the console is offline. Cancel and review current state before sending.",
      ),
    );
  if (heartbeat !== "fresh")
    panel.append(
      el(
        "p",
        "session-note",
        "Runner heartbeat " +
          heartbeat +
          ". A request may remain pending until the runner responds; accepted tasks may still be running.",
      ),
    );
  const confirm = el("button", "", "Confirm intervention"),
    cancel = el("button", "quiet", "Cancel");
  confirm.id = "confirm-research";
  confirm.type = cancel.type = "button";
  confirm.dataset.ownerWrite = "true";
  confirm.dataset.focusKey = "confirm";
  cancel.dataset.focusKey = "cancel";
  confirm.disabled = !valid;
  confirm.onclick = applyResearchReview;
  cancel.onclick = () => {
    researchReview = null;
    renderResearchReview();
  };
  panel.append(confirm, cancel);
  restoreViewState(panel, state);
}
async function applyResearchReview() {
  const review = researchReview;
  if (!researchReviewReady(review)) {
    notice(
      "Research control or attempt changed. Cancel this review and review current state.",
      true,
    );
    return;
  }
  await action(async () => {
    await call("research.control", {
      version: review.version,
      action: review.action,
      reason: review.reason,
    });
    if (researchReview === review) {
      researchReview = null;
      if (
        $("research-reason").value === review.reason &&
        $("research-action").value === review.action
      )
        $("research-reason").value = "";
    }
  }, "Research intervention recorded. Wait for the runner to acknowledge it at a phase barrier.");
}
function renderResearch() {
  const research = snapshot?.research,
    problem = snapshot?.research_error;
  $("research-tab").hidden = !research && !problem;
  const status = $("research-status"),
    population = $("research-population"),
    history = $("research-interventions");
  const populationState = captureViewState(population);
  status.replaceChildren();
  population.replaceChildren();
  history.replaceChildren();
  $("research-control").hidden = !research;
  if (!research) {
    $("research-state").textContent = problem ? "UNAVAILABLE" : "NO RUNNER";
    status.append(
      el(
        "p",
        problem ? "action-error" : "empty",
        problem
          ? "Research status unavailable: " + problem
          : "No research runner is registered with this workspace.",
      ),
    );
    restoreViewState(population, populationState);
    renderResearchReview();
    return;
  }
  const p = research.progress,
    c = research.control;
  $("research-state").textContent =
    (researchSavedOffline ? "SAVED · " : "") + p.state.toUpperCase();
  if (researchSavedOffline)
    status.append(
      el(
        "p",
        "session-note",
        "Broker offline · saved research state. This metadata does not establish runner or task liveness; controls remain disabled.",
      ),
    );
  const line = (label, value) => {
    const row = el("div", "research-stat");
    row.append(el("span", "", label), el("strong", "", value));
    status.append(row);
  };
  line("Trial / attempt", p.trial + " / " + p.attempt);
  line("Condition / seed", p.condition + " / " + p.seed);
  line("Round / phase", p.round + " / " + p.phase);
  line("Last reported runner state", p.state);
  const heartbeat = runnerHeartbeat(research.runner_heartbeat_at);
  line(
    "Runner heartbeat",
    heartbeat +
      (research.runner_heartbeat_at
        ? " · " + new Date(research.runner_heartbeat_at).toLocaleTimeString()
        : ""),
  );
  if (heartbeat !== "fresh")
    status.append(
      el(
        "p",
        "session-note",
        "Heartbeat " +
          heartbeat +
          ": runner liveness is not confirmed. Accepted tasks may still run; control requests may remain pending.",
      ),
    );
  line(
    "Control request",
    c.mode +
      " · v" +
      c.version +
      (p.ack_version < c.version
        ? " · awaiting phase-barrier acknowledgement"
        : " · acknowledged"),
  );
  line(
    "Finite deadline",
    p.deadline ? new Date(p.deadline).toLocaleString() : "Not reported",
  );
  line(
    "Reserved model tasks",
    p.model_tasks_reserved + " / " + p.model_tasks_limit,
  );
  line("Owner mutations", String(research.owner_mutations || 0));
  line(
    "Last runner update",
    p.updated_at ? new Date(p.updated_at).toLocaleString() : "Not reported",
  );
  status.append(
    el(
      "p",
      "session-note",
      "Reported lifecycle metadata. These counters do not establish model quality, cooperation, competition, or total provider spend.",
    ),
  );
  for (const id of p.population || []) {
    const row = el("div", "research-stat"),
      button = el("button", "quiet team-agent", sessionReference(id));
    button.type = "button";
    button.dataset.focusKey = id;
    row.dataset.itemKey = id;
    button.onclick = () => selectAgent(id);
    row.append(
      button,
      el("span", "", p.tasks?.[id] || "No phase task reported"),
    );
    population.append(row);
  }
  if (!population.childElementCount)
    population.append(
      el("p", "empty", "No population reported for this phase."),
    );
  for (const intervention of [...(c.interventions || [])].reverse()) {
    const row = el("article", "lifecycle-row");
    row.append(
      el("strong", "", intervention.action + " · v" + intervention.version),
      el("p", "", intervention.reason),
      el(
        "p",
        "",
        sessionReference(intervention.actor) +
          " · round " +
          intervention.round +
          " · " +
          intervention.phase +
          " · " +
          stamp(intervention.at),
      ),
    );
    history.append(row);
  }
  if (!history.childElementCount)
    history.append(
      el("p", "empty", "No research control interventions recorded."),
    );
  restoreViewState(population, populationState);
  renderResearchReview();
}
$("research-control").onsubmit = (event) => {
  event.preventDefault();
  const research = snapshot?.research,
    reason = $("research-reason").value,
    action = $("research-action").value;
  if (
    !selectionReady() ||
    !researchMutable(research) ||
    snapshot.research_error
  )
    return;
  if (
    !reason.trim() ||
    new TextEncoder().encode(reason).length > 512 ||
    /[\u0000-\u001f\u007f-\u009f]/u.test(reason)
  ) {
    notice("Enter a printable intervention reason of 1–512 bytes.", true);
    return;
  }
  researchReview = {
    version: research.control.version,
    trial: research.progress.trial,
    attempt: research.progress.attempt,
    action,
    reason,
  };
  renderResearchReview();
};
for (const id of ["research-action", "research-reason"])
  $(id).addEventListener("input", () => {
    researchReview = null;
    renderResearchReview();
  });

async function copyReviewedText(text, label = "Command") {
  try { await navigator.clipboard.writeText(text); notice(label + " copied."); }
  catch { notice("Clipboard unavailable. Select and copy the displayed " + label.toLowerCase() + ".", true); }
}
async function loadWorkspaces() {
  if (workspaceLoading) return;
  workspaceLoading = true;
  $("refresh-workspaces").disabled = true;
  try {
    const result = await api("/api/workspaces");
    workspaceListings = result.workspaces || [];
    workspaceLoadedAt = Date.now();
    const list = $("workspaces"), view = captureViewState(list);
    list.replaceChildren();
    for (const workspace of workspaceListings) {
      const row = el("article","workspace-row"), details = el("div");
      row.dataset.itemKey = workspace.state;
      details.append(el("strong","",workspace.session || "Unnamed workspace"),el("small","",workspace.status + " · " + workspace.agents + " registered sessions" + (workspace.current ? " · current console" : "")),el("code","",workspace.state));
      if (workspace.problem) details.append(el("small","action-error",workspace.problem));
      const commands = el("details","workspace-commands"), summary = el("summary","","Open commands");
      commands.dataset.disclosureKey = workspace.state;
      summary.dataset.focusKey = "workspace:" + workspace.state;
      commands.append(summary);
      for (const [label,command] of [["Terminal",workspace.dashboard_command],["Web",workspace.web_command]]) {
        const copy = el("button","quiet","Copy " + label.toLowerCase() + " command");
        copy.type="button"; copy.dataset.focusKey = label + ":" + workspace.state;
        copy.onclick = () => copyReviewedText(command,label + " command");
        commands.append(el("code","",command),copy);
      }
      row.append(details,commands); list.append(row);
    }
    if (!workspaceListings.length) list.append(el("p","empty","No saved workspace inventory found. Run agentmux workspaces --register DIR for an older custom state directory."));
    restoreViewState(list,view);
  } catch (error) { notice("Workspace inventory: " + error.message,true); }
  finally { workspaceLoading = false; $("refresh-workspaces").disabled = false; }
}
$("refresh-workspaces").onclick = loadWorkspaces;
$("relaunch-agent").onclick = () => {
  if (!selectionReady()) return;
  const agent = snapshot.agents.find(a => a.id === selected), pane = snapshot.runtime.agents?.[selected];
  if (!agent || agent.external || agent.state !== "active" || agent.config.interaction !== "interactive" || !pane || pane.state === "running") return;
  relaunchReview = {target:agent.id,attempt:snapshot.runtime.attempt_id,pane:pane.pane_id};
  $("relaunch-target").textContent = "Relaunch " + sessionReference(agent.id) + " · " + agent.config.workdir;
  $("relaunch-resume").value = ""; $("relaunch-history-id").value = ""; $("relaunch-history-id").hidden = true;
  $("relaunch-review").hidden = false; $("relaunch-resume").focus();
};
$("relaunch-resume").onchange = () => { $("relaunch-history-id").hidden = $("relaunch-resume").value !== "id"; };
$("cancel-relaunch").onclick = () => { relaunchReview = null; $("relaunch-review").hidden = true; };
$("relaunch-review").onsubmit = event => {
  event.preventDefault();
  const review = relaunchReview, pane = snapshot?.runtime.agents?.[review?.target];
  if (!review || !selectionReady() || selected !== review.target || snapshot.runtime.attempt_id !== review.attempt || pane?.pane_id !== review.pane || pane?.state === "running") { notice("Session changed after review. Refresh and review its current identity before relaunching.",true); return; }
  const mode = $("relaunch-resume").value, resume = mode === "id" ? $("relaunch-history-id").value.trim() : mode;
  if (mode === "id" && !resume) { notice("Enter a specific native history ID or choose a fresh conversation.",true); return; }
  action(async () => { await api("/api/relaunch",{target:review.target,resume,attempt:review.attempt,pane:review.pane}); if (relaunchReview === review) { relaunchReview = null; $("relaunch-review").hidden = true; } },"Relaunched " + sessionReference(review.target) + ". Healthy peers keep running; continue in its native terminal.");
};
async function loadCapacity() {
  if (!selectionReady()) return;
  const id = selected, request = ++capacityRequest;
  try {
    const [capacity,owner] = await Promise.all([call("capacity.get",{agent:id}),call("capacity.get",{})]);
    if (request !== capacityRequest || selected !== id) return;
    capacityView = capacity;
    if (capacity.resource_version !== owner.resource_version) { notice("Resource limits changed during refresh. Refresh capacity before editing.",true); return; }
    resourceSnapshot = {agent:id,version:capacity.resource_version,session_budget:owner.session_budget || {},agent_budget:capacity.agent_budget || {}};
    renderResourceEditor();
    const panel = $("agent-capacity");
    panel.replaceChildren(el("p","", "Spawn slots " + capacity.spawn_remaining + " · depth remaining " + capacity.spawn_depth_remaining + " · team slots " + capacity.teams_remaining + " · per-team limit " + capacity.team_member_limit));
    panel.append(el("p","", "Can spawn: " + (capacity.can_spawn ? "yes" : "no") + " · create teams: " + (capacity.can_create_teams ? "yes" : "no") + " · join teams: " + (capacity.can_join_teams ? "yes" : "no")));
    if (capacity.spawn_templates?.length) panel.append(el("p","","Templates: " + capacity.spawn_templates.join(", ")));
    if (capacity.runtime_deadline) panel.append(el("p","","Admission deadline " + new Date(capacity.runtime_deadline).toLocaleString() + " · " + capacity.runtime_remaining_seconds + " seconds remaining. Running tasks require cancellation separately."));
    if (capacity.admission_blocked) panel.append(el("p","action-error","Admission blocked: " + capacity.admission_reason));
    for (const reason of capacity.blocked_reasons || []) panel.append(el("p","session-note",reason));
    const details = el("details",""), summary = el("summary","","Capacity details · resource v" + capacity.resource_version);
    details.append(summary,el("pre","",JSON.stringify(capacity,null,2)));
    panel.append(details);
  } catch (error) { if (request === capacityRequest) $("agent-capacity").replaceChildren(el("p","action-error",error.message)); }
}
$("load-capacity").onclick = loadCapacity;
const resourceFields = {"resource-session-tokens":"session_tokens","resource-session-cost":"session_cost","resource-turn-tokens":"turn_tokens","resource-turn-cost":"turn_cost","resource-subtree-tokens":"subtree_tokens","resource-subtree-cost":"subtree_cost","resource-admission":"admission"};
function resourceValues() { return Object.fromEntries(Object.entries(resourceFields).map(([id,key])=>[key,$(id).value])); }
function renderResourceEditor() {
  if (!resourceSnapshot || resourceSnapshot.agent!==selected) { $("review-resources").disabled=true; return; }
  const draft=resourceDrafts.get(selected), limits=resourceSnapshot;
  const values=draft?.values || {session_tokens:limits.session_budget.max_tokens || 0,session_cost:limits.session_budget.max_cost_microusd || 0,turn_tokens:limits.agent_budget.max_turn_tokens || 0,turn_cost:limits.agent_budget.max_turn_cost_microusd || 0,subtree_tokens:limits.agent_budget.max_subtree_tokens || 0,subtree_cost:limits.agent_budget.max_subtree_cost_microusd || 0,admission:""};
  for (const [id,key] of Object.entries(resourceFields)) $(id).value=String(values[key]);
  const conflict=!!draft && draft.version!==limits.version;
  $("resource-current").textContent="Current resource v"+limits.version+" · "+sessionReference(selected)+(conflict ? ". Limits changed; draft retained. Discard and review current limits before editing." : ". Changes apply to future admission only.");
  $("review-resources").disabled=!selectionReady() || conflict;
  if (resourceReview && (resourceReview.agent!==selected || resourceReview.version!==limits.version)) { $("resource-review").hidden=true; resourceReview=null; }
}
for (const id of Object.keys(resourceFields)) $(id).oninput=()=>{
  if (!resourceSnapshot || resourceSnapshot.agent!==selected) return;
  const prior=resourceDrafts.get(selected);
  resourceDrafts.set(selected,{version:prior?.version ?? resourceSnapshot.version,values:resourceValues()});
  resourceReview=null; $("resource-review").hidden=true;
};
$("discard-resources").onclick=()=>{resourceDrafts.delete(selected);resourceReview=null;$("resource-review").hidden=true;renderResourceEditor();};
$("resource-form").onsubmit=event=>{
  event.preventDefault();
  if (!selectionReady() || !resourceSnapshot || resourceSnapshot.agent!==selected) return;
  const draft=resourceDrafts.get(selected);
  if (draft && draft.version!==resourceSnapshot.version) { notice("Resource limits changed. Discard the stale draft and review current limits.",true); return; }
  const values=resourceValues(), numeric={};
  for (const [key,value] of Object.entries(values)) {
    if (key==="admission" && value==="") continue;
    const n=Number(value); if (value==="" || !Number.isSafeInteger(n) || n<0) { notice("Budget values must be nonnegative safe integers.",true); return; } numeric[key]=n;
  }
  const review={agent:selected,version:resourceSnapshot.version,session_budget:{max_tokens:numeric.session_tokens,max_cost_microusd:numeric.session_cost},agent_budget:{max_turn_tokens:numeric.turn_tokens,max_turn_cost_microusd:numeric.turn_cost,max_subtree_tokens:numeric.subtree_tokens,max_subtree_cost_microusd:numeric.subtree_cost,enforcement:"reservation"}};
  if (numeric.admission!==undefined) review.admission_seconds=numeric.admission;
  resourceReview=review;
  const panel=$("resource-review"),confirm=el("button","","Apply reviewed budgets"),cancel=el("button","quiet","Cancel");
  panel.replaceChildren(el("strong","","Review exact admission limits for "+sessionReference(review.agent)),el("pre","",JSON.stringify(review,null,2)),confirm,cancel); panel.hidden=false;
  confirm.type=cancel.type="button"; confirm.dataset.ownerWrite="true";
  confirm.onclick=()=>{
    if (resourceReview!==review || !selectionReady() || selected!==review.agent || resourceSnapshot?.version!==review.version) { notice("Resource review is stale. Refresh and review current limits.",true); return; }
    action(async()=>{await call("capacity.update",review);if(resourceReview===review){resourceReview=null;panel.hidden=true;resourceDrafts.delete(review.agent);}await loadCapacity();},"Admission limits updated. Existing provider calls and permanently reserved usage remain unchanged.");
  };
  cancel.onclick=()=>{resourceReview=null;panel.hidden=true;};
};

function hasUnsavedWork() {
  if (mutating || teamReview || researchReview || relaunchReview || resourceReview || legacyGroupReview) return true;
  if (resourceDrafts.size) return true;
  if ([...roleDrafts.values()].some(draft=>draft.role!==draft.baseline)) return true;
  if ([...teamEditDrafts.values()].some(draft=>draft.name!==draft.baselineName || draft.description!==draft.baselineDescription || draft.transfer)) return true;
  if ($("message").value.trim() || replyTo) return true;
  for (const [id, draft] of messageDrafts)
    if (id !== selected && (draft.text?.trim() || draft.reply)) return true;
  const titleChanged = (id, title, baseline) =>
    title !==
    (baseline ??
      snapshot?.agents?.find((agent) => agent.id === id)?.title ??
      "");
  if (
    titleEditor &&
    titleChanged(titleEditor.id, $("task-title").value, titleEditor.baseline)
  )
    return true;
  for (const [id, draft] of titleDrafts)
    if (id !== titleEditor?.id && titleChanged(id, draft.title, draft.baseline))
      return true;
  if (memoryDirty()) return true;
  const currentMemory = memoryDraftKey(
    memoryEditorNamespace,
    $("memory-key").value,
  );
  if ([...memoryDrafts.keys()].some((key) => key !== currentMemory))
    return true;
  if ($("research-reason").value.trim()) return true;
  if ([...observedDrafts.values()].some((draft) => draft.title || draft.name))
    return true;
  if (["new-name", "new-title", "new-role"].some((id) => $(id).value.trim()))
    return true;
  return [...document.querySelectorAll(".team-actions select")].some(
    (node) => node.value,
  );
}
function renderGlobalHeading() {
  const global = tab === "research" || tab === "teams";
  document.body.classList.toggle("global-view", global);
  const research = snapshot?.research,
    agent = snapshot?.agents?.find((item) => item.id === selected);
  $("global-view-title").textContent =
    tab === "research"
      ? "Research" +
        (research?.progress.condition
          ? " · " + research.progress.condition
          : "")
      : "Teams";
  $("global-view-context").textContent = agent
    ? "Selected: " + sessionTitle(agent) + " · " + agent.id
    : researchSavedOffline
      ? "Broker offline · saved research metadata"
      : "No session selected";
  $("global-view-context").title = $("global-view-context").textContent;
}
function renderMetadataOnly() {
  $("agent-name").textContent = "Saved research attempt";
  $("agent-role").textContent =
    "Broker offline · only durable lifecycle metadata is available.";
  for (const id of [
    "agent-id",
    "agent-workdir",
    "agent-boundary",
    "agent-origin",
    "agent-started",
    "agent-terminal",
    "agent-lineage",
    "agent-harness",
  ])
    $(id).textContent = "";
  $("workbench-shortcuts").textContent =
    "Saved research · lifecycle metadata only";
  $("native-observation-title").textContent = "Saved research metadata";
  $("native-observation-label").textContent = "BROKER OFFLINE · READ ONLY";
  $("terminal").textContent =
    "Only saved research lifecycle metadata is available. Agent terminals, task records and permissions have not been loaded.";
  $("pause-agent").textContent = "Offline";
  $("copy-attach").textContent = "Saved metadata";
  $("task-count").textContent = "—";
  $("agent-count").textContent = "—";
  $("registered-count").textContent = "NOT LOADED";
  $("teams-count").textContent = "NOT LOADED";
  $("memory-form").hidden = true;
  for (const [id, text] of [
    ["feed", "Messages are unavailable in this saved metadata view."],
    ["tasks", "Task records are unavailable in this saved metadata view."],
    ["memory-list", "Agent memory is unavailable while the broker is offline."],
    [
      "consented-teams",
      "Team membership is unavailable in this saved metadata view.",
    ],
    [
      "owner-groups",
      "Collaboration groups are unavailable in this saved metadata view.",
    ],
    [
      "lifecycle-events",
      "Broker events are unavailable here; saved research interventions remain in Research.",
    ],
    ["registered-sessions", "Agent sessions have not been loaded."],
    ["observed-sessions", "Native terminals have not been observed."],
  ])
    $(id).replaceChildren(el("p", "empty", text));
}

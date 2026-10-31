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
  teamsSignature = "";
let teamReview = null;
let researchReview = null;
let researchSavedOffline = false;
const teamInviteDrafts = new Map();
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
    renderSessionLists();
    renderObserved();
  };
  box.append(document.createElement("br"), clear);
  list.append(box);
}
function renderSessionLists() {
  if (!snapshot) return;
  const all = snapshot.agents || [],
    list = $("agents");
  if (researchSavedOffline && !all.length) {
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
      (id === "pause-agent" &&
        !agent?.config.autonomy?.enabled &&
        !researchHeadless(agent)) ||
      (id === "focus-terminal" &&
        (!!agent?.external || nativeHeadless(agent))) ||
      (id === "copy-attach" && nativeHeadless(agent)) ||
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
    if (t.origin || t.trace) {
      const details = el("details", "reported-usage"),
        summary = el("summary", "", "Origin & adapter trace");
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
          scope.source === "team" ||
          (!!agents.find((a) => a.id === source)?.parent &&
            !agents.find((a) => a.id === source)?.owner_managed) ||
          agents.find((a) => a.id === source)?.state !== "active";
        if (control.disabled)
          control.title =
            scope.source === "team"
              ? "Consented team membership controls this scope. Manage invitations and membership in Teams."
              : "Spawned agents use their inherited delegation limits.";
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
  const researchInstruction =
    !board &&
    researchHeadless(snapshot.agents.find((agent) => agent.id === recipient));
  const headlessInstruction =
    !board &&
    nativeHeadless(snapshot.agents.find((agent) => agent.id === recipient));
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
  const research = researchHeadless(a),
    headless = nativeHeadless(a);
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
              : research
                ? "Extra instruction · interrupts run"
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
  select.options[0].textContent =
    a?.config.interaction === "interactive"
      ? "Direct inbox"
      : research
        ? "Extra instruction · interrupts run"
        : "Instruction task";
  const board = select.value.startsWith("board:") ? select.value.slice(6) : "";
  $("send-target").textContent = board
    ? "#" + scopeLabel(board)
    : sessionReference(selected);
  $("delivery-hint").textContent = board
    ? research
      ? "Research steering note. Agents see permitted board context at their next read; this does not add an extra instruction task."
      : "Shared board. Only sessions allowed to read this namespace receive its context."
    : a?.config.interaction === "interactive"
      ? "Inbox message. Direct conversation and native approvals remain in the agent terminal."
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
  if (group) return group.agents.map(sessionReference).join(" + ");
  const team = (snapshot?.teams || []).find(
    (t) => t.board === name || t.memory === name,
  );
  return team ? team.name + " (" + team.id + ")" : name;
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
function reviewTeamChange(operation, team, agent = "") {
  const review = { operation, id: team.id, version: team.version, agent };
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
      await call(review.operation, args);
      if (teamReview === review) teamReview = null;
      teamsSignature = "";
    },
    review.operation === "team.invite"
      ? "Invitation recorded. The agent must accept before becoming a member."
      : "Team membership updated. Native processes and previously read context remain.",
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
    snapshot.teams_error,
    snapshot.agents.map((a) => [a.id, a.title, a.state]),
  ]);
  if (signature === teamsSignature) return;
  teamsSignature = signature;
  $("teams-count").textContent =
    teams.filter((t) => t.state === "active").length + " ACTIVE";
  const teamList = $("consented-teams"),
    groupList = $("owner-groups");
  teamList.replaceChildren();
  groupList.replaceChildren();
  const reference = (id) => {
    const button = el("button", "quiet team-agent", sessionReference(id));
    button.type = "button";
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
    heading.append(
      el("h3", "", team.name),
      el("span", "", team.state + " · v" + team.version),
    );
    card.append(heading, el("code", "team-id", team.id));
    const owner = el("div", "team-participants");
    owner.append(el("span", "", "Created by"), reference(team.owner));
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
        participant.append(reference(id));
        const joining = teamJoiningHint(
          snapshot.agents.find((agent) => agent.id === id),
        );
        if (pending && joining) participant.append(el("small", "", joining));
        if (team.state === "active" && id !== team.owner) {
          const remove = el(
            "button",
            "quiet",
            pending ? "Revoke invite" : "Remove",
          );
          remove.type = "button";
          remove.dataset.ownerWrite = "true";
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
      const form = el("form", "team-actions"),
        invite = el("select"),
        submit = el("button", "", "Review invite");
      invite.setAttribute(
        "aria-label",
        "Invite an agent to " + team.name + " (" + team.id + ")",
      );
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
    heading.append(
      el("h3", "", "Owner collaboration"),
      el("span", "", group.state + " · v" + group.version),
    );
    card.append(heading, el("code", "team-id", group.id));
    const members = el("div", "team-participants");
    members.append(el("span", "", "Current members"));
    for (const id of group.members || []) members.append(reference(id));
    if (!group.members?.length) members.append(el("small", "", "None"));
    card.append(members, scopes(group));
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
          " active or uncertain deliveries",
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
            status.pending +
            " pending observations · " +
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
  list.replaceChildren();
  for (const event of snapshot.activity.events || []) {
    const row = el("article", "lifecycle-row"),
      heading = el("div", "lifecycle-heading");
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
      detail.append(
        el("summary", "", "Event details"),
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
  panel.hidden = !researchReview;
  panel.replaceChildren();
  if (!researchReview) return;
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
  confirm.disabled = !valid;
  confirm.onclick = applyResearchReview;
  cancel.onclick = () => {
    researchReview = null;
    renderResearchReview();
  };
  panel.append(confirm, cancel);
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

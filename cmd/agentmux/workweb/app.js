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
let newSessionDirty = false;
let followStudy = false, studyView = null, studyLoading = false;
let routingEpoch=0, routingPending=false, snapshotRoute=null;
let taskSummary={agents:[],loading:false,error:"",epoch:0,updatedAt:0};
const attemptDrafts = new Map();
let exposureHistory={exposures:[],cursor:"",hasMore:false,loading:false,epoch:0,reader:"",kind:"",route:"",namespace:"",key:"",version:0,error:"",retainedLimit:10000};
let researchHistory = {records:[],cursor:"",hasMore:false,loading:false,epoch:0,query:"",actor:"",type:"",error:""};
let ownerActionReview = null, actionMenuOrigin = null, includeArchived = false, archivedAgents = [];
let messageHistory = {messages:null,cursor:"",hasMore:false,query:"",board:"",loading:false,epoch:0,error:"",scope:"all",agent:""};
let eventHistory = {events:null,cursor:"",hasMore:false,query:"",agent:"",type:"",loading:false,epoch:0,error:""};
let taskAttention = "";
const submissionRecords = new Map();
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
let pendingRailReveal = "";
let sessionQuery = "",
  titleEditor = null,
  titleEditorEpoch = 0;
let pendingDraftKind = "";
let taskHistory = { tasks: null, cursor: "", hasMore: false, query: "", to: "", state: "", loading: false, epoch: 0, updatedAt: 0, error: "" };
let workspaceListings = [], workspaceLoading = false, workspaceLoadedAt = 0;
let relaunchReview = null, capacityView = null, capacityRequest = 0, capacityLoading = "";
let resourceSnapshot = null, resourceReview = null;
const resourceDrafts = new Map();
const nativeDrafts = new Map();
let nativeSignature = "";
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
    ...(item.workspace_origins || []),
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
function revealSelectedSession() {
  if(pendingRailReveal!==selected)return;
  const agent=(snapshot?.agents||[]).find(item=>item.id===selected);
  if(!agent)return;
  if(sessionQuery && !matchesSession(agent)){
    sessionQuery="";$("session-search").value="";agentSignature="";
    $("session-search-status").textContent="Search cleared to show selected session "+sessionReference(selected)+".";
    renderSessionLists();return;
  }
  const row=[...$("agents").children].find(node=>node.dataset.agent===selected);
  if(!row)return;
  row.scrollIntoView?.({block:"nearest",inline:"nearest"});pendingRailReveal="";
}
function renderSessionLists(announceSearch = false) {
  if (!snapshot) return;
  const all = includeArchived ? [...new Map([...(snapshot.agents||[]),...archivedAgents].map(agent=>[agent.id,agent])).values()] : snapshot.agents || [],
    list = $("agents");
  const explicitTarget=pendingRailReveal===selected?all.find(agent=>agent.id===selected):null;
  if(explicitTarget && sessionQuery && !matchesSession(explicitTarget)){
    sessionQuery="";$("session-search").value="";agentSignature="";
    $("session-search-status").textContent="Search cleared to show selected session "+sessionReference(selected)+".";
  }
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
    taskSummary.agents,
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
  if (signature === agentSignature) {revealSelectedSession();return;}
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
    row.tabIndex=-1;row.disabled=item.state==="retired";
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
    const counts=taskSummary.agents.find(summary=>summary.agent===item.id);
    if(counts)copy.append(el("small","agent-task-counts",counts.total+" tasks · "+counts.failed+" failed · "+counts.uncertain+" uncertain"));
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
    if(item.state==="retired") {open.disabled=true;details.append(el("small","",sessionTime(item.retired_at,"Retired")));}
    entry.append(details, open);
    if(item.state==="revoked") {
      const retire=el("button","quiet","Retire from active registry");retire.type="button";retire.dataset.ownerWrite="true";retire.dataset.focusKey="retire:"+item.id;retire.disabled=!selectionReady();
      retire.onclick=()=>reviewOwnerAction({kind:"retire",target:selected,ids:[item.id],title:"Retire "+sessionReference(item.id)+"?",description:"Hide this revoked session from the active registry. Task, message, memory and event records remain available by stable ID. Retirement is rejected until its worker has stopped.",operation:"agent.retire",args:{agent:item.id},origin:"include-archived"});entry.append(retire);
    }
    $("registered-sessions").append(entry);
  }
  restoreViewState(list, rosterState);
  restoreViewState($("registered-sessions"), registeredState);
  revealSelectedSession();
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
function snapshotIdentity(value=snapshot) {return value?.study_attempt || value?.runtime?.attempt_id || "";}
async function api(path, body, attempt, route) {
  const requestEpoch=routingEpoch, requestFollow=route?.follow ?? followStudy;
  if(body!==undefined && routingPending){const error=new Error("Workspace routing is loading. Wait for its matching snapshot before sending or changing state.");error.code="conflict";throw error;}
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
  if(requestFollow)options.headers["X-Agentmux-Follow-Study"]="1";
  if(body!==undefined)options.headers["X-Agentmux-Attempt"]=attempt ?? snapshotIdentity();
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
    if(requestEpoch!==routingEpoch){const error=new Error("Discarded a response from the previous workspace route.");error.code="stale_routing";throw error;}
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
const call = async (operation, args = {}, requestID = "", attempt, route) =>
  (await api("/api/call", { operation, args, ...(requestID ? {request_id:requestID} : {}) },attempt,route)).result;
const selectionReady = () =>
  !!snapshot &&
  !routingPending &&
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
    "disconnect-pair",
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
        "disconnect-pair",
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
      (["connect-pair","disconnect-pair"].includes(id) &&
        (!editable(agent) || !editable(recipient) || peer === selected)) ||
      (id === "isolate-agent" && (!editable(agent) || !!snapshot?.teams_error || !Array.isArray(snapshot?.teams))) ||
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
  $("isolate-agent").title = snapshot?.teams_error || !Array.isArray(snapshot?.teams) ? "Refresh the unavailable team inventory before reviewing global isolation." : editable(agent)
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
  renderResourceAvailability();
}
function selectAgent(id) {
  pendingRailReveal=id;
  const target=snapshot?.agents?.find(agent=>agent.id===id);
  if(target && sessionQuery && !matchesSession(target)){
    sessionQuery="";$("session-search").value="";agentSignature="";
    $("session-search-status").textContent="Search cleared to show selected session "+sessionReference(id)+".";
  }
  if (selected === id) {renderSessionLists();revealSelectedSession();return;}
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
  resourceReview=null; $("resource-review").hidden=true;
  renderResourceAvailability();
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
  renderSessionLists();revealSelectedSession();
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
  const requested = selected, requestEpoch=routingEpoch, requestFollow=followStudy;
  let responseEpoch=requestEpoch;
  try {
    const next = await api(
      "/api/snapshot?agent=" + encodeURIComponent(requested),
    );
    if (requested !== selected || requestEpoch!==routingEpoch) return;
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
    if(snapshot && snapshotIdentity(snapshot)!==snapshotIdentity(next)){routingEpoch++;responseEpoch=routingEpoch;switchAttemptDraftScope(snapshotIdentity(snapshot),snapshotIdentity(next),next.selected);}
    snapshot = next;
    snapshotRoute={follow:requestFollow,epoch:responseEpoch};routingPending=false;
    researchSavedOffline = false;
    connected = true;
    selected = next.selected;
    $("error-overlay").classList.add("hidden");
    $("connection").textContent = "Connected";
    $("connection").classList.remove("offline");
    render();
    if (next.research) loadStudyStatus();
    if(Date.now()-taskSummary.updatedAt>10000)loadTaskSummary();
    renderSubmissionRecords();
    if (tab === "activity") loadMessageHistory("refresh");
    if (tab === "teams" && Date.now()-(eventHistory.updatedAt||0)>10000)loadEventHistory("refresh");
    if (tab === "research" && Date.now()-(researchHistory.updatedAt||0)>10000)loadResearchHistory("refresh");
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
    if(responseEpoch!==routingEpoch)return;
    if(followStudy)loadStudyStatus();
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
    if (requested !== selected || responseEpoch!==routingEpoch) queueMicrotask(refresh);
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
  renderOwnerActionReview();
  renderAttentionQueue();
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
  renderNativeDelivery();
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
  $("bridge-origin").textContent=observed?.workspace_origins?.length?"Observed workspace origin: "+observed.workspace_origins.join(", ")+". This is provenance only; it grants no collaboration access here.":"Workspace origin unknown. Verify the original native terminal before pasting setup.";
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
        "No shared memory enabled for this session. Connect a collaboration group or grant a namespace in Connections.",
      ),
    );
  if (tab === "memory" && memoryNamespace !== ns.value) loadMemory();
  selectionControls();
}
function renderFeed() {
  const feed = $("feed"),
    messages = (messageHistory.messages || snapshot.activity.messages || []).filter((m) =>
      boardFilter === "team"
        ? !!m.board
        : boardFilter === "agent"
          ? !m.board && (m.from === selected || (m.to || []).includes(selected))
          : true,
    );
  const boardSelect=$("message-board"),boardValue=boardSelect.value,boardOptions=[["","All shared boards"],...(snapshot.boundaries.boards||[]).filter(scope=>scope.read).map(scope=>[scope.name,scopeLabel(scope.name)])];
  boardSelect.replaceChildren(...boardOptions.map(([value,label])=>{const option=el("option","",label);option.value=value;return option;}));boardSelect.value=boardValue;
  $("message-history-status").textContent=messageHistory.loading?"Loading message history…":messageHistory.error?"History could not refresh: "+messageHistory.error+". Previous results retained.":messageHistory.messages===null?"Recent snapshot shown. Open Board to load searchable history.":messages.length+" matching message(s) loaded · "+(messageHistory.hasMore?"older messages available":"end of history");
  $("load-more-messages").hidden=!messageHistory.hasMore;$("load-more-messages").disabled=messageHistory.loading||!connected;
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
  $("feed-count").textContent = messages.length + (messageHistory.messages===null?" RECENT":" LOADED");
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
function taskRecovery(task) {
  if(task?.recovery && ["uncertain","cancel_uncertain"].includes(task.recovery.delivery) && Array.isArray(task.recovery.actions))return {delivery:task.recovery.delivery,reason:task.recovery.reason||"Broker reports this delivery requires owner recovery.",actions:task.recovery.actions.filter(action=>["fail","retry"].includes(action))};
  if(task?.delivery==="uncertain"&&task.state==="running")return {delivery:"uncertain",reason:"Delivery outcome is uncertain.",actions:["fail","retry"]};
  if(task?.delivery==="cancel_uncertain")return {delivery:"cancel_uncertain",reason:"Cancellation outcome is uncertain.",actions:["fail"]};
  return {delivery:"",reason:"",actions:[]};
}
function renderTasks() {
  const loadedTasks = taskHistory.tasks || snapshot.activity.tasks || [],
    tasks = taskAttention ? loadedTasks.filter(task=>taskAttentionKind(task)===taskAttention) : loadedTasks,
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
    tasks, taskAttention, snapshot.native_statuses,
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
    item.append(title,el("p","task-timestamps",sessionTime(t.created_at,"Created")+" · "+ageText(t.created_at)+" · "+sessionTime(t.updated_at,"Updated")), el("p", "", t.text));
    const attention=taskAttentionKind(t);if(attention)item.append(el("p","action-error",({review:"Review required",blocked:"Delivery blocked",uncertain:"Delivery uncertain",failed:"Failed"})[attention]));
    if (t.native) item.append(el("p", "session-note", "Native delivery: " + t.native.phase + (t.native.reason ? " · " + t.native.reason : "") + (t.native.adapter === "claude" ? ". Completion is a receiver acknowledgment." : "") + " Transport receipt is separate from task verification."));
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
    const inspect=el("button","quiet","Inspect receiver delivery");inspect.type="button";inspect.dataset.focusKey="inspect-receiver:"+t.id;inspect.onclick=()=>inspectReceiver(t.to);buttons.append(inspect);
    const recovery=taskRecovery(t);
    if (recovery.actions.length) {
      item.append(el("p","action-error","Owner recovery available: "+recovery.reason+" Recorded delivery remains "+t.delivery+" until recovery is applied."));
      for(const resolution of recovery.actions) {const label=resolution==="retry"?"Review safe retry":"Resolve as failed / stopped";
        const resolve=el("button","quiet",label);resolve.type="button";resolve.dataset.ownerWrite="true";resolve.dataset.focusKey="resolve:"+t.id+":"+resolution;resolve.disabled=!selectionReady();
        resolve.onclick=()=>reviewOwnerAction({kind:"resolve",target:selected,ids:[t.id,t.to],version:snapshot.boundaries.version,title:resolution==="retry"?"Retry this uncertain task?":"Resolve this uncertain task?",description:"Task "+t.id+" to "+sessionReference(t.to)+" · version "+t.version+" · recorded delivery "+t.delivery+". "+recovery.reason+" "+(resolution==="retry"?"First stop or fence the old native turn/process and inspect its result. Retrying may repeat external side effects if the previous attempt ran. This explicitly queues the existing task for another attempt; it does not create a new task.":"Record this attempt as failed or stopped after reviewing the native terminal. This cannot retract a turn already submitted or prove that no side effects occurred."),operation:"task.resolve",args:{id:t.id,version:t.version,resolution},origin:"task-attention-filter"});buttons.append(resolve);
      }
    }
    if (["proposed", "running"].includes(t.state)) {
      const cancel = el("button", "quiet", "Cancel task");
      cancel.dataset.ownerWrite = "true";
      cancel.dataset.focusKey = "cancel:" + t.id;
      cancel.disabled = !selectionReady();
      cancel.onclick = () =>
        action(
          async () => { await call("task.cancel", { id: t.id, version: t.version }); taskHistory.updatedAt = 0; },
          t.native ? "Cancellation requested. A native turn already submitted may continue; inspect its receipt and interrupt in the native terminal if needed." : "Cancellation requested. Check the process-stop acknowledgement.",
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
    snapshot.native_statuses?.[peer],
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
    ["ping.send", "Pings", "Wake only when the receiver subscribes and enables delivery"],
    ["instruct", "Commands", "Permission to request work; receiver controls delivery"],
  ]) {
    const row = el("div", "control-row"),
      name = el("div", "control-name");
    name.append(el("strong", "", label), el("small", "", description));
    const reason = targetBoundary?.sources?.[key] || targetBoundary?.denied_reasons?.[key];
    if (reason) name.append(el("small", "permission-source", reason));
    name.append(el("small", "native-edge-status", receiverDeliveryExplanation(recipient,key)));
    const inspect=el("button","quiet","Inspect receiver delivery");inspect.type="button";inspect.dataset.focusKey="receiver:"+recipient+":"+key;inspect.onclick=()=>inspectReceiver(recipient);name.append(inspect);
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
    message = [...(messageHistory.messages || []),...(snapshot?.activity.messages || [])].find(
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
  const message = [...(messageHistory.messages || []),...(snapshot?.activity.messages || [])].find(item => item.id === id);
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
  const received=el("button","quiet","Who received this version?");received.type="button";received.dataset.focusKey="memory-exposure:"+namespace+":"+key+":"+entry.version;
  received.onclick=()=>{Object.assign(exposureHistory,{reader:"",kind:"memory",route:"",namespace,key,version:entry.version});$("exposure-reader").value="";$("exposure-kind").value="memory";$("exposure-route").value="";$("exposure-ledger").open=true;loadExposures("reset");$("exposure-list").focus();};panel.append(received);
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
    : { namespace, key: entry.key, value: entry.value, state:entry.version>0?"live":"missing" };
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
  if (tab === "activity") loadMessageHistory("refresh");
  if (tab === "teams") loadEventHistory("refresh");
  if (tab === "research") {loadStudyStatus();loadResearchHistory("refresh");}
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
  messageHistory.messages=null;
  loadMessageHistory("reset");
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
  const referencedBoard = ([...messageHistory.messages || [],...snapshot.activity.messages || []]).find(item=>item.id===reference)?.board;
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
      const instruction = !board && (mode === "instruction" || mode === "auto" && snapshot.agents.find(agent=>agent.id===recipient)?.config.interaction !== "interactive");
      const operation = board ? "board.post" : instruction ? "instruct" : "message.send";
      const args = board ? {board,text,reply_to:reference} : {to:[recipient],text,reply_to:reference};
      const result = await sendFrozen(operation,args,recipient);
      const kind = board ? "Board note" : instruction ? "Instruction" : "Message";
      confirmation = kind+" accepted for "+(board ? "#"+scopeLabel(board) : sessionReference(recipient))+" · "+(result.task?.id || result.message?.id || "saved")+(instruction ? researchInstruction ? ". This extra instruction interrupts the research phase protocol." : headlessInstruction ? ". Queued only; this one-shot runner may not dispatch additional tasks." : ". "+nativeDeliveryHint(recipient) : ". "+(board ? "Readers must subscribe for native delivery." : receiverDeliveryExplanation(recipient,"message.send")));
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
    state: review.state,
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
  const memoryOperation=version>0 && !["deleted","expired","missing"].includes(memoryBaseline.state) ? "memory.patch" : "memory.put";
  action(async () => {
    let result;
    try {
      result = await call(memoryOperation, { namespace, key, value, version });
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
      memoryBaseline = { namespace, key, value, state:"live" };
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
        : "Shared board. Permitted readers can read this context. Native delivery also requires their board subscription and an enabled compatible receiver."
      : !instruction
        ? "Inbox message. "+receiverDeliveryExplanation(selected,"message.send")
        : a?.config.interaction === "interactive"
          ? nativeDeliveryHint(selected)
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
  const list = $("observed-sessions"), view=captureViewState(list);
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
    emptySearch(list, "existing terminals");
    restoreViewState(list,view);
    return;
  }
  for (const s of filtered) {
    const row = el("article", "session-row"),
      copy = el("div");
    row.dataset.itemKey=s.id;
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
    copy.append(el("small","",s.workspace_origins?.length?"Observed workspace: "+s.workspace_origins.join(", ")+" · provenance only":"Workspace origin unknown"));
    const controls = el("div", "session-actions"),
      title = el("input"),
      name = el("input"),
      details = el("details"),
      summary = el("summary", "", "Stable ID");
    const draft = observedDrafts.get(s.id);
    title.dataset.focusKey="observed-title:"+s.id;name.dataset.focusKey="observed-name:"+s.id;details.dataset.disclosureKey="observed-id:"+s.id;summary.dataset.focusKey="observed-summary:"+s.id;
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
    const connect = el("button", "", "Prepare bridge");
    connect.dataset.focusKey="observed-connect:"+s.id;
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
          $("bridge-origin").textContent=s.workspace_origins?.length?"Observed workspace origin: "+s.workspace_origins.join(", ")+". Provenance grants no access; verify this original terminal before pasting setup.":"Workspace origin unknown. Verify the original native terminal before pasting setup.";
          $("bridge-panel").classList.remove("hidden");
        }
      }, "Bridge ready. Copy setup and paste it in the original native session. Its conversation was not changed.");
    };
    controls.append(title, details, connect);
    row.append(copy, controls);
    list.append(row);
  }
  restoreViewState(list,view);
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
  if (!selectionReady()) return;
  const id=selected, agent=snapshot.agents.find(item=>item.id===id), ids=[id,...activeDescendants(snapshot.agents,id)];
  reviewOwnerAction({kind:"close",target:id,ids,version:snapshot.boundaries.version,fingerprint:ownerActionFingerprint(ids),title:"Close "+ids.length+" session"+(ids.length===1?"":"s")+"?",description:"Exact subtree: "+ids.map(sessionReference).join(", ")+". This revokes broker access and requests managed process shutdown. Process termination requires a separate acknowledgment. "+(agent.external ? "The external terminal keeps running in its original window." : "Accepted tasks may still need cancellation."),operation:"agent.stop",args:{id,expected_agents:[...ids]},origin:"close-agent"});
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
      commands: !!(snapshot.groups || []).find(group=>group.agents?.length===2 && group.agents.includes(from) && group.agents.includes(to))?.commands,
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
$("disconnect-pair").onclick = () => {
  if (!selectionReady() || selected===peer) return;
  const group=(snapshot.groups||[]).find(item=>item.agents?.length===2 && item.agents.includes(selected) && item.agents.includes(peer));
  if (!group) { notice("No exact owner group connects this pair. Review individual directional permissions or the contributing groups below.");return; }
  if (!(group.contributions_version>=1)) {activateTab("teams");notice("Review the legacy group's exact controls and migration policy before disabling it.",true);return;}
  reviewOwnerAction({kind:"pair",target:selected,ids:[selected,peer],version:snapshot.boundaries.version,fingerprint:JSON.stringify(group),group:group.id,title:"Disable only this pair's group?",description:"Remove group "+group.id+" contributions for "+[selected,peer].map(sessionReference).join(" and ")+". Other groups and explicit overrides can keep access enabled. Reconnecting preserves the saved mutual-instruction choice ("+(group.commands?"on":"off")+").",operation:"control.update",args:{version:snapshot.boundaries.version,kind:"collaboration",agents:[...group.agents],enabled:false,commands:!!group.commands},origin:"disconnect-pair"});
};
function isolationImpact(id) {
  const teams=(snapshot.teams||[]).filter(team=>team.state==="active"),owned=teams.filter(team=>team.owner===id),members=teams.filter(team=>team.owner!==id&&team.members?.includes(id)),invited=teams.filter(team=>team.owner!==id&&!team.members?.includes(id)&&team.invited?.includes(id));
  const groups=(snapshot.groups||[]).filter(group=>(group.members||group.agents||[]).includes(id));
  const teamName=team=>(team.name||team.id)+" ("+team.id+")",peers=team=>(team.members||[]).map(sessionReference).join(", ")||"none";
  const expectedTeams=[...owned.map(team=>({id:team.id,version:team.version,agent:id,action:"dissolve"})),...members.map(team=>({id:team.id,version:team.version,agent:id,action:"remove_member"})),...invited.map(team=>({id:team.id,version:team.version,agent:id,action:"withdraw_invitation"}))].sort((a,b)=>{for(const key of ["id","agent","action"]){if(a[key]<b[key])return -1;if(a[key]>b[key])return 1;}return 0;});
  return {groups,owned,members,invited,expectedTeams,details:[
    {label:"Owned agent teams permanently dissolved",items:owned.map(team=>teamName(team)+" · every member loses this team's shared access: "+peers(team)+" · board "+team.board+" · memory "+team.memory)},
    {label:"Team memberships removed",items:members.map(team=>teamName(team)+" · remove "+sessionReference(id)+"; other members keep the team")},
    {label:"Pending invitations withdrawn",items:invited.map(team=>teamName(team)+" · withdraw invitation to "+sessionReference(id))},
    {label:"Owner-group memberships suspended",items:groups.map(group=>(group.name||group.id)+" ("+group.id+")")}
  ]};
}
function isolationFingerprint(id) {
  const impact=isolationImpact(id);
  return JSON.stringify([snapshot.boundaries,impact.groups,...[impact.owned,impact.members,impact.invited].map(teams=>teams.map(team=>[team.id,team.version,team.owner,team.pending_owner,team.members,team.invited,team.board,team.memory]))]);
}
$("isolate-agent").onclick = () => {
  if (!selectionReady()) return;
  if(snapshot.teams_error || !Array.isArray(snapshot.teams)){notice("Current agent-team inventory is unavailable. Refresh before reviewing global isolation; its team effects cannot be safely enumerated.",true);return;}
  const id=selected,b=snapshot.boundaries,impact=isolationImpact(id);
  reviewOwnerAction({kind:"isolate",target:id,ids:[id],version:b.version,fingerprint:isolationFingerprint(id),details:impact.details,title:"Make "+sessionReference(id)+" independent globally?",description:"This denies collaboration with every peer and shared board/memory access, suspends "+impact.groups.length+" owner-group membership(s), permanently dissolves "+impact.owned.length+" owned agent team(s) for every member, removes "+impact.members.length+" team membership(s), and withdraws "+impact.invited.length+" pending invitation(s). Dependent descendants can also lose inherited team access. Accepted tasks continue; previously read context remains known. Reconnecting an owner group or directional permissions does not restore dissolved teams, removed memberships or withdrawn invitations. Dissolved teams must be created again; joining again requires new invitations and agent consent.",operation:"control.update",args:{version:b.version,kind:"independent",agents:[id],expected_teams:impact.expectedTeams},origin:"isolate-agent"});
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
      label.append(choice);panel.append(label,el("p","session-note","This group predates independent contribution tracking. Existing untagged direct overrides may be historical group grants or deliberate owner intent; Agentmux cannot infer which. Preserve keeps those direct overrides even after disabling this group. Move removes matching ALLOW overrides so the group governs them. Existing DENY overrides remain and require a separate explicit reset in Connections."));
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
  await action(async()=>{await call("control.update",{version:review.version,kind:"collaboration",agents:review.agents,enabled:review.enabled,commands:review.commands,legacy_policy:review.policy});if(legacyGroupReview===review){legacyGroupReview=null;legacyGroupChoices.delete(review.id);}teamsSignature="";},"Legacy policy migrated with your explicit choice. Existing DENY controls remain; review effective permissions in Connections.");
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
    eventHistory.events || snapshot.activity.events || [],
    (snapshot.agents || []).map((agent) => [agent.id, agent.title]),
  ]);
  $("event-history-status").textContent=eventHistory.loading?"Loading event history…":eventHistory.error?"History could not refresh: "+eventHistory.error:eventHistory.events===null?"Recent events shown. Open Groups & teams to search full lifecycle history.":eventHistory.events.length+" matching event(s) loaded · "+(eventHistory.hasMore?"older events available":"end of history");
  $("load-more-events").hidden=!eventHistory.hasMore;$("load-more-events").disabled=eventHistory.loading||!connected;
  if (signature === lifecycleSignature) return;
  lifecycleSignature = signature;
  const state = captureViewState(list);
  list.replaceChildren();
  for (const event of eventHistory.events || snapshot.activity.events || []) {
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
$("cancel-relaunch").onclick = () => { relaunchReview = null; $("relaunch-review").hidden = true; restoreSessionActionFocus(); };
$("relaunch-review").onsubmit = event => {
  event.preventDefault();
  const review = relaunchReview, pane = snapshot?.runtime.agents?.[review?.target];
  if (!review || !selectionReady() || selected !== review.target || snapshot.runtime.attempt_id !== review.attempt || pane?.pane_id !== review.pane || pane?.state === "running") { notice("Session changed after review. Refresh and review its current identity before relaunching.",true); return; }
  const mode = $("relaunch-resume").value, resume = mode === "id" ? $("relaunch-history-id").value.trim() : mode;
  if (mode === "id" && !resume) { notice("Enter a specific native history ID or choose a fresh conversation.",true); return; }
  action(async () => { await api("/api/relaunch",{target:review.target,resume,attempt:review.attempt,pane:review.pane}); if (relaunchReview === review) { relaunchReview = null; $("relaunch-review").hidden = true; restoreSessionActionFocus(true); } },"Relaunched " + sessionReference(review.target) + ". Healthy peers keep running; continue in its native terminal.");
};
async function loadCapacity() {
  if (!selectionReady() || capacityLoading === selected) return;
  const id = selected, request = ++capacityRequest;
  resourceSnapshot=null;
  capacityLoading=id;
  renderResourceAvailability(true);
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
  finally { if (request === capacityRequest) {capacityLoading="";if(id === selected)renderResourceAvailability();} }
}
$("load-capacity").onclick = loadCapacity;
const resourceFields = {"resource-session-tokens":"session_tokens","resource-session-cost":"session_cost","resource-turn-tokens":"turn_tokens","resource-turn-cost":"turn_cost","resource-subtree-tokens":"subtree_tokens","resource-subtree-cost":"subtree_cost","resource-admission":"admission"};
function resourceValues() { return Object.fromEntries(Object.entries(resourceFields).map(([id,key])=>[key,$(id).value])); }
function renderResourceAvailability(pending = false) {
  const ready = !pending && capacityLoading !== selected && selectionReady() && resourceSnapshot?.agent === selected;
  for (const id of Object.keys(resourceFields)) $(id).disabled=!ready;
  $("review-resources").disabled=!ready || !!resourceDrafts.get(selected) && resourceDrafts.get(selected).version!==resourceSnapshot?.version;
  $("discard-resources").disabled=!ready;
  if (!ready) $("resource-current").textContent="Loading admission limits for "+selected+". Saved drafts remain attached to their original session.";
}
function renderResourceEditor() {
  renderResourceAvailability();
  if (!resourceSnapshot || resourceSnapshot.agent!==selected) { $("review-resources").disabled=true; return; }
  const draft=resourceDrafts.get(selected), limits=resourceSnapshot;
  const values=draft?.values || {session_tokens:limits.session_budget.max_tokens || 0,session_cost:limits.session_budget.max_cost_microusd || 0,turn_tokens:limits.agent_budget.max_turn_tokens || 0,turn_cost:limits.agent_budget.max_turn_cost_microusd || 0,subtree_tokens:limits.agent_budget.max_subtree_tokens || 0,subtree_cost:limits.agent_budget.max_subtree_cost_microusd || 0,admission:""};
  for (const [id,key] of Object.entries(resourceFields)) $(id).value=String(values[key]);
  const conflict=!!draft && draft.version!==limits.version;
  const conflictPanel=$("resource-conflict");conflictPanel.hidden=!conflict;
  if(conflict){
    const adopt=el("button","quiet","Use reviewed current resource version");adopt.type="button";adopt.dataset.ownerWrite="true";adopt.disabled=!selectionReady();
    conflictPanel.replaceChildren(el("strong","","Current saved limits · v"+limits.version),el("pre","",JSON.stringify({session_budget:limits.session_budget,agent_budget:limits.agent_budget},null,2)),el("p","session-note","Your unsaved values remain above. Adopting this reviewed version preserves them and does not save."),adopt);
    adopt.onclick=()=>{if(!selectionReady()||resourceSnapshot!==limits||selected!==limits.agent)return;draft.version=limits.version;renderResourceEditor();};
  }
  $("resource-current").textContent="Current resource v"+limits.version+" · "+sessionReference(selected)+(conflict ? ". Limits changed; draft retained. Review the current limits below before adopting their version." : ". Changes apply to future admission only.");
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
  if (draft && draft.version!==resourceSnapshot.version) { notice("Resource limits changed. Review current limits and explicitly adopt their version, or discard the draft.",true); return; }
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
  if (mutating || ownerActionReview || teamReview || researchReview || relaunchReview || resourceReview || legacyGroupReview) return true;
  if(typeof attemptDrafts!=="undefined")for(const scope of attemptDrafts.values()){
    if(scope.newSessionDirty || scope.maps.resourceDrafts?.length || scope.maps.memoryDrafts?.length)return true;
    for(const [,draft] of scope.maps.messageDrafts||[])if(draft.text?.trim()||draft.reply)return true;
    for(const [,draft] of scope.maps.roleDrafts||[])if(draft.role!==draft.baseline)return true;
    for(const [,draft] of scope.maps.nativeDrafts||[])if(draft.dirty)return true;
    for(const [,draft] of scope.maps.titleDrafts||[])if(draft.title!==draft.baseline)return true;
  }
  if (resourceDrafts.size) return true;
  if ([...nativeDrafts.values()].some(draft=>draft.dirty)) return true;
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
  const global = ["research","teams","sessions","terminals"].includes(tab);
  document.body.classList.toggle("global-view", global);
  const research = snapshot?.research,
    agent = snapshot?.agents?.find((item) => item.id === selected);
  $("global-view-title").textContent =
    tab === "research"
      ? "Research" +
        (research?.progress.condition
          ? " · " + research.progress.condition
          : "")
      : tab==="sessions"?"Sessions":tab==="terminals"?"Existing terminals":"Groups & teams";
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


function ownerActionFingerprint(ids) {
  return JSON.stringify(ids.map(id=>{const a=snapshot?.agents?.find(item=>item.id===id);return [id,a?.state,a?.parent,a?.external];}));
}
function ownerActionReady(review) {
  if (!review || !selectionReady() || selected!==review.target) return false;
  if (review.kind==="close") return JSON.stringify([review.target,...activeDescendants(snapshot.agents,review.target)])===JSON.stringify(review.ids) && ownerActionFingerprint(review.ids)===review.fingerprint;
  if (review.kind==="retire")return snapshot.agents.some(agent=>agent.id===review.args.agent && agent.state==="revoked");
  if (snapshot.boundaries.version!==review.version) return false;
  if (review.kind==="pair") return JSON.stringify((snapshot.groups||[]).find(group=>group.id===review.group))===review.fingerprint;
  if (review.kind==="isolate") return !snapshot.teams_error && Array.isArray(snapshot.teams) && isolationFingerprint(review.target)===review.fingerprint;
  if (review.kind==="resolve") {if(review.args.resolution==="retry"&&!review.retryConfirmed)return false;const current=(taskHistory.tasks||snapshot.activity.tasks||[]).find(task=>task.id===review.args.id);return current?.version===review.args.version && taskRecovery(current).actions.includes(review.args.resolution);}
  return true;
}
function reviewOwnerAction(review) { ownerActionReview=review; renderOwnerActionReview(); $("owner-action-review").focus(); }
function renderOwnerActionReview() {
  const panel=$("owner-action-review"), review=ownerActionReview;
  panel.hidden=!review;
  if (!review) return;
  const state=captureViewState(panel), valid=ownerActionReady(review), confirm=el("button","","Confirm exact action"), cancel=el("button","quiet","Cancel");
  confirm.type=cancel.type="button";confirm.dataset.ownerWrite="true";confirm.dataset.focusKey="confirm-owner-action";cancel.dataset.focusKey="cancel-owner-action";confirm.disabled=!valid;
  panel.replaceChildren(el("strong","",review.title),el("p","",review.description),el("code","",review.ids.join(" · ")),el("p",valid?"session-note":"action-error",valid?"The target and impact above are frozen for this review.":review.kind==="resolve"&&review.args.resolution==="retry"&&!review.retryConfirmed?"Review the old process and confirm the recovery condition below before retrying.":"Targets or policy changed. Cancel and review the current state; this action will not adopt new targets."),confirm,cancel);
  for(const detail of review.details||[]){
    const section=el("section","isolation-impact"),items=el("ul");section.append(el("strong","",detail.label+" · "+detail.items.length));
    if(detail.items.length)for(const text of detail.items)items.append(el("li","",text));else items.append(el("li","session-note","None in the current reviewed inventory."));section.append(items);panel.append(section);
  }
  if(review.kind==="resolve"&&review.args.resolution==="retry"){
    const label=el("label","retry-acknowledgment"),check=el("input");check.type="checkbox";check.checked=!!review.retryConfirmed;check.dataset.focusKey="retry-confirmed";check.onchange=()=>{review.retryConfirmed=check.checked;confirm.disabled=!ownerActionReady(review);};label.append(check,document.createTextNode("I stopped or fenced the old turn/process and reviewed its prior outcome."));panel.append(label);
  }
  const close=()=>{ownerActionReview=null;panel.hidden=true;$(review.origin)?.focus({preventScroll:true});};
  cancel.onclick=close;
  confirm.onclick=()=>{if(!ownerActionReady(review)){notice("Review changed. Cancel and review current targets.",true);return;}action(async()=>{await call(review.operation,review.args);taskHistory.updatedAt=0;close();},"Applied reviewed action for "+review.ids.join(", ")+". Refreshing evidence.");};
  panel.onkeydown=event=>{if(event.key==="Escape"){event.preventDefault();close();}};
  restoreViewState(panel,state);
}
function restoreSessionActionFocus(completed=false) { const target=$("relaunch-agent"); (completed||target.hidden||target.disabled ? $("focus-terminal").disabled ? $("workspace") : $("focus-terminal") : target).focus({preventScroll:true}); }
function ageText(at) {const seconds=Math.max(0,Math.floor((Date.now()-Date.parse(at))/1000));return !Number.isFinite(seconds)?"age unknown":seconds<60?seconds+"s ago":seconds<3600?Math.floor(seconds/60)+"m ago":Math.floor(seconds/3600)+"h ago";}
function receiverDeliveryExplanation(id,key) {
  const agent=snapshot?.agents?.find(item=>item.id===id), status=nativeInfo(id), subscription=key==="message.send"?"notes":key==="ping.send"?"pings":"";
  if(agent?.external) return "External bridge: permission queues broker data. The receiver must explicitly read its inbox in the original native terminal; no verified automatic adapter is connected.";
  if(agent?.config.interaction!=="interactive") return key==="instruct"?"Authorized instruction runs when the managed worker is ready.":"Inbox data waits for the managed worker to read it; permission alone does not start a turn.";
  if(!status?.policy) return "Permission stores inbox data; receiver delivery status is unavailable. Inspect the receiver before assuming it will see this.";
  if(subscription && !status.policy[subscription]) return "Allowed into the inbox, but the receiver's "+subscription+" subscription is off. No automatic native turn will carry it; read manually or enable that subscription.";
  if(subscription) return "Receiver's "+subscription+" subscription is on. "+nativeDeliveryHint(id);
  return nativeDeliveryHint(id);
}
function inspectReceiver(id) {const receiver=snapshot?.agents?.find(agent=>agent.id===id);if(!receiver){notice("Receiver "+id+" is outside the active registry. Its historical tasks and exposure receipts remain searchable; include retired sessions in Sessions to review its metadata.",true);return;}const interactive=receiver.config.interaction==="interactive";selectAgent(id);activateTab("agent");const panel=$(interactive?"native-delivery":"worker-runtime");panel.setAttribute("tabindex","-1");panel.focus({preventScroll:true});}
function taskAttentionKind(task) {
  if(taskRecovery(task).actions.length||["uncertain","cancel_uncertain"].includes(task.delivery)||task.native?.phase==="uncertain")return "uncertain";
  if(task.state==="failed"||task.native?.phase==="failed")return "failed";
  if(!["proposed","running"].includes(task.state))return "";
  const status=nativeInfo(task.to), agent=snapshot?.agents?.find(item=>item.id===task.to);
  if(status?.mode==="review" && status.connected && ["ready","active"].includes(status.state) && task.delivery==="pending" && (status.pending||[]).some(candidate=>candidate.task_id===task.id||candidate.source_id===task.id))return "review";
  if(task.delivery==="pending" && agent?.config.interaction==="interactive" && (agent.external || !status?.connected || status.mode==="manual" || !["ready","active"].includes(status.state)))return "blocked";
  return "";
}
function renderAttentionQueue() {
  const panel=$("attention-queue");if(!panel||!snapshot)return;
  const state=captureViewState(panel);panel.replaceChildren(el("h3","","Delivery attention · current session statuses"));
  const total=taskSummary.agents.reduce((sum,row)=>sum+row.total,0);
  $("task-summary-status").textContent=taskSummary.loading?"Loading complete retained task counts…":taskSummary.error?"Task counts could not refresh: "+taskSummary.error:taskSummary.updatedAt?total+" tasks across "+taskSummary.agents.length+" recipients · complete retained history · independent of loaded task pages":"Complete task counts have not loaded yet.";
  if(taskSummary.updatedAt){
    panel.append(el("h3","","Recorded task outcomes · complete retained history"));
    for(const summary of taskSummary.agents.filter(row=>row.failed||row.uncertain||row.pending)){
      const row=el("article","attention-row"),inspect=el("button","quiet","Inspect receiver delivery");row.dataset.itemKey="summary:"+summary.agent;inspect.type="button";inspect.dataset.focusKey="summary:"+summary.agent;inspect.onclick=()=>inspectReceiver(summary.agent);
      row.append(el("strong","",sessionReference(summary.agent)),el("p","session-note",summary.total+" total · "+summary.failed+" failed · "+summary.uncertain+" uncertain · "+summary.pending+" pending · "+summary.running+" running. Pending alone does not prove delivery is blocked."),inspect);panel.append(row);
    }
  }
  const statuses={...(snapshot.native_statuses||{}),...(snapshot.native?.agent?{[snapshot.native.agent]:snapshot.native}:{})};
  const classify=status=>status.state==="uncertain"||status.active?.phase==="uncertain"?"Delivery uncertain":status.state==="failed"||status.active?.phase==="failed"?"Delivery failed":(status.pending_count||status.pending?.length)>0?status.mode==="review"&&status.connected&&["ready","active"].includes(status.state)?"Review required":!status.connected||status.mode==="manual"||!["ready","active"].includes(status.state)?"Delivery blocked":"":"";
  const rows=Object.entries(statuses).filter(([,status])=>classify(status));
  for(const [id,status] of rows) {
    const row=el("article","attention-row"),open=el("button","quiet","Inspect receiver delivery");row.dataset.itemKey=id;open.type="button";open.dataset.focusKey="attention:"+id;open.onclick=()=>inspectReceiver(id);
    row.append(el("strong","",classify(status)+" · "+sessionReference(id)),el("p","session-note",(status.reason||"Awaiting receiver admission")+" · "+(status.pending_count||status.pending?.length||0)+" eligible pending turn(s)"),open);panel.append(row);
  }
  const uncertain=[...submissionRecords.values()].filter(record=>record.status==="uncertain");
  for(const record of uncertain){const row=el("article","attention-row"),open=el("button","quiet","Inspect exact submission");row.dataset.itemKey=record.id;open.type="button";open.dataset.focusKey="submission-attention:"+record.id;open.onclick=()=>{activateTab("activity");const target=[...$("submission-records").children].find(item=>item.dataset.itemKey===record.id);if(target){target.open=true;target.setAttribute("tabindex","-1");target.focus();}};row.append(el("strong","","Submission uncertain · "+record.id),el("p","session-note","Acceptance unknown · "+ageText(record.createdAt)+". Exact replay identity is retained."),open);panel.append(row);}
  if(!rows.length&&!uncertain.length)panel.append(el("p","session-note","No review, blocked or uncertain state reported by the current native status inventory or local submission records. Use task history for recorded failures."));
  restoreViewState(panel,state);
}
const submissionStorageKey="agentmux-submissions:"+location.host;
function saveSubmissionRecords() {try{sessionStorage.setItem(submissionStorageKey,JSON.stringify([...submissionRecords.values()]));}catch{}renderSubmissionRecords();}
function restoreSubmissionRecords() {try{for(const row of JSON.parse(sessionStorage.getItem(submissionStorageKey)||"[]")){if(row && typeof row.id==="string" && ["board.post","message.send","instruct"].includes(row.operation) && row.args && typeof row.args.text==="string")submissionRecords.set(row.id,{...row,route:{follow:typeof row.route?.follow==="boolean"?row.route.follow:!!row.attempt},status:row.status==="submitting"?"uncertain":row.status});}}catch{}renderSubmissionRecords();}
async function dispatchSubmissionRecord(record) {
  if(routingPending || !connected || record.attempt!==snapshotIdentity()){
    const error=new Error("This saved submission belongs to another attempt or an unresolved workspace route. Its original request identity is retained; inspect that attempt before replaying.");error.code="conflict";throw error;
  }
  const wasUncertain=record.status==="uncertain";
  record.status="submitting";saveSubmissionRecords();
  try {const result=await call(record.operation,record.args,record.id,record.attempt,record.route);record.status="accepted";record.resultID=result.task?.id || result.message?.id || "";record.error="";saveSubmissionRecords();return result;}
  catch(error){record.status=!wasUncertain && ["invalid","conflict","forbidden","not_found","limit","unsupported","expired"].includes(error.code)?"rejected":"uncertain";record.error=error.message;saveSubmissionRecords();throw error;}
}
async function sendFrozen(operation,args,target) {
  const attempt=snapshotIdentity(),fingerprint=JSON.stringify([attempt,operation,args]),existing=[...submissionRecords.values()].find(record=>record.fingerprint===fingerprint && ["uncertain","submitting"].includes(record.status));
  const record=existing || {id:"web-"+(globalThis.crypto?.randomUUID?.() || Date.now().toString(36)+"-"+Math.random().toString(36).slice(2)),operation,args:JSON.parse(JSON.stringify(args)),target,attempt,route:{follow:followStudy},fingerprint,createdAt:new Date().toISOString(),status:"submitting"};
  submissionRecords.set(record.id,record);return dispatchSubmissionRecord(record);
}
function renderSubmissionRecords() {
  const panel=$("submission-records");if(!panel)return;
  const state=captureViewState(panel), allRecords=[...submissionRecords.values()], records=[...allRecords.filter(record=>record.status==="uncertain"),...allRecords.filter(record=>record.status!=="uncertain").slice(-12)];panel.hidden=!records.length;panel.replaceChildren();
  for(const record of records.reverse()) {
    const row=el("details","submission-record");row.dataset.itemKey=record.id;row.dataset.disclosureKey=record.id;
    row.append(el("summary","",record.status+" · "+(record.operation==="board.post"?"#"+record.args.board:sessionReference(record.target))+" · "+ageText(record.createdAt)),el("code","",record.id),el("pre","",record.args.text));
    if(record.resultID)row.append(el("code","","Accepted item: "+record.resultID));
    if(record.error)row.append(el("p","action-error",record.error));
    if(record.status==="uncertain") {
      row.append(el("p","session-note","Acceptance is unknown. Replay uses the exact frozen request identity and payload; it cannot create a second task if this request was already saved."));
      const retry=el("button","","Replay exact submission");retry.type="button";retry.dataset.focusKey="replay:"+record.id;retry.disabled=!connected||routingPending||mutating||record.attempt!==snapshotIdentity();retry.onclick=()=>action(()=>dispatchSubmissionRecord(record),"Recovered the exact submission. Inspect its saved item ID.");row.append(retry);
    }
    if(record.status!=="submitting") {const dismiss=el("button","quiet",record.status==="uncertain"?"Keep draft; dismiss recovery record":"Dismiss record");dismiss.type="button";dismiss.dataset.focusKey="dismiss:"+record.id;dismiss.onclick=()=>{submissionRecords.delete(record.id);saveSubmissionRecords();};if(record.status!=="uncertain")row.append(dismiss);}
    panel.append(row);
  }
  restoreViewState(panel,state);
}
restoreSubmissionRecords();

async function loadMessageHistory(mode="refresh") {
  if(!connected || !snapshot || (messageHistory.loading && mode!=="reset"))return;
  const epoch=++messageHistory.epoch, append=mode==="more", scope=boardFilter==="team"?"board":"all", inbox=boardFilter==="agent", agent=selected, query=messageHistory.query, board=messageHistory.board, target=append?50:Math.max(50,messageHistory.messages?.length||0);
  messageHistory.loading=true;messageHistory.error="";renderFeed();
  let cursor=append?messageHistory.cursor:"",rows=[],hasMore=false;
  try {
    do {
      const page=await call("message.list",{scope,agent:inbox?agent:"",board,query,after:cursor,order:"desc",limit:50});
      if(epoch!==messageHistory.epoch || inbox && selected!==agent)return;
      const incoming=page.messages||[],next=page.cursor||cursor;rows.push(...incoming);hasMore=page.has_more===true;
      if(!incoming.length || cursor===next){hasMore=false;break;}cursor=next;
    }while(hasMore&&rows.length<target);
    if(epoch!==messageHistory.epoch)return;
    messageHistory.messages=[...new Map((append?[...(messageHistory.messages||[]),...rows]:rows).map(item=>[item.id,item])).values()];
    Object.assign(messageHistory,{cursor,hasMore,scope,agent,updatedAt:Date.now()});feedSignature="";
  }catch(error){if(epoch===messageHistory.epoch)messageHistory.error=error.message;}
  finally{if(epoch===messageHistory.epoch){messageHistory.loading=false;renderFeed();}}
}
$("message-history-filter").onsubmit=event=>{event.preventDefault();messageHistory.query=$("message-query").value.trim();messageHistory.board=$("message-board").value;loadMessageHistory("reset");};
$("load-more-messages").onclick=()=>loadMessageHistory("more");
async function loadEventHistory(mode="refresh") {
  if(!connected || !snapshot || (eventHistory.loading&&mode!=="reset"))return;
  const epoch=++eventHistory.epoch,append=mode==="more",query=eventHistory.query,agent=eventHistory.agent,type=eventHistory.type,target=append?100:Math.max(100,eventHistory.events?.length||0);
  eventHistory.loading=true;eventHistory.error="";renderLifecycle();
  let cursor=append?eventHistory.cursor:"",rows=[],hasMore=false;
  try {
    do {
      const page=await call("events.list",{query,agent,type,after:cursor,order:"desc",limit:100});if(epoch!==eventHistory.epoch)return;
      const incoming=page.events||[],next=page.cursor||cursor;rows.push(...incoming);hasMore=page.has_more===true;if(!incoming.length||cursor===next){hasMore=false;break;}cursor=next;
    }while(hasMore&&rows.length<target);
    if(epoch!==eventHistory.epoch)return;
    eventHistory.events=[...new Map((append?[...(eventHistory.events||[]),...rows]:rows).map(item=>[item.id,item])).values()];Object.assign(eventHistory,{cursor,hasMore,updatedAt:Date.now()});lifecycleSignature="";
  }catch(error){if(epoch===eventHistory.epoch)eventHistory.error=error.message;}
  finally{if(epoch===eventHistory.epoch){eventHistory.loading=false;renderLifecycle();}}
}
$("event-history-filter").onsubmit=event=>{event.preventDefault();eventHistory.query=$("event-query").value.trim();eventHistory.agent=$("event-agent").value.trim();eventHistory.type=$("event-type").value.trim();loadEventHistory("reset");};
$("reset-event-filter").onclick=()=>{for(const id of ["event-query","event-agent","event-type"])$(id).value="";Object.assign(eventHistory,{query:"",agent:"",type:""});loadEventHistory("reset");};
$("load-more-events").onclick=()=>loadEventHistory("more");
$("task-attention-filter").onchange=()=>{taskAttention=$("task-attention-filter").value;taskSignature="";renderTasks();};
function quickNewSession() {
  const agent=snapshot?.agents?.find(item=>item.id===selected);
  activateTab("sessions");
  // Opening an existing unsaved new-session form keeps its draft intact.
  if(!newSessionDirty && !$("new-title").value && !$("new-name").value && !$("new-role").value) {
    $("new-harness").value=agent?.config?.harness || "claude";
    $("new-project").value=agent?.config?.workdir || $("new-project").value;
    updateNewNativeProfile();
  }
  $("new-title").focus();
}
$("quick-new-session").onclick=quickNewSession;
function availableActions() {
  const ready=selectionReady(),agent=snapshot?.agents?.find(item=>item.id===selected),actions=[];
  const add=(label,run,shortcut="",reason="",target=selected)=>actions.push({label,run,shortcut,reason,target});
  for(const [key,label] of [["activity","Board & inbox"],["agent","Session detail & receiver delivery"],["tasks","Tasks & recovery"],["memory","Shared memory"],["graph","Connections"],["teams","Owner groups & agent teams"],["sessions","Sessions & workspaces"],["terminals","Existing terminals & bridge setup"],["research","Research study"]])if(key!=="research"||snapshot?.research)add("Open "+label,()=>activateTab(key));
  add("New session",quickNewSession,"Ctrl Shift N",ready?"":"Wait for the current workspace to reconnect");
  for(const [id,label] of [["focus-terminal","Focus native terminal"],["copy-attach","Copy attach command"],["edit-title","Rename session"],["close-agent","Review close session subtree"],["relaunch-agent","Relaunch exited session"],["graph-edit","Connect sessions"]])add(label,()=>$(id).click(),"",!ready?"Current session is loading or offline":$(id).hidden?"Unavailable for this session":$(id).disabled?$(id).title||"Unavailable for the selected session":"");
  for(const item of snapshot?.agents||[])add("Select "+sessionTitle(item),()=>selectAgent(item.id),"", "",item.id);
  return actions;
}
function renderActionMenu() {
  const query=$("action-query").value.trim().toLowerCase(),results=$("action-results");results.replaceChildren();
  for(const item of availableActions().filter(item=>(item.label+" "+item.target+" "+sessionReference(item.target)+" "+(snapshot?.agents?.find(agent=>agent.id===item.target)?.config?.harness||"")+" "+(snapshot?.agents?.find(agent=>agent.id===item.target)?.config?.workdir||"")).toLowerCase().includes(query))) {
    const button=el("button","action-result");button.type="button";button.tabIndex=-1;button.dataset.actionItem="true";button.setAttribute("role","option");button.setAttribute("aria-disabled",String(!!item.reason));
    button.append(el("strong","",item.label),el("small","",(item.target?sessionReference(item.target):"Workspace")+(item.shortcut?" · "+item.shortcut:"")+(item.reason?" · "+item.reason:"")));
    button.onclick=()=>{const current=availableActions().find(action=>action.label===item.label&&action.target===item.target);if(item.target&&!item.label.startsWith("Select ")&&item.target!==selected){notice("Selected session changed. Review the current action target.",true);renderActionMenu();return;}if(!current||current.reason){notice(current?.reason||"Action is no longer available.",true);renderActionMenu();return;}closeActionMenu(false);current.run();};results.append(button);
  }
  if(!results.childElementCount)results.append(el("p","empty","No matching action."));
}
function openActionMenu() {actionMenuOrigin=document.activeElement;$("action-menu").classList.remove("hidden");$("action-query").value="";renderActionMenu();$("action-query").focus();}
function closeActionMenu(restore=true) {$("action-menu").classList.add("hidden");if(restore)(actionMenuOrigin||$("open-actions")).focus({preventScroll:true});}
$("open-actions").onclick=openActionMenu;$("close-actions").onclick=()=>closeActionMenu();$("action-query").oninput=renderActionMenu;
$("action-menu").onkeydown=event=>{
  if(event.key==="Escape"){event.preventDefault();closeActionMenu();return;}
  const items=[...$("action-results").children].filter(item=>item.dataset.actionItem),index=items.indexOf(document.activeElement);
  if(event.key==="ArrowDown"||event.key==="ArrowUp"){event.preventDefault();items[index<0?(event.key==="ArrowDown"?0:items.length-1):(index+(event.key==="ArrowDown"?1:-1)+items.length)%items.length]?.focus();}
  if(event.key==="Enter"&&event.target===$("action-query")){event.preventDefault();items[0]?.click();}
  if(event.key==="Tab"){const controls=[$("action-query"),$("close-actions")],i=controls.indexOf(document.activeElement);event.preventDefault();(i<0?controls[event.shiftKey?0:1]:controls[(i+(event.shiftKey?-1:1)+controls.length)%controls.length])?.focus();}
};
document.addEventListener("keydown",event=>{if((event.ctrlKey||event.metaKey)&&event.key.toLowerCase()==="k"){event.preventDefault();openActionMenu();}else if((event.ctrlKey||event.metaKey)&&event.shiftKey&&event.key.toLowerCase()==="n"){event.preventDefault();quickNewSession();}});
$("include-archived").onchange=async()=>{includeArchived=$("include-archived").checked;try{archivedAgents=includeArchived?(await call("agent.list",{include_archived:true})).agents||[]:[];agentSignature="";renderSessionLists();}catch(error){notice(error.message,true);}};


function switchAttemptDraftScope(previous,next,nextSelected=selected) {
  if(previous===next)return;
  stashMemoryDraft();closeTitleEditor(true);
  if(selected)messageDrafts.set(selected,{text:$("message").value,kind:pendingDraftKind||$("message-kind").value,mode:$("message-mode").value||"auto",reply:replyTo});
  const maps={messageDrafts,roleDrafts,titleDrafts,observedDrafts,resourceDrafts,nativeDrafts,teamInviteDrafts,teamEditDrafts,legacyGroupChoices,memoryDrafts,memoryLastDrafts,memoryEntries};
  const sessionFields=["new-title","new-name","new-role","new-project","new-harness","new-sandbox"], prior={maps:Object.fromEntries(Object.entries(maps).map(([name,map])=>[name,[...map]])),session:Object.fromEntries(sessionFields.map(id=>[id,$(id).value])),nativeOnly:$("new-native-only").checked,newSessionDirty};
  attemptDrafts.set(previous,prior);
  const restore=attemptDrafts.get(next);
  for(const [name,map] of Object.entries(maps)){map.clear();for(const [key,value] of restore?.maps[name]||[])map.set(key,value);}
  for(const id of sessionFields)$(id).value=restore?.session[id] || (id==="new-harness"?"claude":id==="new-sandbox"?"local":"");
  $("new-native-only").checked=!!restore?.nativeOnly;newSessionDirty=!!restore?.newSessionDirty;
  ownerActionReview=teamReview=researchReview=legacyGroupReview=relaunchReview=resourceReview=null;
  for(const id of ["owner-action-review","research-review","relaunch-review","resource-review"])$(id).hidden=true;
  pendingRailReveal="";
  resourceSnapshot=capacityView=null;capacityLoading="";capacityRequest++;nativeSignature=boundarySignature=agentSignature=taskSignature=feedSignature=teamsSignature=lifecycleSignature="";
  memoryEditorEpoch++;memoryRequest++;memoryReviewRequest++;memoryReview=null;memoryEditorNamespace="";memoryMetadataSelection="";memoryVersion=0;memoryBaseline={namespace:"",key:"",value:""};
  $("memory-key").value=$("memory-value").value="";memoryNamespace="";
  const draft=messageDrafts.get(nextSelected);$("message").value=draft?.text||"";pendingDraftKind=draft?.kind||"direct";$("message-mode").value=draft?.mode||"auto";replyTo=draft?.reply||"";$("reply-context").textContent=replyTo?"↳ "+replyTo:"";$("clear-reply").classList.toggle("hidden",!replyTo);
  exposureHistory.epoch++;Object.assign(exposureHistory,{exposures:[],cursor:"",hasMore:false,loading:false,namespace:"",key:"",version:0,error:""});
  taskSummary.epoch++;Object.assign(taskSummary,{agents:[],loading:false,error:"",updatedAt:0});
  taskHistory.epoch++;Object.assign(taskHistory,{tasks:null,cursor:"",hasMore:false,loading:false,updatedAt:0,error:""});messageHistory.epoch++;Object.assign(messageHistory,{messages:null,cursor:"",hasMore:false,loading:false});eventHistory.epoch++;Object.assign(eventHistory,{events:null,cursor:"",hasMore:false,loading:false});
  if(typeof graphVersion!=="undefined"){graphVersion="";graphCache.clear();graphSelection=null;}
  notice("Study moved to another attempt. Previous attempt drafts are retained separately; reviews were cleared before loading the new attempt.");
}
async function loadStudyStatus() {
  if(studyLoading)return;studyLoading=true;
  try {const value=await api("/api/study");studyView=value.study;renderStudyStatus();}
  catch {if(!studyView)$("study-observer").hidden=true;}
  finally{studyLoading=false;}
}
function renderStudyStatus() {
  const panel=$("study-observer");panel.hidden=!studyView;if(!studyView)return;
  $("study-status").textContent=studyView.completed+" / "+studyView.total+" trials completed · "+studyView.state+(followStudy?" · following current attempt":" · observing this fixed attempt")+(studyView.live?" · runner live":" · no live runner reported")+". Drafts and submission recovery stay bound to their attempt.";
  const list=$("study-trials"),view=captureViewState(list);list.replaceChildren();
  for(const trial of studyView.results||[])list.append(el("p","study-trial",trial.trial+" · "+trial.condition+" · attempt "+trial.attempt+" · "+trial.state));restoreViewState(list,view);
}
function changeStudyRoute() {
  const desired=$("follow-study").checked;if(desired===followStudy)return;
  followStudy=desired;routingEpoch++;routingPending=true;connected=false;
  ownerActionReview=teamReview=researchReview=legacyGroupReview=relaunchReview=resourceReview=null;
  for(const id of ["owner-action-review","research-review","relaunch-review","resource-review"])$(id).hidden=true;
  capacityRequest++;capacityLoading="";memoryRequest++;memoryReviewRequest++;taskHistory.epoch++;taskHistory.loading=false;messageHistory.epoch++;messageHistory.loading=false;eventHistory.epoch++;eventHistory.loading=false;exposureHistory.epoch++;exposureHistory.loading=false;taskSummary.epoch++;taskSummary.loading=false;
  selectionControls();renderSubmissionRecords();renderStudyStatus();$("connection").textContent="Loading workspace route";
  notice("Loading the requested workspace route. Writes are disabled until its matching snapshot arrives; drafts stay bound to the displayed attempt.");
  refresh();
}
$("follow-study").onchange=changeStudyRoute;
async function loadResearchHistory(mode="refresh") {
  if(researchHistory.loading&&mode!=="reset")return;
  const epoch=++researchHistory.epoch,append=mode==="more",target=append?100:Math.max(100,researchHistory.records.length);let cursor=append?researchHistory.cursor:"",rows=[],hasMore=false;
  researchHistory.loading=true;researchHistory.error="";renderResearchHistory();
  try {
    do {
      const params=new URLSearchParams({after:cursor,limit:"100",actor:researchHistory.actor,type:researchHistory.type,query:researchHistory.query});const page=(await api("/api/research-history?"+params)).history;if(epoch!==researchHistory.epoch)return;
      const incoming=page.records||[],next=page.cursor||cursor;rows.push(...incoming);hasMore=page.has_more===true;if(!incoming.length||next===cursor){hasMore=false;break;}cursor=next;
    }while(hasMore&&rows.length<target);
    if(epoch!==researchHistory.epoch)return;
    researchHistory.records=[...new Map((append?[...researchHistory.records,...rows]:rows).map(row=>[row.sequence,row])).values()];Object.assign(researchHistory,{cursor,hasMore,updatedAt:Date.now()});
  }catch(error){if(epoch===researchHistory.epoch)researchHistory.error=error.message;}
  finally{if(epoch===researchHistory.epoch){researchHistory.loading=false;renderResearchHistory();}}
}
function renderResearchHistory() {
  const list=$("research-history"),view=captureViewState(list);list.replaceChildren();
  $("research-history-status").textContent=researchHistory.loading?"Loading archived study evidence…":researchHistory.error?"Archive unavailable: "+researchHistory.error:researchHistory.records.length+" archived records loaded · archive order"+(researchHistory.hasMore?" · more evidence available":" · end of available archive");
  $("load-more-research-history").hidden=!researchHistory.hasMore;$("load-more-research-history").disabled=researchHistory.loading;
  for(const row of researchHistory.records){const record=row.record||{},data=record.data||{},item=el("article","lifecycle-row"),detail=el("details"),summary=el("summary","",record.kind+" · "+record.id);item.dataset.itemKey=String(row.sequence);detail.dataset.disclosureKey=String(row.sequence);summary.dataset.focusKey="archive:"+row.sequence;item.append(el("p","session-note",row.trial+" · attempt "+row.attempt+" · record "+row.sequence));
    if(record.kind==="message")item.append(el("p","",(data.from||"unknown")+" → "+(data.board?"#"+data.board:(data.to||[]).join(", "))+" · "+(data.reply_to?"reply to "+data.reply_to:"root message")),el("p","",data.text||""));
    else if(record.kind==="exposure")item.append(el("p","",(data.reader||"unknown reader")+" received "+(data.kind||"data")+" "+(data.entity_id||data.key||"")+" · version "+(data.version??"unknown")+" · "+(data.routes||[]).join(", ")));
    else if(record.kind==="memory")item.append(el("p","",(data.namespace||"")+" / "+(data.key||"")+" · version "+(data.version??"unknown")));
    detail.append(summary,el("pre","",JSON.stringify(data,null,2)));item.append(detail);list.append(item);
  }
  if(!researchHistory.records.length&&!researchHistory.loading)list.append(el("p","empty","No archived evidence matches. Live coordination is archived at research export boundaries."));restoreViewState(list,view);
}
$("research-history-filter").onsubmit=event=>{event.preventDefault();researchHistory.query=$("research-history-query").value.trim();researchHistory.actor=$("research-history-actor").value.trim();researchHistory.type=$("research-history-kind").value;loadResearchHistory("reset");};
$("load-more-research-history").onclick=()=>loadResearchHistory("more");
$("agents").onkeydown=event=>{const rows=[...$("agents").children].filter(row=>row.dataset.agent),index=rows.indexOf(document.activeElement);if(["ArrowUp","ArrowDown","Home","End"].includes(event.key)){event.preventDefault();const next=event.key==="Home"?0:event.key==="End"?rows.length-1:index<0?(event.key==="ArrowDown"?0:rows.length-1):(index+(event.key==="ArrowDown"?1:-1)+rows.length)%rows.length;rows[next]?.focus();}else if((event.key==="Enter"||event.key===" ")&&event.target===$("agents")){event.preventDefault();(rows.find(row=>row.dataset.agent===selected)||rows[0])?.focus();}};

for(const id of ["new-title","new-name","new-role","new-project","new-harness","new-sandbox","new-native-only"])$(id).addEventListener("input",()=>{newSessionDirty=true;});


async function loadExposures(mode="refresh") {
  if(!connected||(exposureHistory.loading&&mode!=="reset"))return;
  const epoch=++exposureHistory.epoch,append=mode==="more",filters={reader:exposureHistory.reader,kind:exposureHistory.kind,route:exposureHistory.route,namespace:exposureHistory.namespace,key:exposureHistory.key,...(exposureHistory.version?{version:exposureHistory.version}:{})};
  exposureHistory.loading=true;exposureHistory.error="";renderExposures();
  try {const page=await call("exposure.list",{...filters,after:append?exposureHistory.cursor:"",limit:100});if(epoch!==exposureHistory.epoch)return;exposureHistory.exposures=append?[...exposureHistory.exposures,...(page.exposures||[])]:page.exposures||[];Object.assign(exposureHistory,{cursor:page.cursor||"",hasMore:page.has_more===true,retainedLimit:page.retained_limit||10000});}
  catch(error){if(epoch===exposureHistory.epoch)exposureHistory.error=error.message;}
  finally{if(epoch===exposureHistory.epoch){exposureHistory.loading=false;renderExposures();}}
}
function renderExposures() {
  const list=$("exposure-list"),view=captureViewState(list);list.replaceChildren();
  const exact=exposureHistory.namespace?" · exact "+exposureHistory.namespace+" / "+exposureHistory.key+" · version "+exposureHistory.version:"";
  $("exposure-status").textContent=exposureHistory.loading?"Loading exposure receipts…":exposureHistory.error?"Receipts could not refresh: "+exposureHistory.error:exposureHistory.exposures.length+" receipt(s) loaded"+exact+" · retained ledger limit "+exposureHistory.retainedLimit+(exposureHistory.hasMore?" · more receipts available":" · end of retained matching receipts");
  $("load-more-exposures").hidden=!exposureHistory.hasMore;$("load-more-exposures").disabled=exposureHistory.loading;
  for(const entry of exposureHistory.exposures){const row=el("article","lifecycle-row"),detail=el("details"),summary=el("summary","",sessionReference(entry.reader)+" · "+entry.kind+" · version "+entry.version);row.dataset.itemKey=entry.reader+":"+entry.entity_id+":"+entry.version+":"+entry.at;detail.dataset.disclosureKey=row.dataset.itemKey;summary.dataset.focusKey=row.dataset.itemKey;detail.append(summary,el("p","",entry.namespace+(entry.key?" / "+entry.key:"")+" · "+sessionTime(entry.at,"Disclosed")),el("p","session-note","Routes: "+(entry.routes||[]).join(", ")+" · reader generation "+entry.generation),el("code","",entry.entity_id),el("code","","SHA-256: "+entry.sha256));row.append(detail);list.append(row);}
  if(!exposureHistory.exposures.length&&!exposureHistory.loading)list.append(el("p","empty","No retained receipt matches. An absent receipt does not prove the session never knew this context."));restoreViewState(list,view);
}
$("exposure-filter").onsubmit=event=>{event.preventDefault();exposureHistory.reader=$("exposure-reader").value.trim();exposureHistory.kind=$("exposure-kind").value;exposureHistory.route=$("exposure-route").value.trim();loadExposures("reset");};
$("clear-exposure-version").onclick=()=>{Object.assign(exposureHistory,{namespace:"",key:"",version:0});loadExposures("reset");};
$("load-more-exposures").onclick=()=>loadExposures("more");


async function loadTaskSummary() {
  if(!connected||routingPending||taskSummary.loading)return;
  const epoch=++taskSummary.epoch;taskSummary.loading=true;taskSummary.error="";renderAttentionQueue();
  try{
    const rows=[];let cursor="";
    for(let pageNumber=0;pageNumber<50;pageNumber++){
      const page=await call("task.summary",{after:cursor,limit:200});if(epoch!==taskSummary.epoch)return;rows.push(...(page.agents||[]));
      if(!page.has_more){taskSummary.agents=rows;taskSummary.updatedAt=Date.now();agentSignature="";renderSessionLists();break;}
      if(!page.cursor||page.cursor===cursor)throw new Error("Task summary cursor did not advance");cursor=page.cursor;
      if(pageNumber===49)throw new Error("Task summary exceeds the pagination budget");
    }
  }catch(error){if(epoch===taskSummary.epoch)taskSummary.error=error.message;}
  finally{if(epoch===taskSummary.epoch){taskSummary.loading=false;renderAttentionQueue();}}
}

// Native delivery controls permission-independent admission into one existing
// conversation. Every mutation retains the displayed agent and policy version.
function nativeInfo(id = selected) {
  if (snapshot?.native?.agent === id) return snapshot.native;
  return snapshot?.native_statuses?.[id] || null;
}
function nativeDeliveryHint(id) {
  const agent = snapshot?.agents?.find((item) => item.id === id);
  if (agent?.config.interaction !== "interactive") return "Instruction tasks run when the managed worker is ready.";
  const status = nativeInfo(id);
  if (!status?.policy?.version) return "Native delivery unavailable. Commands remain queued until the session explicitly reads them.";
  if (!status.session || !status.connected) return "Native delivery " + status.state + ": " + (status.reason || "adapter is not connected") + ".";
  if (status.mode === "manual") return "Manual inbox: commands stay queued. Enable Review or Auto in Session detail → Native collaboration to deliver them.";
  if (status.mode === "review") return "Review delivery: approve each exact request in Session detail → Native collaboration before it enters the native conversation.";
  if (!["ready","active"].includes(status.state)) return "Auto delivery is blocked: " + status.reason + ".";
  return "Auto delivery enabled. Native receipts distinguish submitted, running and completed; existing terminal approvals still apply.";
}
function nativeControlReady(id, version) {
  const status = nativeInfo(id), agent = snapshot?.agents?.find((item) => item.id === id);
  return selectionReady() && selected === id && agent?.state === "active" && !agent.external && !agent.config.native_only && status?.policy?.version === version;
}
function renderNativeDelivery() {
  const panel = $("native-delivery");
  if (!panel) return;
  const agent = snapshot?.agents?.find((item) => item.id === selected);
  panel.hidden = agent?.config.interaction !== "interactive";
  if (panel.hidden) return;
  const status = nativeInfo(selected), id = selected, policy = status?.policy;
  const signature = JSON.stringify([id,status,snapshot.native_error,selectionReady(),nativeDrafts.get(id)]);
  if (nativeSignature === signature) return;
  nativeSignature = signature;
  const view = captureViewState(panel);
  panel.replaceChildren();
  const heading = el("div","section-label");
  heading.append(el("h2","","Native collaboration"),el("span","",status?.state || "UNAVAILABLE"));
  panel.append(heading);
  panel.append(el("p","session-note",status?.reason || snapshot.native_error || "This controller has no native delivery status. Reopen with a current controller before enabling automatic delivery."));
  if(agent.external) {
    panel.append(el("p","session-note","External bridge setup authorizes explicit inbox reads in its existing terminal. Automatic delivery needs a verified adapter and authenticated native session; registering a bridge does not supply one. Use the copied setup in Existing terminals, then read the inbox manually."));
    const sessions=el("button","quiet","Open existing terminal setup");sessions.type="button";sessions.onclick=()=>activateTab("terminals");panel.append(sessions);
  }
  if(status?.state==="auth_required" || /auth|credential|sign.?in/i.test(status?.reason||""))panel.append(el("p","action-error","Complete provider authentication in the original native terminal. Agentmux permissions and owner links cannot bypass provider sign-in; retry receiver delivery after authentication succeeds."));
  if (!policy?.version) { restoreViewState(panel,view); return; }
  const enabled = nativeControlReady(id,policy.version), modes = el("div","native-modes");
  modes.setAttribute("aria-label","Native delivery mode");
  for (const [value,label] of [["manual","Manual inbox"],["review","Review each turn"],["auto","Auto"]]) {
    const button = el("button", value === status.mode ? "native-mode selected" : "native-mode quiet",label);
    button.type="button"; button.dataset.ownerWrite="true"; button.dataset.focusKey="native-mode:"+value;
    button.setAttribute("aria-pressed",String(value === status.mode));
    button.disabled=!enabled || value===status.mode;
    const version=policy.version;
    button.onclick=()=>{
      if (!nativeControlReady(id,version)) { notice("Native policy changed. Refresh before changing delivery mode.",true); return; }
      action(async()=>{await call("native.configure",{agent:id,version,mode:value}); if (!nativeDrafts.get(id)?.dirty) nativeDrafts.delete(id);},value==="manual" ? "Future native delivery paused. Turns already submitted may continue; interrupt in the native terminal if needed." : "Native delivery changed to "+value+". Peer permissions and native tool approvals still apply.");
    };
    modes.append(button);
  }
  panel.append(modes,el("p","session-note",status.mode === "manual" ? "No automatic turns. Read the inbox manually. Review / Auto requires a connected compatible adapter; choosing a mode alone cannot connect an external bridge." : status.mode === "review" ? "Review the full request and approve its exact candidate below. Approval does not bypass native tool permissions." : "Authorized requests can start model turns in this conversation. Native tool approvals and provider usage apply."));
  const caps=status.capabilities || {}, capability=el("p","native-capabilities");
  capability.append(el("span","",status.adapter ? "Adapter: "+status.adapter : agent.external ? "No verified automatic bridge adapter" : "Adapter: verified session setup required"),el("span","",caps.instructions ? "Instructions supported" : "Instructions unavailable"),el("span","",caps.native_queue ? "Native queue" : status.adapter ? "Idle admission only" : "No verified delivery adapter"),el("span","",caps.preserves_draft ? "Draft preservation verified" : "Draft preservation unverified"),el("span","",caps.cancellation ? "Cancellation supported" : "Queued native turns cannot be retracted"));
  capability.append(el("span","",caps.completion_evidence === "receiver-ack" ? "Completion: receiver acknowledgment" : caps.completion_evidence === "native-events" ? "Completion: correlated native events" : "Completion evidence unavailable"));
  panel.append(capability);
  let draft=nativeDrafts.get(id);
  if (!draft || !draft.dirty && draft.version !== policy.version) { draft={version:policy.version,max_turns:policy.max_turns,max_hops:policy.max_hops,cooldown_seconds:policy.cooldown_seconds,notes:!!policy.notes,pings:!!policy.pings,boards:!!policy.boards,memory_updates:!!policy.memory_updates,results:!!policy.results,dirty:false};nativeDrafts.set(id,draft); }
  const conflict=draft.version!==policy.version;
  const settings=el("details","native-settings");settings.dataset.disclosureKey="native-settings:"+id;
  settings.append(el("summary","","Delivery limits & subscriptions · "+status.turns+" / "+policy.max_turns+" turns used"));
  const form=el("form","native-policy-form");
  const fields=el("div","native-limits");
  for (const [key,label,min,max] of [["max_turns","Turn limit",1,10000],["max_hops","Maximum hops",1,64],["cooldown_seconds","Cooldown seconds",0,86400]]) {
    const row=el("label","",label),input=el("input","");input.type="number";input.min=String(min);input.max=String(max);input.step="1";input.required=true;input.value=String(draft[key]??min);input.dataset.focusKey="native:"+key;input.disabled=!enabled;input.dataset.ownerWrite="true";
    input.oninput=()=>{draft[key]=input.value;draft.dirty=true;};row.append(input);fields.append(row);
  }
  form.append(fields);
  const subscriptions=el("div","native-subscriptions");
  for (const [key,label] of [["notes","Direct notes"],["pings","Pings"],["boards","Board updates"],["memory_updates","Shared memory updates"],["results","Results of this agent's requests"]]) {
    const row=el("label",""),input=el("input","");input.type="checkbox";input.checked=draft[key];input.disabled=!enabled;input.dataset.ownerWrite="true";input.dataset.focusKey="native:"+key;input.onchange=()=>{draft[key]=input.checked;draft.dirty=true;};row.append(input,document.createTextNode(label));subscriptions.append(row);
  }
  form.append(subscriptions,el("p","session-note","Subscriptions deliver observations as data, not new instruction grants. Shared memory access is still controlled separately."));
  if (conflict) {
    form.append(el("p","action-error","Policy changed since this draft. Review current settings before applying your saved changes."));
    const current=el("div","native-policy-current");
    current.append(el("strong","","Current saved settings · policy v"+policy.version),el("pre","",nativeSavedPolicyText(policy)),el("p","session-note","Your unsaved values remain in the form above. Review these saved settings before adopting their version; adoption does not save your draft."));
    form.append(current);
    const rebase=el("button","quiet","Use reviewed current version");rebase.type="button";rebase.disabled=!enabled;rebase.dataset.ownerWrite="true";rebase.onclick=()=>{if (!nativeControlReady(id,policy.version)) return;draft.version=policy.version;nativeSignature="";renderNativeDelivery();};form.append(rebase);
  }
  const save=el("button","","Save limits & subscriptions");save.type="submit";save.disabled=!enabled||conflict;save.dataset.ownerWrite="true";save.dataset.focusKey="native-save";form.append(save);
  const discard=el("button","quiet","Discard native draft");discard.type="button";discard.disabled=!draft.dirty&&!conflict;discard.dataset.focusKey="native-discard";
  discard.onclick=()=>{if(selected!==id || nativeDrafts.get(id)!==draft) return;nativeDrafts.delete(id);nativeSignature="";renderNativeDelivery();};form.append(discard);
  form.onsubmit=(event)=>{
    event.preventDefault();
    if (!nativeControlReady(id,draft.version)) { notice("Native policy changed. Review current settings before saving this draft.",true);return; }
    const args={agent:id,version:draft.version};
    for(const key of ["max_turns","max_hops","cooldown_seconds"]) {const value=Number(draft[key]);if(!Number.isInteger(value)||value<0){notice("Native limits must be whole numbers within the displayed ranges.",true);return;}args[key]=value;}
    for(const key of ["notes","pings","boards","memory_updates","results"]) args[key]=!!draft[key];
    action(async()=>{
      const result=await call("native.configure",args);
      if(nativeDrafts.get(id)===draft) {
        const unchanged=Object.keys(args).filter(key=>key!=="agent"&&key!=="version").every(key=>typeof args[key]==="number" ? Number(draft[key])===args[key] : draft[key]===args[key]);
        if(unchanged) nativeDrafts.delete(id);
        else if(Number.isInteger(result?.policy?.version) && result.policy.version>draft.version) draft.version=result.policy.version;
      }
    },"Saved native delivery limits and subscriptions. Access permissions remain independently enforced.");
  };
  settings.append(form);panel.append(settings);
  if (status.active) panel.append(el("p",status.active.phase==="uncertain" ? "action-error" : "session-note","Current delivery "+status.active.id+": "+status.active.phase+". "+(status.active.reason||"Awaiting a correlated native receipt.")));
  const pending=status.pending||[], queue=el("div","native-queue");
  queue.append(el("h3","","Pending native turns · "+(status.pending_count ?? pending.length)+(status.pending_more ? "+" : "")));
  if (!pending.length) queue.append(el("p","session-note","No eligible requests waiting."));
  for(const delivery of pending.slice(0,5)) {
    const item=el("details","native-candidate");item.dataset.disclosureKey="native:"+delivery.id;item.dataset.itemKey=delivery.id;
    item.append(el("summary","",delivery.kind+" from "+sessionReference(delivery.from)+" · "+ageText(delivery.created_at)),el("code","",delivery.id),el("pre","",delivery.text));
    if(status.mode==="review") {
      const approve=el("button","","Approve this exact turn"), version=policy.version, fingerprint=JSON.stringify(delivery);
      approve.type="button";approve.dataset.ownerWrite="true";approve.dataset.focusKey="native-approve:"+delivery.id;
      approve.disabled=!enabled||!status.connected||!status.session;
      approve.onclick=()=>{
        const current=nativeInfo(id), candidate=current?.pending?.find((entry)=>entry.id===delivery.id);
        if(!nativeControlReady(id,version)||current.mode!=="review"||!candidate||JSON.stringify(candidate)!==fingerprint){notice("Request or policy changed. Review the current request before approving.",true);return;}
        action(()=>call("native.approve",{agent:id,version,candidate:delivery.id}),"Approved this exact native turn. Submission and completion are reported separately.");
      };
      item.append(approve);
    }
    queue.append(item);
  }
  if(pending.length>5 || status.pending_more) queue.append(el("p","session-note",(pending.length>5 ? (pending.length-5)+" additional requests waiting. " : "More requests waiting. ")+"The next requests appear as this queue advances; Tasks shows all instruction tasks."));
  panel.append(queue,el("p","session-note","Manual stops future admission. Submitted native turns may continue. Interrupt the native terminal for immediate human intervention; use Tasks to resolve uncertain attempts."));
  restoreViewState(panel,view);
}

function nativeSavedPolicyText(policy) {
  return [["Delivery",policy.mode],["Turn limit",policy.max_turns],["Maximum hops",policy.max_hops],["Cooldown seconds",policy.cooldown_seconds],...[ ["notes","Direct notes"],["pings","Pings"],["boards","Board updates"],["memory_updates","Shared memory updates"],["results","Returned results"] ].map(([key,label])=>[label,policy[key]?"on":"off"])].map(([label,value])=>label+": "+value).join("\n");
}

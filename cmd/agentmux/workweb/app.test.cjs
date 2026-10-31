"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const source = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");

// Execute the production identity helpers and submit callback as ordinary
// JavaScript units. This starts no browser, server, terminal or model.
function identityContext() {
  const context = vm.createContext({ snapshot: null, sessionQuery: "" });
  vm.runInContext(
    source.slice(
      source.indexOf("const providerName ="),
      source.indexOf("async function api("),
    ),
    context,
  );
  return context;
}

test("external bridge status separates registry access from native observation", () => {
  const context = identityContext();
  const agent = {
    id: "bridge-one",
    external: true,
    external_ref: "process:123:stable",
    state: "active",
  };
  const runtime = { agents: { "bridge-one": { state: "running" } } };
  const absent = context.sessionStatus(agent, runtime, [
    { id: "unrelated-process" },
  ]);
  assert.equal(absent.text, "bridge active · native not currently observed");
  assert.equal(absent.active, false);
  assert.equal(absent.muted, true);
  const present = context.sessionStatus(agent, runtime, [
    { id: agent.external_ref },
  ]);
  assert.equal(present.text, "bridge active · native observed");
  assert.equal(present.active, true);
  assert.equal(present.muted, false);
  const revoked = context.sessionStatus(
    { ...agent, state: "revoked" },
    runtime,
    [{ id: agent.external_ref }],
  );
  assert.equal(revoked.text, "bridge revoked · native observed");
  assert.equal(revoked.active, false);
  assert.equal(revoked.muted, true);
  assert.equal(agent.state, "active");
  assert.equal(agent.external_ref, "process:123:stable");
});

test("human titles, project fallbacks and search retain stable identities", () => {
  const context = identityContext();
  const agent = {
    id: "claude_stable",
    title: "Fix sign-in flow",
    config: { harness: "claude", workdir: "/work/auth-service" },
  };
  context.snapshot = { agents: [agent] };
  assert.equal(context.sessionTitle(agent), "Fix sign-in flow");
  assert.equal(
    context.sessionReference(agent.id),
    "Fix sign-in flow (claude_stable)",
  );
  assert.equal(
    context.sessionTitle({ ...agent, title: "" }),
    "auth-service · Claude",
  );
  for (const query of [
    "sign-in",
    "CLAUDE",
    "auth-service",
    "claude_stable",
    "claude sign-in",
  ]) {
    context.sessionQuery = query;
    assert.equal(context.matchesSession(agent), true, query);
  }
  context.sessionQuery = "unrelated project";
  assert.equal(context.matchesSession(agent), false);
  assert.equal(agent.id, "claude_stable");
});

function renameContext() {
  const elements = {
    "task-title": { value: "Fix sign-in flow" },
    "title-editor": {},
  };
  let release;
  const response = new Promise((resolve) => {
    release = resolve;
  });
  const requests = [];
  let pending;
  let closed = 0;
  const context = vm.createContext({
    $: (id) => elements[id],
    titleEditor: { id: "native-a", version: 3 },
    titleEditorEpoch: 10,
    titleDrafts: new Map(),
    selectionReady: () => true,
    call: (operation, args) => {
      requests.push({ operation, args });
      return response;
    },
    action: (fn) => {
      pending = fn();
    },
    closeTitleEditor: () => {
      closed++;
    },
  });
  vm.runInContext(
    source.slice(
      source.indexOf('$("title-editor").onsubmit ='),
      source.indexOf("\nfunction teamOperationReady("),
    ),
    context,
  );
  return {
    context,
    elements,
    requests,
    release,
    submit: () => elements["title-editor"].onsubmit({ preventDefault() {} }),
    settled: () => pending,
    closed: () => closed,
  };
}

test("in-flight rename cannot follow a session switch or close another draft", async () => {
  const fixture = renameContext();
  fixture.submit();
  assert.equal(fixture.requests[0].operation, "agent.label");
  assert.equal(fixture.requests[0].args.id, "native-a");
  assert.equal(fixture.requests[0].args.version, 3);
  fixture.context.titleDrafts.set("native-a", {
    title: "Fix sign-in flow",
    version: 3,
  });
  fixture.context.titleEditor = { id: "native-b", version: 7 };
  fixture.context.titleEditorEpoch++;
  fixture.elements["task-title"].value = "Different task for B";
  fixture.release({ agent: { title_version: 4 } });
  await fixture.settled();
  assert.equal(fixture.requests.length, 1);
  assert.equal(fixture.context.titleEditor.id, "native-b");
  assert.equal(fixture.context.titleEditor.version, 7);
  assert.equal(fixture.elements["task-title"].value, "Different task for B");
  assert.equal(fixture.closed(), 0);
  assert.equal(fixture.context.titleDrafts.has("native-a"), false);
});

test("typing while rename is pending preserves the newer draft and advances its CAS", async () => {
  const fixture = renameContext();
  fixture.submit();
  fixture.elements["task-title"].value = "Newer title still being edited";
  fixture.release({ agent: { title_version: 4 } });
  await fixture.settled();
  assert.equal(
    fixture.elements["task-title"].value,
    "Newer title still being edited",
  );
  assert.equal(fixture.context.titleEditor.version, 4);
  assert.equal(fixture.closed(), 0);
});

test("a newer draft left behind on switch receives only its own successful version", async () => {
  const fixture = renameContext();
  fixture.submit();
  fixture.context.titleDrafts.set("native-a", {
    title: "Later A draft",
    version: 3,
  });
  fixture.context.titleDrafts.set("native-b", { title: "B draft", version: 8 });
  fixture.context.titleEditor = { id: "native-b", version: 8 };
  fixture.context.titleEditorEpoch++;
  fixture.release({ agent: { title_version: 4 } });
  await fixture.settled();
  assert.equal(
    fixture.context.titleDrafts.get("native-a").title,
    "Later A draft",
  );
  assert.equal(fixture.context.titleDrafts.get("native-a").version, 4);
  assert.equal(fixture.context.titleDrafts.get("native-b").version, 8);
  assert.equal(fixture.closed(), 0);
});

function connectionContext() {
  const nodes = new Map(),
    requests = [],
    timers = new Map();
  const node = (id) => {
    if (!nodes.has(id))
      nodes.set(id, {
        disabled: false,
        textContent: "",
        classList: { add() {}, remove() {} },
        querySelectorAll() {
          return [];
        },
      });
    return nodes.get(id);
  };
  const context = vm.createContext({
    token: "fixture",
    AbortController,
    snapshot: {
      selected: "a",
      boundaries: { agent: "a" },
      agents: [
        { id: "a", state: "active", config: { autonomy: { enabled: false } } },
      ],
    },
    selected: "a",
    connected: true,
    loading: false,
    mutating: false,
    boundarySignature: "previous",
    taskSignature: "previous",
    observedSignature: "previous",
    $: node,
    document: {
      body: { classList: { add() {}, remove() {} } },
      querySelectorAll() {
        return [];
      },
    },
    notice: (message) => {
      context.lastNotice = message;
    },
    queueMicrotask,
    updateTitleConflict() {},
    researchMutable: () => false,
    researchReviewReady: () => false,
    researchHeadless: () => false,
    nativeHeadless: () => false,
    researchReview: null,
    researchSavedOffline: false,
    setTimeout(fn) {
      const id = timers.size + 1;
      timers.set(id, fn);
      return id;
    },
    clearTimeout(id) {
      timers.delete(id);
    },
    fetch: (path, options) => {
      requests.push({ path, options });
      return new Promise((resolve, reject) =>
        options.signal.addEventListener("abort", () =>
          reject(new Error("aborted")),
        ),
      );
    },
  });
  vm.runInContext(
    source.slice(
      source.indexOf("function validSavedResearch("),
      source.indexOf("async function api("),
    ),
    context,
  );
  vm.runInContext(
    source.slice(
      source.indexOf("async function api("),
      source.indexOf("const call ="),
    ),
    context,
  );
  vm.runInContext(
    source.slice(
      source.indexOf("const selectionReady ="),
      source.indexOf("function selectAgent("),
    ),
    context,
  );
  vm.runInContext(
    source.slice(
      source.indexOf("async function action("),
      source.indexOf("function render()"),
    ),
    context,
  );
  context.render = () => vm.runInContext("selectionControls()", context);
  return { context, nodes, node, requests, timers };
}

test("a hung snapshot aborts, disables stale actions, and recovers without losing state", async () => {
  const f = connectionContext(),
    before = f.context.snapshot;
  const pending = f.context.refresh();
  assert.equal(f.requests.length, 1);
  [...f.timers.values()][0]();
  await pending;
  assert.equal(f.context.loading, false);
  assert.equal(f.context.connected, false);
  assert.equal(f.context.snapshot, before);
  assert.equal(f.node("send-message").disabled, true);
  assert.match(f.context.lastNotice, /Last snapshot shown/);
  let writes = 0;
  await f.context.action(async () => {
    writes++;
  }, "saved");
  assert.equal(
    writes,
    0,
    "offline action must not submit a mutation against stale permissions",
  );
  f.context.fetch = async () => ({
    ok: true,
    status: 200,
    text: async () => JSON.stringify(before),
  });
  await f.context.refresh();
  assert.equal(f.context.connected, true);
  assert.equal(f.node("send-message").disabled, false);
  assert.equal(f.timers.size, 0);
});

test("a timed out write reports uncertainty and is never replayed automatically", async () => {
  const f = connectionContext();
  const pending = f.context.api("/api/call", {
    operation: "memory.put",
    args: { key: "draft" },
  });
  [...f.timers.values()][0]();
  await assert.rejects(pending, /may already be saved/);
  assert.equal(f.requests.length, 1);
  assert.equal(f.timers.size, 0);
});

test("definite version conflicts are distinguished from uncertain delivery", async () => {
  const f = connectionContext();
  f.context.refresh = async () => {};
  await f.context.action(async () => {
    const error = new Error("memory version changed");
    error.code = "conflict";
    throw error;
  }, "saved");
  assert.match(f.context.lastNotice, /Refresh current state/);
  assert.doesNotMatch(f.context.lastNotice, /delivery may be uncertain/);
});

test("large graph keeps selected session and recipient visible without overlap", () => {
  const graph = fs.readFileSync(path.join(__dirname, "graph.js"), "utf8");
  const agents = Array.from({ length: 200 }, (_, i) => ({
    id: "agent-" + i,
    state: "active",
  }));
  const context = vm.createContext({
    snapshot: { agents },
    selected: "agent-199",
    peer: "agent-180",
  });
  vm.runInContext(
    graph.slice(
      graph.indexOf("function graphAgents()"),
      graph.indexOf("function graphPoint("),
    ),
    context,
  );
  const shown = context.graphAgents();
  assert.equal(shown.length, 40);
  assert.equal(
    shown[0].id,
    "agent-199",
    "selected session starts in view without scrolling",
  );
  assert.ok(shown.some((a) => a.id === "agent-199"));
  assert.ok(shown.some((a) => a.id === "agent-180"));
  const points = shown.map((_, i) =>
    context.graphDefaultPosition(i, 40, "agents"),
  );
  for (let i = 0; i < points.length; i++)
    for (let j = 0; j < i; j++)
      assert.ok(
        Math.abs(points[i].x - points[j].x) >= 144 ||
          Math.abs(points[i].y - points[j].y) >= 56,
        "overlapping readable cards",
      );
});

test("title conflicts preserve draft and require explicit review of current version", () => {
  const elements = new Map();
  const element = (id) => {
    if (!elements.has(id)) elements.set(id, {});
    return elements.get(id);
  };
  const context = vm.createContext({
    $: element,
    selected: "a",
    mutating: false,
    titleEditor: { id: "a", version: 2 },
    snapshot: {
      agents: [{ id: "a", title: "Other owner's title", title_version: 3 }],
    },
    selectionReady: () => true,
    sessionTitle: () => "Default",
  });
  vm.runInContext(
    source.slice(
      source.indexOf("function updateTitleConflict()"),
      source.indexOf("async function api("),
    ),
    context,
  );
  element("task-title").value = "My unsaved draft";
  context.updateTitleConflict();
  assert.equal(context.titleEditor.version, 2);
  assert.equal(context.titleEditor.conflict, true);
  assert.equal(element("save-title").disabled, true);
  assert.match(element("title-conflict").textContent, /Other owner's title/);
  assert.equal(element("task-title").value, "My unsaved draft");
  context.titleEditor.version = 3;
  context.updateTitleConflict();
  assert.equal(context.titleEditor.conflict, false);
  assert.equal(element("save-title").disabled, false);
});

test("editing memory while a save is pending preserves newer text as unsaved", async () => {
  const nodes = {
    "memory-form": {},
    "memory-namespace": { value: "pair-a" },
    "memory-key": { value: "proposal" },
    "memory-value": { value: "Submitted draft" },
    "memory-version": { textContent: "" },
  };
  let release, pending;
  const context = vm.createContext({
    $: (id) => nodes[id],
    selectionReady: () => true,
    memoryVersion: 4,
    memoryEditorEpoch: 2,
    call: () =>
      new Promise((resolve) => {
        release = resolve;
      }),
    action: (fn) => {
      pending = fn();
    },
    loadMemory: async () => {},
  });
  vm.runInContext(
    source.slice(
      source.indexOf('$("memory-form").onsubmit ='),
      source.indexOf("\nrefresh();"),
    ),
    context,
  );
  nodes["memory-form"].onsubmit({ preventDefault() {} });
  nodes["memory-value"].value = "Newer text still being typed";
  release({ entry: { version: 5 } });
  await pending;
  assert.equal(nodes["memory-value"].value, "Newer text still being typed");
  assert.equal(context.memoryVersion, 5);
  assert.match(nodes["memory-version"].textContent, /Unsaved changes/);
});

function replyContext(interaction) {
  const nodes = new Map();
  const node = (id) => {
    if (!nodes.has(id))
      nodes.set(id, {
        value: "",
        textContent: "",
        options: [],
        classList: { add() {}, remove() {} },
        focus() {},
        replaceChildren(...children) {
          this.options = children;
        },
      });
    return nodes.get(id);
  };
  node("message").value = "Unsent draft for A";
  node("message-kind").value = "direct";
  node("message-kind").options = [{ value: "direct" }];
  const messages = [{ id: "message-b", from: "b", to: ["a"] }],
    tasks = [{ id: "task_b", from: "$owner", to: "b" }],
    agents = [
      { id: "a", state: "active", config: { interaction: "interactive" } },
      { id: "b", state: "active", config: { interaction } },
    ];
  let release, pending;
  const requests = [];
  const context = vm.createContext({
    $: node,
    snapshot: {
      selected: "a",
      boundaries: { agent: "a", boards: [] },
      agents,
      activity: { messages, tasks },
    },
    selected: "a",
    connected: true,
    replyTo: "",
    boundarySignature: "old",
    pendingDraftKind: "",
    graphSelection: null,
    messageDrafts: new Map([
      ["b", { text: "B draft", kind: "direct", reply: "" }],
    ]),
    document: { querySelectorAll: () => [] },
    closeTitleEditor() {},
    selectionControls() {},
    refresh() {},
    activateTab() {},
    notice: (message) => {
      context.lastNotice = message;
    },
    sessionReference: (id) => "Session " + id,
    scopeLabel: (name) => name,
    el: (_tag, _class, text) => ({ textContent: text }),
    action: (fn, success) => {
      pending = fn().then(() => {
        context.lastNotice =
          typeof success === "function" ? success() : success;
      });
    },
    api: (path, args) => {
      requests.push({ path, args });
      return new Promise((resolve) => {
        release = resolve;
      });
    },
  });
  for (const [start, end] of [
    ["function nativeHeadless(", "function sessionStatus("],
    ["const selectionReady =", "function selectionControls()"],
    ["function selectAgent(", "async function action("],
    ["function replyRecipient(", "async function loadMemory("],
    ["function renderComposer()", "function renderObserved()"],
    ['$("composer").onsubmit =', '$("message").onkeydown ='],
  ])
    vm.runInContext(
      source.slice(source.indexOf(start), source.indexOf(end)),
      context,
    );
  return {
    context,
    node,
    requests,
    agents,
    release: (value) => release(value),
    settled: () => pending,
    coherent() {
      context.snapshot.selected = context.selected;
      context.snapshot.boundaries.agent = context.selected;
      context.renderComposer();
    },
    submit() {
      node("composer").onsubmit({ preventDefault() {} });
    },
  };
}

for (const interaction of ["interactive", "managed"]) {
  test(
    "reply selects and displays actual " +
      interaction +
      " recipient before sending",
    async () => {
      const f = replyContext(interaction),
        reference = interaction === "interactive" ? "message-b" : "task_b";
      f.context.setReply(reference);
      assert.equal(f.context.selected, "b");
      assert.equal(f.context.messageDrafts.get("a").text, "Unsent draft for A");
      assert.equal(f.node("message").value, "B draft");
      assert.equal(f.context.replyTo, reference);
      f.submit();
      assert.equal(
        f.requests.length,
        0,
        "pending target boundaries cannot submit",
      );
      f.coherent();
      assert.equal(f.node("send-target").textContent, "Session b");
      assert.match(
        f.node("delivery-hint").textContent,
        interaction === "interactive" ? /Inbox message/ : /Instruction task/,
      );
      f.submit();
      assert.equal(f.requests[0].args.target, "b");
      assert.equal(f.requests[0].args.reply_to, reference);
      f.context.selectAgent("a");
      assert.equal(f.node("message").value, "Unsent draft for A");
      f.release({
        target: "b",
        kind: interaction === "interactive" ? "Message" : "Instruction",
      });
      await f.settled();
      assert.equal(f.node("message").value, "Unsent draft for A");
      assert.match(f.context.lastNotice, /queued for Session b/);
      assert.match(
        f.context.lastNotice,
        interaction === "interactive" ? /^Message/ : /^Instruction/,
      );
      assert.equal(
        f.context.messageDrafts.has("b"),
        false,
        "only successfully sent B draft is cleared",
      );
    },
  );
}

test("reply recipient missing or revoked requires review without a write", () => {
  const f = replyContext("managed");
  f.agents[1].state = "revoked";
  f.context.setReply("task_b");
  assert.equal(f.context.selected, "a");
  assert.equal(f.context.replyTo, "");
  assert.match(f.context.lastNotice, /unavailable/);
  f.agents[1].state = "active";
  f.context.setReply("task_b");
  f.coherent();
  f.agents[1].state = "revoked";
  f.submit();
  assert.equal(f.requests.length, 0);
  assert.match(f.context.lastNotice, /unavailable/);
});

test("an offline memory draft stays editable with namespace and writes frozen", () => {
  const f = connectionContext(),
    key = { disabled: false, value: "proposal" },
    value = { disabled: false, value: "Unsaved text" },
    save = { disabled: false };
  f.node("memory-form").querySelectorAll = (selector) =>
    selector === "button" ? [save] : [key, value];
  f.context.connected = false;
  vm.runInContext("selectionControls()", f.context);
  assert.equal(key.disabled, false);
  assert.equal(value.disabled, false);
  assert.equal(save.disabled, true);
  assert.equal(f.node("memory-namespace").disabled, true);
  assert.equal(f.node("load-memory").disabled, true);
  assert.equal(value.value, "Unsaved text");
  f.context.selected = "another-agent";
  vm.runInContext("selectionControls()", f.context);
  assert.equal(
    key.disabled,
    true,
    "previous session's namespace cannot be edited during selection change",
  );
  assert.equal(value.disabled, true);
});

test("a completed send cannot clear a different reply started while pending", async () => {
  const f = replyContext("interactive");
  f.context.setReply("message-b");
  f.coherent();
  f.submit();
  f.context.setReply("task_b");
  f.release({ target: "b", kind: "Message" });
  await f.settled();
  assert.equal(f.context.replyTo, "task_b");
  assert.equal(f.node("message").value, "B draft");
});

test("explicit reply overrides a restored board draft after recipient boundaries load", async () => {
  const f = replyContext("interactive");
  f.context.messageDrafts.set("b", {
    text: "B board draft",
    kind: "board:pair",
    reply: "",
  });
  f.context.snapshot.boundaries.boards = [
    { name: "pair", read: true, write: true },
  ];
  const boardWrites = [];
  f.context.call = async (operation, args) => {
    boardWrites.push({ operation, args });
  };
  f.context.setReply("message-b");
  assert.equal(f.context.selected, "b");
  assert.equal(f.node("message").value, "B board draft");
  f.coherent();
  assert.equal(f.node("message-kind").value, "direct");
  assert.equal(f.node("send-target").textContent, "Session b");
  f.submit();
  assert.equal(
    boardWrites.length,
    0,
    "reply must never become board.post from restored draft kind",
  );
  assert.equal(f.requests[0].path, "/api/send");
  assert.equal(f.requests[0].args.target, "b");
  assert.equal(f.requests[0].args.reply_to, "message-b");
  f.release({ target: "b", kind: "Message" });
  await f.settled();
});

function teamControlContext() {
  const requests = [],
    team = {
      id: "team-one",
      version: 3,
      state: "active",
      owner: "creator",
      members: ["creator"],
      invited: ["invited"],
    };
  let release;
  const context = vm.createContext({
    snapshot: { teams: [team], agents: [{ id: "candidate", state: "active" }] },
    selectionReady: () => true,
    teamReview: null,
    teamsSignature: "previous",
    renderTeams() {},
    notice: (message) => {
      context.lastNotice = message;
    },
    action: (fn) => fn(),
    call: (operation, args) => {
      requests.push({ operation, args });
      return new Promise((resolve) => {
        release = resolve;
      });
    },
  });
  vm.runInContext(
    source.slice(
      source.indexOf("function teamOperationReady("),
      source.indexOf("function renderTeams("),
    ),
    context,
  );
  return { context, team, requests, release: () => release({}) };
}

test("team interventions require current consent state and preserve a newer review", async () => {
  const f = teamControlContext();
  f.context.reviewTeamChange("team.invite", f.team, "candidate");
  const original = f.context.teamReview;
  assert.equal(f.requests.length, 0, "review alone must not mutate membership");
  f.team.version++;
  await f.context.applyTeamReview();
  assert.equal(f.requests.length, 0, "stale team CAS cannot submit");
  f.team.version = 3;
  f.context.snapshot.agents[0].state = "revoked";
  await f.context.applyTeamReview();
  assert.equal(f.requests.length, 0, "revoked invitation target cannot submit");
  f.context.snapshot.agents[0].state = "active";
  const pending = f.context.applyTeamReview();
  assert.equal(f.requests[0].operation, "team.invite");
  assert.equal(f.requests[0].args.id, "team-one");
  assert.equal(f.requests[0].args.version, 3);
  assert.equal(f.requests[0].args.agent, "candidate");
  f.context.reviewTeamChange("team.remove", f.team, "invited");
  const newer = f.context.teamReview;
  assert.notEqual(newer, original);
  f.release();
  await pending;
  assert.equal(
    f.context.teamReview,
    newer,
    "late invitation completion cannot clear a different review",
  );
  assert.equal(
    f.context.teamOperationReady({
      operation: "team.join",
      id: f.team.id,
      version: 3,
      agent: "invited",
    }),
    false,
    "owner cannot impersonate participant consent",
  );
  assert.equal(
    f.context.teamOperationReady({
      operation: "team.remove",
      id: f.team.id,
      version: 3,
      agent: "creator",
    }),
    false,
    "team creator requires dissolution, not removal",
  );
});

test("headless research blocks attach without changing native workspace behavior", () => {
  const c = identityContext(),
    agent = { id: "worker" };
  c.snapshot = { runtime: { agents: {} } };
  assert.equal(c.researchHeadless(agent), false);
  c.snapshot.research = { progress: { state: "running" } };
  assert.equal(c.researchHeadless(agent), true);
  c.snapshot.runtime = {
    attempt_id: "owned-attempt",
    agents: { worker: { pane_id: "%12", state: "running" } },
  };
  assert.equal(c.researchHeadless(agent), false);
  c.snapshot.runtime.agents.worker.state = "unavailable";
  assert.equal(c.researchHeadless(agent), true);
  c.snapshot.research = null;
  c.snapshot.research_error = "State unavailable";
  assert.equal(
    c.researchHeadless(agent),
    true,
    "research status failure must not promise a native terminal",
  );
});

test("research intervention freezes attempt, CAS and reason without overwriting newer input", async () => {
  const nodes = {
      "research-reason": { value: "Inspect team formation" },
      "research-action": { value: "pause" },
    },
    requests = [];
  let release;
  const context = vm.createContext({
    snapshot: {
      research: {
        progress: { trial: "trial-1", attempt: 2, state: "running" },
        control: { version: 4, mode: "running" },
      },
    },
    researchReview: {
      trial: "trial-1",
      attempt: 2,
      version: 4,
      action: "pause",
      reason: "Inspect team formation",
    },
    selectionReady: () => true,
    $: (id) => nodes[id],
    notice: (message) => {
      context.lastNotice = message;
    },
    action: (fn) => fn(),
    call: (operation, args) => {
      requests.push({ operation, args });
      return new Promise((resolve) => {
        release = resolve;
      });
    },
  });
  vm.runInContext(
    source.slice(
      source.indexOf("function researchMutable("),
      source.indexOf("function renderResearchReview("),
    ),
    context,
  );
  vm.runInContext(
    source.slice(
      source.indexOf("async function applyResearchReview("),
      source.indexOf("function renderResearch()"),
    ),
    context,
  );
  context.snapshot.research.progress.attempt = 3;
  await context.applyResearchReview();
  assert.equal(
    requests.length,
    0,
    "review for a different attempt cannot apply",
  );
  context.snapshot.research.progress.attempt = 2;
  context.snapshot.research.control.version = 5;
  await context.applyResearchReview();
  assert.equal(
    requests.length,
    0,
    "changed control version requires explicit review",
  );
  context.snapshot.research.control.version = 4;
  const pending = context.applyResearchReview();
  assert.equal(requests[0].operation, "research.control");
  assert.equal(requests[0].args.version, 4);
  assert.equal(requests[0].args.action, "pause");
  assert.equal(requests[0].args.reason, "Inspect team formation");
  nodes["research-reason"].value = "Newer draft while request is pending";
  release({});
  await pending;
  assert.equal(
    nodes["research-reason"].value,
    "Newer draft while request is pending",
  );
  context.snapshot.research.progress.state = "completed";
  assert.equal(context.researchMutable(context.snapshot.research), false);
});

test("delegated session fallback uses template while IDs and searchable lineage stay exact", () => {
  const c = identityContext(),
    id = "child_" + "f".repeat(64),
    child = {
      id,
      title: "",
      parent: "alice",
      template: "helper",
      config: { harness: "command", workdir: "/tmp/" + id },
    };
  c.snapshot = { agents: [child] };
  assert.equal(c.sessionTitle(child), "helper · Command");
  assert.equal(c.sessionReference(id), "helper · Command (" + id + ")");
  c.sessionQuery = "alice helper";
  assert.equal(c.matchesSession(child), true);
  assert.equal(
    c.sessionTitle({ ...child, title: "Review permissions" }),
    "Review permissions",
  );
  assert.equal(
    c.sessionTitle({ ...child, owner_managed: true }),
    id + " · Command",
  );
  assert.equal(child.id, id);
  assert.equal(child.title, "");
});

test("headless research extra instructions warn before submission and preserve contextual confirmation", async () => {
  const f = replyContext("managed");
  f.context.snapshot.research = { progress: { trial: "trial-1" } };
  f.context.setReply("task_b");
  f.coherent();
  assert.match(
    f.node("message-kind").options[0].textContent,
    /Extra instruction.*interrupts run/,
  );
  assert.match(
    f.node("delivery-hint").textContent,
    /interrupt.*phase protocol/,
  );
  assert.match(f.node("delivery-hint").textContent, /shared board or memory/);
  f.submit();
  f.context.snapshot.research = null;
  f.release({ target: "b", kind: "Instruction" });
  await f.settled();
  assert.match(f.context.lastNotice, /interrupts the research phase protocol/);
  assert.doesNotMatch(f.context.lastNotice, /runs this instruction when ready/);
});

test("headless research shared-board steering does not promise an extra dispatched turn", () => {
  const f = replyContext("managed");
  f.context.snapshot.research = { progress: { trial: "trial-1" } };
  f.context.selectAgent("b");
  f.context.snapshot.boundaries.boards = [
    { name: "team", read: true, write: true },
  ];
  f.coherent();
  f.node("message-kind").value = "board:team";
  f.context.renderComposer();
  assert.match(f.node("delivery-hint").textContent, /Research steering note/);
  assert.match(f.node("delivery-hint").textContent, /next read/);
  assert.match(
    f.node("delivery-hint").textContent,
    /does not add an extra instruction task/,
  );
});

test("first headless research snapshot defaults to Research and later polls preserve navigation", async () => {
  const f = connectionContext(),
    next = f.context.snapshot,
    activated = [];
  next.research = { progress: { trial: "trial-1" } };
  f.context.snapshot = null;
  f.context.tab = "activity";
  f.context.activateTab = (name) => {
    activated.push(name);
    f.context.tab = name;
  };
  vm.runInContext(
    source.slice(
      source.indexOf("function nativeHeadless("),
      source.indexOf("function sessionStatus("),
    ),
    f.context,
  );
  f.context.fetch = async () => ({
    ok: true,
    status: 200,
    text: async () => JSON.stringify(next),
  });
  await f.context.refresh();
  assert.deepEqual(activated, ["research"]);
  assert.match(f.context.lastNotice, /Research owner console ready/);
  assert.doesNotMatch(f.context.lastNotice, /terminal input stays in tmux/);
  f.context.tab = "teams";
  await f.context.refresh();
  assert.deepEqual(activated, ["research"]);
  assert.equal(f.context.tab, "teams");
});

test("headless participant phase-controls button navigates without toggling autonomy", () => {
  const f = replyContext("managed"),
    tabs = [],
    calls = [];
  f.context.snapshot.research = { progress: { trial: "trial-1" } };
  f.context.activateTab = (name) => tabs.push(name);
  f.context.call = (operation) => calls.push(operation);
  vm.runInContext(
    source.slice(
      source.indexOf('$("pause-agent").onclick ='),
      source.indexOf("function activateTab("),
    ),
    f.context,
  );
  f.node("pause-agent").onclick();
  assert.deepEqual(tabs, ["research"]);
  assert.deepEqual(calls, []);
});

test("headless one-shot managed runs warn that additional queued tasks may not dispatch", async () => {
  const f = replyContext("managed");
  f.context.snapshot.headless = true;
  f.context.setReply("task_b");
  f.coherent();
  assert.equal(f.context.nativeHeadless(f.agents[1]), true);
  assert.equal(f.context.researchHeadless(f.agents[1]), false);
  assert.match(
    f.node("delivery-hint").textContent,
    /one-shot runner may not dispatch additional tasks/,
  );
  assert.doesNotMatch(
    f.node("delivery-hint").textContent,
    /phase protocol|runs it when ready/,
  );
  assert.equal(
    f.node("message-kind").options[0].textContent,
    "Instruction task",
  );
  f.submit();
  f.context.snapshot.headless = false;
  f.release({ target: "b", kind: "Instruction" });
  await f.settled();
  assert.match(
    f.context.lastNotice,
    /Queued only; this one-shot runner may not dispatch/,
  );
});

test("first legacy headless snapshot opens Tasks and disables fake terminal actions", async () => {
  const f = connectionContext(),
    next = f.context.snapshot,
    activated = [];
  next.headless = true;
  f.context.snapshot = null;
  f.context.tab = "activity";
  f.context.activateTab = (name) => {
    activated.push(name);
    f.context.tab = name;
  };
  vm.runInContext(
    source.slice(
      source.indexOf("function nativeHeadless("),
      source.indexOf("function sessionStatus("),
    ),
    f.context,
  );
  f.context.fetch = async () => ({
    ok: true,
    status: 200,
    text: async () => JSON.stringify(next),
  });
  await f.context.refresh();
  assert.deepEqual(activated, ["tasks"]);
  assert.equal(f.node("focus-terminal").disabled, true);
  assert.equal(f.node("copy-attach").disabled, true);
  assert.match(f.context.lastNotice, /Managed task console ready/);
  assert.doesNotMatch(
    f.context.lastNotice,
    /terminal input stays in tmux|phase controls/i,
  );
});

test("runner heartbeat distinguishes fresh, stale and unknown without inferring task state", () => {
  const c = vm.createContext({});
  vm.runInContext(
    source.slice(
      source.indexOf("function runnerHeartbeat("),
      source.indexOf("function researchMutable("),
    ),
    c,
  );
  const now = Date.parse("2026-10-31T12:00:00Z");
  assert.equal(c.runnerHeartbeat("2026-10-31T11:59:58Z", now), "fresh");
  assert.equal(c.runnerHeartbeat("2026-10-31T11:59:45Z", now), "stale");
  assert.equal(c.runnerHeartbeat("", now), "unknown");
  assert.equal(c.runnerHeartbeat("2026-10-31T12:01:00Z", now), "unknown");
});

test("delegated joining limitation is explained without changing eligibility or stored grants", () => {
  const c = vm.createContext({});
  vm.runInContext(
    source.slice(
      source.indexOf("function teamJoiningHint("),
      source.indexOf("function teamOperationReady("),
    ),
    c,
  );
  const child = { parent: "alice", capabilities: { join_teams: false } };
  assert.equal(c.teamJoiningHint(child), "joining not delegated");
  assert.equal(
    c.teamJoiningHint({ parent: "alice", capabilities: { join_teams: true } }),
    "",
  );
  assert.equal(c.teamJoiningHint({ parent: "" }), "");
  assert.equal(child.capabilities.join_teams, false);
});

function offlineResearchContext(recovered) {
  const f = connectionContext(),
    rendered = [];
  f.context.snapshot.research = {
    progress: {
      trial: "trial-stop",
      attempt: 2,
      revision: 8,
      state: "paused",
      ack_version: 3,
    },
    control: { version: 4, mode: "stopped" },
  };
  f.context.researchSavedOffline = false;
  f.context.renderResearch = () =>
    rendered.push(f.context.snapshot.research.progress.state);
  f.context.fetch = async (path, options) => {
    f.requests.push({ path, options });
    if (path.startsWith("/api/snapshot")) throw new Error("Broker unavailable");
    assert.equal(path, "/api/research-status");
    return {
      ok: true,
      status: 200,
      text: async () =>
        JSON.stringify({ ok: true, live: false, research: recovered }),
    };
  };
  return { ...f, rendered };
}

test("offline research refresh learns durable stopped state without reviving routing or replaying writes", async () => {
  const recovered = {
    progress: {
      trial: "trial-stop",
      attempt: 2,
      revision: 9,
      state: "stopped",
      ack_version: 4,
    },
    control: { version: 4, mode: "stopped" },
    runner_heartbeat_at: new Date().toISOString(),
  };
  const f = offlineResearchContext(recovered),
    before = f.context.snapshot,
    boundaries = before.boundaries,
    agents = before.agents;
  await f.context.refresh();
  assert.equal(f.context.connected, false);
  assert.equal(f.context.loading, false);
  assert.equal(
    f.context.snapshot,
    before,
    "only research metadata may change on fallback",
  );
  assert.equal(f.context.snapshot.agents, agents);
  assert.equal(f.context.snapshot.boundaries, boundaries);
  assert.equal(f.context.snapshot.selected, "a");
  assert.equal(f.context.snapshot.research.progress.state, "stopped");
  assert.equal(
    f.context.snapshot.research.runner_heartbeat_at,
    "",
    "durable metadata cannot establish current liveness",
  );
  assert.equal(f.context.researchSavedOffline, true);
  assert.deepEqual(f.rendered, ["stopped"]);
  assert.match(
    f.node("runtime-state").textContent,
    /Broker offline.*saved research state: stopped/,
  );
  assert.equal(f.node("review-research").disabled, true);
  assert.equal(f.node("send-message").disabled, true);
  let writes = 0;
  await f.context.action(async () => {
    writes++;
  }, "must not send");
  assert.equal(writes, 0);
  assert.equal(
    f.requests.length,
    2,
    "one snapshot read and one bounded research metadata fallback",
  );
  assert.ok(
    f.requests.every(({ options }) => options.method === undefined),
    "fallback never replays a mutation",
  );
});

test("offline research metadata cannot replace a different attempt or roll back observed control state", async () => {
  for (const [attempt, version] of [
    [3, 4],
    [2, 3],
  ]) {
    const f = offlineResearchContext({
      progress: { trial: "trial-stop", attempt, revision: 9, state: "stopped" },
      control: { version, mode: "stopped" },
    });
    const prior = f.context.snapshot.research;
    await f.context.refresh();
    assert.equal(f.context.snapshot.research, prior);
    assert.equal(f.context.researchSavedOffline, false);
    assert.equal(f.context.connected, false);
    assert.equal(f.requests.length, 2);
    assert.deepEqual(f.rendered, []);
  }
});

function savedResearchFixture() {
  return {
    progress: {
      trial: "saved-trial",
      attempt: 2,
      revision: 7,
      round: 1,
      ack_version: 3,
      state: "stopped",
      condition: "dynamic-teams",
      phase: "decide",
      seed: 42,
      deadline: "2026-10-31T23:00:00Z",
      updated_at: "2026-10-31T22:00:00Z",
      model_tasks_reserved: 4,
      model_tasks_limit: 20,
      population: ["alice"],
      tasks: { alice: "submitted" },
    },
    control: {
      version: 3,
      mode: "stopped",
      interventions: [
        {
          version: 3,
          action: "stop",
          reason: "Finish inspection",
          at: "2026-10-31T22:00:00Z",
          actor: "$owner",
          round: 1,
          phase: "decide",
        },
      ],
    },
    owner_mutations: 1,
  };
}
function coldSavedContext(saved, live = false) {
  const f = connectionContext(),
    activated = [];
  f.context.snapshot = null;
  f.context.selected = "";
  f.context.tab = "activity";
  f.context.activateTab = (name) => {
    activated.push(name);
    f.context.tab = name;
  };
  f.context.fetch = async (path, options) => {
    f.requests.push({ path, options });
    return {
      ok: false,
      status: 400,
      text: async () =>
        JSON.stringify({
          ok: false,
          error: "Broker offline",
          saved_research: saved,
          live,
        }),
    };
  };
  return { ...f, activated };
}

test("cold saved research opens an offline metadata-only Research view without inventing routing", async () => {
  const saved = savedResearchFixture(),
    f = coldSavedContext(saved);
  await f.context.refresh();
  assert.equal(f.context.connected, false);
  assert.equal(f.context.researchSavedOffline, true);
  assert.deepEqual(f.activated, ["research"]);
  const snapshot = f.context.snapshot;
  assert.equal(snapshot.selected, "");
  assert.equal(snapshot.agents.length, 0);
  assert.equal(snapshot.teams.length, 0);
  assert.equal(snapshot.boundaries.targets.length, 0);
  assert.equal(snapshot.boundaries.boards.length, 0);
  assert.equal(snapshot.activity.tasks.length, 0);
  assert.equal(Object.keys(snapshot.runtime.agents).length, 0);
  assert.equal(snapshot.research.progress.deadline, saved.progress.deadline);
  assert.equal(
    snapshot.research.control.interventions[0].reason,
    "Finish inspection",
  );
  assert.equal(f.node("review-research").disabled, true);
  assert.equal(f.node("send-message").disabled, true);
  assert.match(f.context.lastNotice, /Only durable research metadata/);
  assert.equal(
    f.requests.length,
    1,
    "cold bootstrap does not require a second metadata request",
  );
});

test("cold research bootstrap rejects malformed metadata and non-saved envelopes", async () => {
  for (const saved of [
    null,
    {},
    { ...savedResearchFixture(), control: null },
    {
      ...savedResearchFixture(),
      progress: {
        ...savedResearchFixture().progress,
        state: "invented",
        deadline: "invalid",
      },
    },
    {
      ...savedResearchFixture(),
      control: { ...savedResearchFixture().control, interventions: {} },
    },
  ]) {
    const f = coldSavedContext(saved);
    await f.context.refresh();
    assert.equal(f.context.snapshot, null);
    assert.equal(f.context.connected, false);
    assert.deepEqual(f.activated, []);
    assert.equal(f.requests.length, 1);
  }
  const f = coldSavedContext(savedResearchFixture(), true);
  await f.context.refresh();
  assert.equal(
    f.context.snapshot,
    null,
    "only explicitly saved live:false metadata may bootstrap",
  );
});

test("global Research and Teams headings preserve selected session and restore agent presentation", () => {
  const c = identityContext(),
    nodes = new Map(),
    classes = new Map();
  c.snapshot = {
    agents: [{ id: "alice", title: "Investigate protocol", config: {} }],
    research: { progress: { condition: "dynamic-teams" } },
  };
  c.selected = "alice";
  c.researchSavedOffline = false;
  c.$ = (id) => {
    if (!nodes.has(id)) nodes.set(id, {});
    return nodes.get(id);
  };
  c.document = {
    body: { classList: { toggle: (name, value) => classes.set(name, value) } },
  };
  vm.runInContext(
    source.slice(
      source.indexOf("function renderGlobalHeading("),
      source.indexOf("function renderMetadataOnly("),
    ),
    c,
  );
  for (const tab of ["research", "teams"]) {
    c.tab = tab;
    c.renderGlobalHeading();
    assert.equal(classes.get("global-view"), true);
    assert.match(
      nodes.get("global-view-context").textContent,
      /Investigate protocol.*alice/,
    );
    assert.equal(c.selected, "alice");
  }
  c.tab = "activity";
  c.renderGlobalHeading();
  assert.equal(classes.get("global-view"), false);
  assert.equal(c.selected, "alice");
});

test("saved metadata roster and task count stay unknown, including after a search render", () => {
  const nodes = new Map(),
    node = (id) => {
      if (!nodes.has(id))
        nodes.set(id, {
          textContent: "",
          replaceChildren(...children) {
            this.children = children;
          },
        });
      return nodes.get(id);
    };
  const c = vm.createContext({
    snapshot: { agents: [] },
    researchSavedOffline: true,
    sessionQuery: "",
    $: node,
    el: (_tag, _class, text) => ({ textContent: text }),
  });
  vm.runInContext(
    source.slice(
      source.indexOf("function renderSessionLists("),
      source.indexOf("function closeTitleEditor("),
    ),
    c,
  );
  vm.runInContext(
    source.slice(source.indexOf("function renderMetadataOnly(")),
    c,
  );
  c.renderSessionLists();
  c.renderMetadataOnly();
  assert.match(
    node("agents").children[0].textContent,
    /Agent registry not loaded/,
  );
  assert.doesNotMatch(
    node("agents").children[0].textContent,
    /No sessions match|Clear search/,
  );
  assert.equal(node("task-count").textContent, "—");
  assert.equal(node("copy-attach").textContent, "Saved metadata");
  c.sessionQuery = "alice";
  c.renderSessionLists();
  assert.match(
    node("agents").children[0].textContent,
    /Agent registry not loaded/,
  );
  assert.equal(node("registered-count").textContent, "NOT LOADED");
});

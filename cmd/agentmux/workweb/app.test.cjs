"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { domFixture } = require("./dom-fixture.cjs");
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
        value: id === "message-kind" ? "direct" : "",
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
    peer: "",
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
    renderMemoryReview() {},
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
  const f = memoryContext();
  await f.context.loadMemory();
  f.entry("plan").click();
  f.type("Submitted draft");
  let release;
  f.context.call = () =>
    new Promise((resolve) => {
      release = resolve;
    });
  f.submit();
  f.type("Newer text still being typed");
  release({ entry: { version: 5 } });
  await f.pending();
  assert.equal(f.node("memory-value").value, "Newer text still being typed");
  assert.equal(f.context.memoryVersion, 5);
  assert.match(f.node("memory-version").textContent, /Unsaved changes/);
  assert.equal(f.unload().prevented, true);
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
          this.value = children[0]?.value || "";
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

function pollingContext() {
  const dom = domFixture(),
    view = dom.node("fixture-view", "view"),
    writes = [],
    replies = [];
  for (const id of [
    "feed",
    "tasks",
    "lifecycle-events",
    "edge-controls",
    "scope-controls",
    "consented-teams",
    "owner-groups",
    "research-review",
    "research-population",
  ])
    view.append(dom.node(id));
  const context = vm.createContext({
    document: dom.document,
    $: dom.node,
    el: dom.el,
    selected: "a",
    peer: "b",
    connected: true,
    mutating: false,
    boardFilter: "all",
    feedSignature: "",
    taskSignature: "",
    boundarySignature: "",
    lifecycleSignature: "",
    teamsSignature: "",
    teamReview: null,
    teamInviteDrafts: new Map(),
    researchReview: null,
    snapshot: {
      selected: "a",
      boundaries: {
        agent: "a",
        version: 1,
        targets: [],
        boards: [],
        memory: [],
      },
      agents: ["a", "b", "c"].map((id) => ({
        id,
        state: "active",
        config: {},
      })),
      activity: { messages: [], tasks: [], events: [] },
      teams: [],
      groups: [],
    },
    sessionReference: (id) => id,
    scopeLabel: (id) => id,
    stamp: (time) => time,
    selectionReady: () => true,
    updateTitleConflict() {},
    renderMemoryReview() {},
    nativeHeadless: () => false,
    researchHeadless: () => false,
    researchMutable: () => false,
    researchReviewReady: () => false,
    setReply: (id) => replies.push(id),
    selectAgent: (id) => {
      context.selected = id;
    },
    action: (fn) => fn(),
    call: async (operation, args) => {
      writes.push({ operation, args });
      return {};
    },
  });
  for (const [start, end] of [
    ["function captureViewState(", "const notice ="],
    ["function renderFeed()", "function toggle("],
    ["function toggle(", "function replyRecipient("],
    ["function renderLifecycle()", "function runnerHeartbeat("],
    ["function selectionControls()", "function selectAgent("],
  ])
    vm.runInContext(
      source.slice(source.indexOf(start), source.indexOf(end)),
      context,
    );
  return { ...dom, context, view, writes, replies };
}

test("Board polling preserves Reply focus and reading position across unchanged and new messages", () => {
  const f = pollingContext(),
    message = {
      id: "m1",
      from: "b",
      to: ["a"],
      text: "First",
      created_at: "12:00",
    };
  f.context.snapshot.activity.messages = [message];
  f.context.renderFeed();
  const reply = f.node("feed").querySelectorAll("button")[0];
  reply.focus();
  f.view.scrollTop = 140;
  f.context.renderFeed();
  assert.equal(
    f.document.activeElement,
    reply,
    "an unchanged poll must not rebuild focused controls",
  );
  f.context.snapshot.activity.messages.unshift({
    ...message,
    id: "m2",
    text: "New message",
  });
  f.context.renderFeed();
  assert.notEqual(
    f.document.activeElement,
    reply,
    "changed data exercises actual DOM replacement",
  );
  assert.equal(f.document.activeElement.dataset.focusKey, "reply:m1");
  assert.equal(f.view.scrollTop, 140);
  f.document.activeElement.click();
  assert.deepEqual(
    f.replies,
    ["m1"],
    "restored focus still acts on the original message",
  );
});

test("lifecycle polling preserves an expanded event and its focused summary", () => {
  const f = pollingContext(),
    event = {
      id: "event-1",
      at: "12:00",
      type: "team.join",
      actor: "a",
      entity_id: "team-one",
      details: { version: 3 },
    };
  f.context.snapshot.activity.events = [event];
  f.context.renderLifecycle();
  const details = f.node("lifecycle-events").querySelectorAll("details")[0],
    summary = details.children[0];
  details.open = true;
  summary.focus();
  f.view.scrollTop = 250;
  f.context.renderLifecycle();
  assert.equal(f.document.activeElement, summary);
  f.context.snapshot.activity.events.unshift({
    ...event,
    id: "event-2",
    type: "agent.spawned",
  });
  f.context.renderLifecycle();
  assert.equal(f.document.activeElement.dataset.focusKey, "event-1");
  assert.equal(
    f
      .node("lifecycle-events")
      .querySelectorAll("details")
      .find((node) => node.dataset.disclosureKey === "event-1").open,
    true,
  );
  assert.equal(f.view.scrollTop, 250);
});

test("task updates keep open provenance and return focus to the same completed task", () => {
  const f = pollingContext(),
    task = {
      id: "task-1",
      from: "$owner",
      to: "a",
      state: "running",
      delivery: "dispatching",
      text: "Inspect",
      version: 1,
      origin: { kind: "owner" },
    };
  f.context.snapshot.activity.tasks = [task];
  f.context.renderTasks();
  const details = f.node("tasks").querySelectorAll("details")[0];
  details.open = true;
  details.children[0].focus();
  task.version++;
  f.context.renderTasks();
  assert.equal(f.document.activeElement.dataset.focusKey, "origin:task-1");
  assert.equal(f.node("tasks").querySelectorAll("details")[0].open, true);
  const cancel = f
    .node("tasks")
    .querySelectorAll("button")
    .find((node) => node.dataset.focusKey === "cancel:task-1");
  cancel.focus();
  task.state = "submitted";
  task.delivery = "completed";
  task.version++;
  f.context.renderTasks();
  assert.equal(
    f.document.activeElement.dataset.itemKey,
    "task-1",
    "vanished Cancel should retain focus at its task, not body",
  );
  assert.equal(
    f.writes.length,
    0,
    "rendering/restoring focus must never trigger an owner action",
  );
});

test("revoking a graph recipient never silently retargets controls to another active agent", () => {
  const f = pollingContext();
  f.context.renderBoundaries();
  const toggle = f.node("edge-controls").querySelectorAll("button")[0];
  toggle.focus();
  f.context.snapshot.agents[1].state = "revoked";
  f.context.renderBoundaries();
  f.context.selectionControls();
  assert.equal(f.context.peer, "b");
  assert.equal(f.node("target-agent").value, "b");
  assert.match(f.node("target-agent").options[1].textContent, /revoked/);
  assert.ok(
    f
      .node("edge-controls")
      .querySelectorAll("button")
      .every((node) => node.disabled),
  );
  assert.equal(f.node("connect-pair").disabled, true);
  assert.notEqual(
    f.document.activeElement,
    f.document.body,
    "revoked control should fall back within its connection row",
  );
  f.document.activeElement.click();
  assert.equal(f.writes.length, 0);
  f.context.peer = "c";
  f.context.renderBoundaries();
  f.context.selectionControls();
  assert.equal(
    f.node("connect-pair").disabled,
    false,
    "explicit active recipient selection re-enables control",
  );
  f.context.snapshot.agents[0].parent = "delegator";
  f.context.selectionControls();
  assert.equal(f.node("connect-pair").disabled, true);
  assert.equal(f.node("isolate-agent").disabled, true);
});

test("skip-to-workspace moves keyboard focus without changing the owner URL fragment", () => {
  const f = pollingContext();
  f.context.location = { hash: "#private-owner-capability" };
  vm.runInContext(
    source.slice(
      source.indexOf('$("skip-workspace").onclick ='),
      source.indexOf('$("refresh").onclick ='),
    ),
    f.context,
  );
  let prevented = false;
  f.node("skip-workspace").onclick({
    preventDefault() {
      prevented = true;
    },
  });
  assert.equal(prevented, true);
  assert.equal(f.document.activeElement, f.node("workspace"));
  assert.equal(f.context.location.hash, "#private-owner-capability");
});

function graphKeyboardContext() {
  const f = pollingContext(),
    canvas = f.node("graph-canvas", "graph-canvas");
  canvas.append(f.node("interaction-graph"));
  f.context.tab = "graph";
  f.context.snapshot.runtime = { session: "keyboard-fixture" };
  for (const agent of f.context.snapshot.agents)
    agent.config.harness = "command";
  f.context.sessionTitle = (agent) => agent.id;
  f.context.providerName = (name) => name;
  f.context.localStorage = { getItem: () => null, setItem() {} };
  f.context.notice = () => {};
  f.context.selectedCalls = [];
  f.context.selectAgent = (id) => {
    f.context.selected = id;
    f.context.selectedCalls.push(id);
  };
  vm.runInContext(
    fs.readFileSync(path.join(__dirname, "graph.js"), "utf8"),
    f.context,
  );
  return { ...f, canvas };
}

test("graph redraw keeps keyboard focus and Space activates node/edge without losing inspector navigation", () => {
  const f = graphKeyboardContext();
  f.context.drawGraph();
  const original = f
    .node("interaction-graph")
    .querySelectorAll("[data-node]")[0];
  original.focus();
  f.canvas.scrollTop = 180;
  f.context.snapshot.boundaries.version++;
  f.context.drawGraph();
  assert.notEqual(f.document.activeElement, original);
  assert.equal(f.document.activeElement.dataset.node, "agent:a");
  assert.equal(f.canvas.scrollTop, 180);
  const b = f
    .node("interaction-graph")
    .querySelectorAll("[data-node]")
    .find((node) => node.dataset.node === "agent:b");
  let prevented = false;
  b.onkeydown({
    key: " ",
    preventDefault() {
      prevented = true;
    },
  });
  assert.equal(prevented, true);
  assert.deepEqual(f.context.selectedCalls, ["b"]);
  f.context.selected = "a";
  f.context.drawGraph();
  assert.equal(
    f.node("interaction-graph").querySelectorAll(".edge-hit").length,
    0,
  );
  f.context.graphPick("a", "b");
  const edge = f.node("interaction-graph").querySelectorAll(".edge-hit")[0];
  assert.ok(
    edge,
    "selecting an otherwise denied connection must redraw its editable edge",
  );
  edge.focus();
  edge.onkeydown({ key: "Enter", preventDefault() {} });
  assert.equal(f.document.activeElement.id, "close-inspector");
  f.node("graph-inspector").onkeydown({ key: "Escape", preventDefault() {} });
  assert.equal(f.document.activeElement.dataset.node, "agent:a");
  assert.equal(vm.runInContext("graphSelection", f.context), null);
  f.canvas.scrollTop = 180;
  f.document.activeElement.onkeydown({
    key: "ArrowRight",
    preventDefault() {},
  });
  assert.equal(f.document.activeElement.dataset.node, "agent:a");
  assert.equal(f.canvas.scrollTop, 180);
  assert.equal(f.writes.length, 0);
});

test("research review polling preserves Confirm focus and invalidated controls retain review context", () => {
  const f = pollingContext();
  f.context.runnerHeartbeat = () => "fresh";
  f.context.researchReviewReady = () => true;
  f.context.applyResearchReview = () => {
    throw new Error("poll must not submit an intervention");
  };
  f.context.researchReview = {
    action: "pause",
    reason: "Inspect",
    trial: "trial",
    attempt: 1,
    version: 2,
  };
  vm.runInContext(
    source.slice(
      source.indexOf("function renderResearchReview("),
      source.indexOf("async function applyResearchReview("),
    ),
    f.context,
  );
  f.context.renderResearchReview();
  const original = f.node("research-review").querySelectorAll("button")[0];
  original.focus();
  f.context.renderResearchReview();
  assert.notEqual(f.document.activeElement, original);
  assert.equal(f.document.activeElement.dataset.focusKey, "confirm");
  f.context.researchReviewReady = () => false;
  f.context.renderResearchReview();
  assert.equal(f.document.activeElement, f.node("research-review"));
  assert.equal(
    f.node("research-review").querySelectorAll("button")[0].disabled,
    true,
  );
});

test("session close reports descendant revocation without claiming process termination or following navigation", async () => {
  const f = pollingContext();
  vm.runInContext(
    source.slice(
      source.indexOf("function activeDescendants("),
      source.indexOf("function nativeHeadless("),
    ),
    f.context,
  );
  const agents = [
    { id: "a", state: "active" },
    { id: "child", state: "active", parent: "a" },
    { id: "grandchild", state: "active", parent: "child" },
    { id: "unrelated", state: "active", parent: "$owner" },
    { id: "old-child", state: "revoked", parent: "a" },
  ];
  assert.deepEqual(
    [...f.context.activeDescendants(agents, "a")],
    ["child", "grandchild"],
  );
  let pending;
  f.context.action = (fn, success) => {
    pending = fn().then(() => {
      f.context.lastNotice = success();
    });
  };
  f.context.call = async (operation, args) => {
    f.writes.push({ operation, args });
    return { revoked_agents: 3, process_stopped: false };
  };
  vm.runInContext(
    source.slice(
      source.indexOf('$("close-agent").onclick ='),
      source.indexOf('$("connect-pair").onclick ='),
    ),
    f.context,
  );
  f.node("close-agent").onclick();
  f.context.selected = "b";
  await pending;
  assert.equal(f.writes[0].args.id, "a");
  assert.match(f.context.lastNotice, /a and 2 descendants/);
  assert.match(f.context.lastNotice, /termination is not yet acknowledged/);
  assert.doesNotMatch(f.context.lastNotice, /Closed|terminated successfully/);
});

function unloadContext() {
  const f = pollingContext(),
    listeners = new Map();
  Object.assign(f.context, {
    messageDrafts: new Map(),
    titleDrafts: new Map(),
    observedDrafts: new Map(),
    titleEditor: null,
    replyTo: "",
    memoryEditorNamespace: "",
    memoryEditorEpoch: 0,
    memoryRequest: 0,
    memoryVersion: 0,
    memoryBaseline: { namespace: "", key: "", value: "" },
    memoryReview: null,
    memoryReviewRequest: 0,
    memoryDrafts: new Map(),
    memoryLastDrafts: new Map(),
    memoryEntries: new Map(),
    window: {
      addEventListener: (name, handler) => listeners.set(name, handler),
    },
  });
  vm.runInContext(
    source.slice(
      source.indexOf("function memoryDraftKey("),
      source.indexOf('$("skip-workspace").onclick ='),
    ),
    f.context,
  );
  vm.runInContext(
    source.slice(
      source.indexOf("function hasUnsavedWork("),
      source.indexOf("function renderGlobalHeading("),
    ),
    f.context,
  );
  vm.runInContext(
    source.slice(
      source.indexOf('window.addEventListener("beforeunload"'),
      source.indexOf("function renderComposer()"),
    ),
    f.context,
  );
  return {
    ...f,
    unload() {
      const event = {
        prevented: false,
        preventDefault() {
          this.prevented = true;
        },
      };
      listeners.get("beforeunload")(event);
      return event;
    },
  };
}

test("beforeunload stays silent while idle/polling and protects unsaved messages, replies and pending writes", () => {
  const f = unloadContext();
  f.context.loading = true;
  assert.equal(f.unload().prevented, false);
  f.node("message").value = "Unsent coordination";
  assert.equal(f.unload().prevented, true);
  assert.equal(f.unload().returnValue, "");
  f.node("message").value = "";
  f.context.messageDrafts.set("a", {
    text: "Stale cached current draft",
    reply: "",
  });
  assert.equal(
    f.unload().prevented,
    false,
    "current live editor supersedes its cached draft",
  );
  f.context.messageDrafts.set("b", {
    text: "Other session's draft",
    reply: "",
  });
  assert.equal(f.unload().prevented, true);
  f.context.messageDrafts.clear();
  f.context.replyTo = "message-to-reply";
  assert.equal(f.unload().prevented, true);
  f.context.replyTo = "";
  f.context.mutating = true;
  assert.equal(f.unload().prevented, true);
  f.context.mutating = false;
  f.context.researchReview = { action: "pause" };
  assert.equal(f.unload().prevented, true);
  f.context.researchReview = null;
  assert.equal(f.unload().prevented, false);
});

test("beforeunload ignores unchanged copied memory but protects actual local memory edits", async () => {
  const f = unloadContext();
  f.node("memory-namespace").value = "team";
  f.context.call = async () => ({
    entries: [{ key: "plan", value: "Saved server value", version: 4 }],
  });
  vm.runInContext(
    source.slice(
      source.indexOf("function memoryDraftKey("),
      source.indexOf('$("skip-workspace").onclick ='),
    ),
    f.context,
  );
  await f.context.loadMemory();
  f.node("memory-list").querySelectorAll("button")[0].click();
  assert.equal(
    f.unload().prevented,
    false,
    "selecting a server entry creates no unsaved work",
  );
  f.node("memory-value").value = "Local change";
  assert.equal(f.unload().prevented, true);
  f.node("memory-value").value = "Saved server value";
  assert.equal(f.unload().prevented, false);
  f.node("memory-key").value = "new-key";
  assert.equal(f.unload().prevented, true);
});

test("beforeunload distinguishes edited title drafts from merely opening an existing title", () => {
  const f = unloadContext();
  f.context.snapshot.agents[0].title = "Existing task";
  f.context.titleEditor = { id: "a", version: 2, baseline: "Existing task" };
  f.node("task-title").value = "Existing task";
  assert.equal(f.unload().prevented, false);
  f.context.snapshot.agents[0].title = "Another owner's concurrent rename";
  assert.equal(
    f.unload().prevented,
    false,
    "a read-only title view must not become a local edit because a server version changed",
  );
  f.node("task-title").value = "My new title";
  assert.equal(f.unload().prevented, true);
  f.context.titleEditor = null;
  f.context.titleDrafts.set("a", {
    title: "My new title",
    baseline: "Existing task",
    version: 2,
  });
  assert.equal(f.unload().prevented, true);
  f.context.titleDrafts.clear();
  assert.equal(f.unload().prevented, false);
});

function memoryContext() {
  const f = unloadContext(),
    entries = new Map([
      [
        "team",
        [
          { key: "plan", value: "Saved server value", version: 4 },
          { key: "other", value: "Other saved", version: 2 },
        ],
      ],
      ["pair", [{ key: "plan", value: "Pair saved", version: 1 }]],
    ]),
    requests = [];
  let pending;
  f.node("memory-namespace").value = "team";
  f.context.selectionReady = () =>
    f.context.connected && f.context.snapshot.selected === f.context.selected;
  f.context.call = async (operation, args) => {
    requests.push({ operation, args });
    const list = entries.get(args.namespace) || [],
      entry = list.find((item) => item.key === args.key);
    if (operation === "memory.list")
      return { entries: list.map((item) => ({ ...item })) };
    if (operation === "memory.head")
      return entry
        ? { key: entry.key, state: "live", version: entry.version }
        : { key: args.key, state: "missing", version: 0 };
    if (operation === "memory.get") return { entry: { ...entry } };
    if (operation === "memory.put") {
      if ((entry?.version || 0) !== args.version)
        throw new Error("Memory version conflict");
      const saved = {
        key: args.key,
        value: args.value,
        version: args.version + 1,
      };
      entries.set(args.namespace, [
        ...list.filter((item) => item.key !== args.key),
        saved,
      ]);
      return { entry: saved };
    }
    throw new Error("Unexpected operation " + operation);
  };
  f.context.action = (fn) => {
    pending = fn().catch((error) => {
      f.context.lastNotice = error.message;
    });
  };
  vm.runInContext(
    source.slice(
      source.indexOf('$("memory-key").oninput ='),
      source.indexOf("\nrefresh();"),
    ),
    f.context,
  );
  return {
    ...f,
    entries,
    requests,
    pending: () => pending,
    entry: (key) =>
      f
        .node("memory-list")
        .querySelectorAll("button")
        .find((node) => node.children[0].textContent === key),
    type(value) {
      f.node("memory-value").value = value;
      f.node("memory-value").oninput();
    },
    submit() {
      f.node("memory-form").onsubmit({ preventDefault() {} });
    },
    async namespace(namespace) {
      f.node("memory-namespace").value = namespace;
      await f.context.loadMemory();
    },
  };
}

test("memory entry and namespace switches retain independent drafts without changing saved CAS", async () => {
  const f = memoryContext();
  await f.context.loadMemory();
  f.entry("plan").click();
  f.type("Long plan draft");
  f.entry("other").click();
  f.type("Other local draft");
  f.entry("plan").click();
  assert.equal(f.node("memory-value").value, "Long plan draft");
  assert.equal(f.context.memoryVersion, 4);
  await f.namespace("pair");
  f.entry("plan").click();
  f.type("Pair local draft");
  await f.namespace("team");
  assert.equal(f.node("memory-key").value, "plan");
  assert.equal(f.node("memory-value").value, "Long plan draft");
  f.entry("other").click();
  assert.equal(f.node("memory-value").value, "Other local draft");
  f.node("discard-memory-draft").click();
  assert.equal(f.node("memory-value").value, "Other saved");
  assert.equal(
    f.unload().prevented,
    true,
    "other scopes' drafts still protected",
  );
  assert.equal(
    f.requests.filter((r) => r.operation === "memory.put").length,
    0,
  );
});

test("memory conflict review keeps exact draft beside latest and requires explicit CAS adoption then Save", async () => {
  const f = memoryContext();
  await f.context.loadMemory();
  f.entry("plan").click();
  f.type("My plan draft");
  f.entries.get("team")[0] = {
    key: "plan",
    value: "Other owner's revision",
    version: 5,
  };
  f.submit();
  await f.pending();
  assert.equal(f.node("memory-value").value, "My plan draft");
  assert.equal(f.context.memoryVersion, 4);
  assert.match(
    f.node("memory-version").textContent,
    /draft kept.*Review latest/,
  );
  await f.context.reviewMemoryLatest();
  assert.match(
    f.node("memory-review-target").textContent,
    /team \/ plan.*version 5/,
  );
  assert.equal(f.node("memory-review-draft").textContent, "My plan draft");
  assert.equal(
    f.node("memory-review-latest").textContent,
    "Other owner's revision",
  );
  assert.equal(
    f.context.memoryVersion,
    4,
    "read review does not adopt or retry",
  );
  assert.equal(
    f.requests.filter((r) => r.operation === "memory.put").length,
    1,
  );
  f.node("adopt-memory-version").click();
  assert.equal(f.context.memoryVersion, 5);
  assert.equal(f.node("memory-value").value, "My plan draft");
  assert.match(f.node("memory-version").textContent, /not saved/);
  assert.equal(
    f.requests.filter((r) => r.operation === "memory.put").length,
    1,
  );
  f.submit();
  await f.pending();
  assert.equal(
    f.entries.get("team").find((e) => e.key === "plan").value,
    "My plan draft",
  );
  assert.equal(f.context.memoryVersion, 6);
  assert.equal(
    f.unload().prevented,
    false,
    "saved entry creates no unload warning",
  );
});

test("memory tombstone review uses its real CAS while late reviews and saves cannot replace another entry", async () => {
  const f = memoryContext();
  await f.context.loadMemory();
  f.entry("plan").click();
  f.type("Recreated plan");
  const normalCall = f.context.call;
  f.context.call = async (operation, args) =>
    operation === "memory.head"
      ? { key: args.key, state: "deleted", version: 9 }
      : normalCall(operation, args);
  await f.context.reviewMemoryLatest();
  assert.match(f.node("memory-review-latest").textContent, /deleted/);
  f.node("adopt-memory-version").click();
  assert.equal(f.context.memoryVersion, 9);
  assert.equal(f.node("memory-value").value, "Recreated plan");
  let release;
  f.context.call = () =>
    new Promise((resolve) => {
      release = resolve;
    });
  const reviewing = f.context.reviewMemoryLatest();
  f.entry("other").click();
  release({ state: "deleted", version: 10 });
  await reviewing;
  assert.equal(f.context.memoryReview, null);
  assert.equal(f.node("memory-key").value, "other");
  f.context.call = normalCall;
  f.entry("plan").click();
  // Use a valid base for the independent save race.
  f.context.memoryVersion = 4;
  f.context.call = () =>
    new Promise((resolve) => {
      release = resolve;
    });
  f.submit();
  f.entry("other").click();
  f.type("New other draft");
  release({ entry: { version: 5 } });
  await f.pending();
  assert.equal(f.node("memory-key").value, "other");
  assert.equal(f.node("memory-value").value, "New other draft");
  assert.equal(f.context.memoryVersion, 2);
});

test("memory list refresh and offline typing retain drafts without adopting a newer saved version", async () => {
  const f = memoryContext();
  await f.context.loadMemory();
  f.entry("plan").click();
  f.type("Kept draft");
  let release;
  f.context.call = () =>
    new Promise((resolve) => {
      release = resolve;
    });
  const loading = f.context.loadMemory();
  f.entry("other").click();
  f.type("Other draft");
  release({
    entries: [
      { key: "plan", value: "Updated saved value", version: 8 },
      { key: "other", value: "Other saved", version: 2 },
    ],
  });
  await loading;
  assert.equal(f.node("memory-value").value, "Other draft");
  f.entry("plan").click();
  assert.equal(f.node("memory-value").value, "Kept draft");
  assert.equal(f.context.memoryVersion, 4);
  f.context.connected = false;
  f.type("Offline edit");
  assert.equal(f.unload().prevented, true);
  const count = f.requests.length;
  f.submit();
  await f.context.reviewMemoryLatest();
  assert.equal(
    f.requests.length,
    count,
    "offline draft editing never writes or reads stale scope",
  );
});

// Before the first authenticated snapshot, HTML itself must be safe while JS
// and the network are still loading. Ready-state renderers enable these later.
test("initial connecting document disables owner actions while keeping Refresh and draft typing available", () => {
  const html = fs.readFileSync(path.join(__dirname, "index.html"), "utf8"),
    tag = (id) =>
      html.match(
        new RegExp(
          '<(?:button|select|input|textarea)\\b[^>]*\\bid="' + id + '"[^>]*>',
        ),
      )?.[0];
  for (const id of [
    "copy-attach",
    "edit-title",
    "focus-terminal",
    "close-agent",
    "pause-agent",
    "save-title",
    "clear-title",
    "review-research",
    "memory-namespace",
    "load-memory",
    "review-memory",
    "adopt-memory-version",
    "graph-edit",
    "connect-pair",
    "isolate-agent",
    "message-kind",
    "send-message",
  ])
    assert.match(
      tag(id),
      /\bdisabled\b/,
      id + " must await a current snapshot",
    );
  assert.match(html, /<button type="submit" disabled>New session<\/button>/);
  assert.match(html, /<button type="submit" disabled>Save entry<\/button>/);
  for (const id of ["refresh", "message", "new-name"])
    assert.doesNotMatch(
      tag(id),
      /\bdisabled\b/,
      id + " remains usable while connecting",
    );
});

for (const interaction of ["interactive", "managed"]) {
  test(
    "revoked board destination stays visible and unsendable for a retained " +
      interaction +
      " draft",
    () => {
      const f = replyContext(interaction);
      f.context.snapshot.boundaries.boards = [
        { name: "pair", read: true, write: true },
      ];
      f.node("message-kind").value = "board:pair";
      f.context.renderComposer();
      f.node("message").value = "Board-only coordination draft";
      f.context.snapshot.boundaries.boards = [];
      f.context.renderComposer();
      assert.equal(f.node("message-kind").value, "board:pair");
      const unavailable = f
        .node("message-kind")
        .options.find((option) => option.value === "board:pair");
      assert.equal(unavailable.disabled, true);
      assert.match(unavailable.textContent, /unavailable/);
      assert.equal(f.node("send-message").disabled, true);
      f.submit();
      assert.equal(
        f.requests.length,
        0,
        "revoked board draft never turns into an inbox/instruction",
      );
      assert.match(f.context.lastNotice, /explicitly choose/);
      assert.equal(f.node("message").value, "Board-only coordination draft");
      f.node("message-kind").value = "direct";
      f.context.renderComposer();
      assert.equal(
        f.node("send-message").disabled,
        false,
        "explicit destination change reenables sending",
      );
    },
  );
}

test("restoring an unavailable stashed board draft never defaults to direct, including read-only access", () => {
  const f = replyContext("managed");
  f.context.messageDrafts.set("b", {
    text: "B private board context",
    kind: "board:restricted",
    reply: "",
  });
  f.context.selectAgent("b");
  f.coherent();
  assert.equal(f.node("message-kind").value, "board:restricted");
  assert.equal(f.context.pendingDraftKind, "");
  assert.equal(f.node("send-message").disabled, true);
  f.submit();
  assert.equal(f.requests.length, 0);
  assert.equal(f.node("message").value, "B private board context");
  f.context.snapshot.boundaries.boards = [
    { name: "restricted", read: true, write: false },
  ];
  f.context.renderComposer();
  assert.equal(f.node("message-kind").value, "board:restricted");
  assert.match(f.node("message-kind").options[1].textContent, /read only/);
  f.submit();
  assert.equal(f.requests.length, 0);
  f.context.snapshot.boundaries.boards[0].write = true;
  f.context.renderComposer();
  assert.equal(
    f.node("send-message").disabled,
    false,
    "restored actual permission enables original destination",
  );
});

test("rapid navigation keeps a pending unavailable board destination in its own session draft", () => {
  const f = replyContext("interactive");
  f.context.messageDrafts.set("b", {
    text: "Restricted board draft",
    kind: "board:restricted",
    reply: "",
  });
  f.context.selectAgent("b");
  assert.equal(f.context.pendingDraftKind, "board:restricted");
  f.context.selectAgent("a");
  assert.equal(f.context.messageDrafts.get("b").kind, "board:restricted");
  f.context.selectAgent("b");
  f.coherent();
  assert.equal(f.node("message-kind").value, "board:restricted");
  f.submit();
  assert.equal(f.requests.length, 0);
});

test("memory browsing follows all broker pages and retains exact namespace and final key", async () => {
  const f = memoryContext(), calls = [];
  f.context.call = async (operation, args) => {
    assert.equal(operation, "memory.list");
    calls.push({ ...args });
    if (!args.after) return { entries: Array.from({length:100}, (_,i)=>({key:`key-${i}`, value:`knowledge ${i}`, version:1})), cursor:"key-99" };
    if (args.after === "key-99") return { entries: [{key:"beyond-first-page",value:"exact scope knowledge",version:7}], cursor:"beyond-first-page" };
    return { entries: [], cursor:args.after };
  };
  await f.context.loadMemory();
  assert.equal(f.context.memoryEntries.get("team").length,101);
  assert.deepEqual(calls.map(c=>c.namespace),["team","team","team"]);
  f.entry("beyond-first-page").click();
  assert.equal(f.node("memory-key").value,"beyond-first-page");
  assert.equal(f.node("memory-value").value,"exact scope knowledge");
  assert.equal(f.context.memoryVersion,7);
});

test("memory pagination discards late pages after namespace changes and never publishes a partial failed refresh", async () => {
  const f = memoryContext();
  f.context.notice = message => { f.context.lastNotice = message; };
  await f.context.loadMemory();
  let release, reached;
  const waiting = new Promise(resolve => { reached = resolve; });
  f.context.call = async (_operation,args) => {
    if (!args.after) return {entries:[{key:"old-first",value:"old",version:1}],cursor:"old-first"};
    return new Promise(resolve=>{release=resolve; reached();});
  };
  const pending=f.context.loadMemory();
  await waiting;
  f.node("memory-namespace").value="pair";
  f.context.resetMemoryNamespace("pair");
  release({entries:[{key:"old-late",value:"wrong namespace",version:1}],cursor:"old-late"});
  await pending;
  assert.equal(f.context.memoryEntries.get("team")[0].key,"plan");
  assert.equal(f.context.memoryEditorNamespace,"pair");
  assert.equal(f.node("memory-list").childElementCount,0);
  f.node("memory-namespace").value="team";
  f.context.resetMemoryNamespace("team");
  f.context.call=async (_operation,args)=>{
    if (!args.after) return {entries:[{key:"partial",value:"not complete",version:1}],cursor:"partial"};
    throw new Error("second page unavailable");
  };
  await f.context.loadMemory();
  assert.equal(f.context.memoryEntries.get("team")[0].key,"plan");
  assert.match(f.context.lastNotice,/second page unavailable/);
});

test("Teams separates retained membership from broker-derived recursive access and updates on polling", () => {
  const f = pollingContext();
  vm.runInContext(source.slice(source.indexOf("function teamJoiningHint("), source.indexOf("function renderWorkerRuntime(")), f.context);
  const team = {
    id: "team-recursive", name: "Recursive collaboration", owner: "$owner",
    state: "active", version: 4, members: ["b"], invited: ["c"],
    board: "board-exact", memory: "memory-exact",
    member_access: { b: { allowed: false, reason: "ancestor is not a team member", blocked_by: "stable-ancestor-not-in-roster" } },
  };
  f.context.snapshot.teams = [team];
  const text = () => {
    const all = node => [node.textContent, ...node.children.flatMap(child => all(child))];
    return all(f.node("consented-teams")).join("\n");
  };
  f.context.renderTeams();
  assert.match(text(), /Membership retained · effective access blocked/);
  assert.match(text(), /ancestor is not a team member/);
  assert.match(text(), /Blocked by stable-ancestor-not-in-roster/);
  assert.doesNotMatch(text(), /Current board \+ memory access allowed/);
  // A metadata-only poll must refresh this status even with unchanged team
  // version, and must not send an owner command or need parent-name guessing.
  team.member_access.b = { allowed: true };
  f.context.renderTeams();
  assert.match(text(), /Current board \+ memory access allowed/);
  assert.doesNotMatch(text(), /effective access blocked/);
  delete team.member_access;
  f.context.renderTeams();
  assert.match(text(), /Membership recorded · effective access unavailable/);
  assert.doesNotMatch(text(), /access allowed/);
  assert.equal(f.writes.length, 0);
});

function rosterAccessibilityContext() {
  const dom = domFixture(),
    context = identityContext();
  Object.assign(context, {
    document: dom.document,
    $: dom.node,
    el: dom.el,
    selected: "alpha",
    agentSignature: "",
    researchSavedOffline: false,
    renderObserved() {},
    selectAgent() {},
    snapshot: {
      agents: [
        { id: "alpha", config: { harness: "claude", workdir: "/work/project" } },
        { id: "beta", title: "Investigate auth", config: { harness: "claude", workdir: "/work/project" } },
      ],
      runtime: { agents: {} },
    },
  });
  vm.runInContext(
    source.slice(source.indexOf("function captureViewState("), source.indexOf("const notice =")),
    context,
  );
  vm.runInContext(
    source.slice(source.indexOf('$("session-search").oninput'), source.indexOf('$("edit-title").onclick')),
    context,
  );
  return { ...dom, context };
}

test("session search announces results and clear recovery while polling stays quiet", () => {
  const f = rosterAccessibilityContext(),
    status = f.node("session-search-status");
  let text = "", writes = 0;
  Object.defineProperty(status, "textContent", {
    get: () => text,
    set: (value) => { text = value; writes++; },
  });
  f.context.renderSessionLists();
  assert.equal(writes, 0, "initial polling must not announce the roster");
  f.node("session-search").value = "nomatch";
  f.node("session-search").focus();
  f.node("session-search").oninput();
  assert.equal(text, "0 of 2 sessions match “nomatch”.");
  assert.equal(f.document.activeElement, f.node("session-search"));
  f.context.renderSessionLists();
  f.context.snapshot.agents[0].title = "Changed asynchronously";
  f.context.renderSessionLists();
  assert.equal(writes, 1, "polling and unrelated title changes must stay quiet");
  const clear = f.node("agents").querySelectorAll("button")[0];
  clear.focus();
  clear.click();
  assert.equal(text, "2 sessions shown.");
  assert.equal(f.context.sessionQuery, "");
  assert.equal(f.document.activeElement, f.node("session-search"), "removed Clear search must return focus to search");
  f.context.snapshot.agents = [];
  f.context.researchSavedOffline = true;
  f.node("session-search").value = "alpha";
  f.node("session-search").oninput();
  assert.equal(text, "Session registry not loaded. Search results are unavailable.");
});

test("session identity keeps stable IDs without repeating untitled project/provider rows", () => {
  const f = rosterAccessibilityContext();
  f.context.renderSessionLists();
  const rows = f.node("agents").children,
    plain = rows[0].children[1],
    titled = rows[1].children[1];
  assert.equal(plain.children[0].textContent, "project · Claude");
  assert.equal(plain.children[1].textContent, "alpha");
  assert.equal(plain.children.length, 2, "fallback title already contains the project and provider");
  assert.equal(rows[0].getAttribute("aria-label"), "project · Claude · alpha");
  assert.equal(titled.children[0].textContent, "Investigate auth");
  assert.equal(titled.children[1].textContent, "beta · Claude");
  assert.equal(titled.children[2].textContent, "project");
  assert.equal(rows[0].getAttribute("aria-pressed"), "true");
  f.context.snapshot.agents[1].config.workdir = "/tmp/beta";
  f.context.renderSessionLists();
  assert.equal(f.node("agents").children[1].children[1].children.length, 2, "project equal to stable ID adds no identifying information");
});

test("workspace view buttons expose one active state without moving activation focus", () => {
  const dom = domFixture(), names = ["activity", "agent", "tasks"],
    buttons = names.map((name) => {
      const button = dom.el("button");
      button.dataset.tab = name;
      dom.document.body.append(button);
      const view = dom.node("view-" + name, "view");
      return { button, view };
    }),
    context = vm.createContext({
      document: dom.document, $: dom.node, tab: "activity", renderGlobalHeading() {},
    });
  vm.runInContext(source.slice(source.indexOf("function activateTab("), source.indexOf('$("board-filter").onchange')), context);
  buttons[1].button.focus();
  buttons[1].button.click();
  assert.deepEqual(buttons.map(({ button }) => button.getAttribute("aria-pressed")), ["false", "true", "false"]);
  assert.equal(dom.document.activeElement, buttons[1].button);
  buttons[2].button.click();
  assert.deepEqual(buttons.map(({ button }) => button.getAttribute("aria-pressed")), ["false", "false", "true"]);
  assert.ok(buttons[2].view.className.includes("active"));
  assert.ok(!buttons[1].view.className.split(/\s+/).includes("active"));
  const html = fs.readFileSync(path.join(__dirname, "index.html"), "utf8");
  const initial = [...html.matchAll(/<button\b[^>]*data-tab="([^"]+)"[^>]*>/g)];
  assert.equal(initial.filter(([markup]) => markup.includes('aria-pressed="true"')).length, 1);
  for (const [markup, name] of initial)
    assert.ok(markup.includes('aria-controls="view-' + name + '"'), name);
  assert.match(html, /id="session-search-status"[^>]*role="status"[^>]*aria-atomic="true"/);
});

test("recent task view describes its bounded window and avoids implying empty history", () => {
  const f = pollingContext();
  f.context.renderTasks();
  assert.match(f.node("tasks").children[0].textContent, /^No recent tasks\./);
  const html = fs.readFileSync(path.join(__dirname, "index.html"), "utf8");
  assert.match(html, /Recent tasks <span id="task-count">/);
  assert.match(html, /latest 40 tasks.*older tasks may exist/);
});

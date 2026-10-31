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
    source.slice(source.indexOf('$("title-editor").onsubmit =')),
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

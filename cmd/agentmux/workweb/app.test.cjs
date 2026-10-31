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

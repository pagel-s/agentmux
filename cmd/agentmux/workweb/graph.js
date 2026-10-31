"use strict";
let graphMode = "agents",
  graphVersion = "",
  graphLoading = false,
  graphCache = new Map(),
  graphSelection = null,
  graphPositions = {},
  graphDrag = null,
  graphLayoutSession = "",
  graphDrawSignature = "";
let graphWidth = 960,
  graphHeight = 580;
const svgNS = "http://www.w3.org/2000/svg";
function svgNode(tag, attrs = {}, text) {
  const node = document.createElementNS(svgNS, tag);
  for (const [key, value] of Object.entries(attrs))
    node.setAttribute(key, String(value));
  if (text !== undefined) node.textContent = text;
  return node;
}
function graphPosition(key, fallback) {
  return graphPositions[key] || fallback;
}
function graphPoint(event) {
  const point = $("interaction-graph").createSVGPoint();
  point.x = event.clientX;
  point.y = event.clientY;
  return point.matrixTransform($("interaction-graph").getScreenCTM().inverse());
}
function graphPick(source, target, kind = "edge", namespace = "") {
  selectAgent(source);
  graphSelection = { source, target, kind, namespace };
  peer = target;
  if (selectionReady()) {
    renderBoundaries();
    graphInspector();
  }
  drawGraph();
}
function graphInspector() {
  const panel = $("graph-inspector");
  panel.classList.toggle("hidden", !graphSelection);
  if (!graphSelection) return;
  if (
    graphSelection.source !== selected ||
    snapshot.boundaries.agent !== selected
  ) {
    $("edge-section").classList.add("hidden");
    $("scope-section").classList.add("hidden");
    return;
  }
  const edge = graphSelection.kind === "edge";
  $("inspector-title").textContent =
    selected + " → " + (edge ? peer : scopeLabel(graphSelection.namespace));
  $("edge-section").classList.toggle("hidden", !edge);
  $("scope-section").classList.toggle("hidden", edge);
  for (const row of $("scope-controls").children)
    row.classList.toggle(
      "hidden",
      !edge &&
        row.dataset.scope !==
          graphSelection.kind + ":" + graphSelection.namespace,
    );
}
async function renderGraph() {
  if (!snapshot || tab !== "graph") return;
  if (graphLayoutSession !== snapshot.runtime.session) {
    graphLayoutSession = snapshot.runtime.session;
    graphPositions = {};
    try {
      const saved = JSON.parse(
        localStorage.getItem("agentmux-layout-" + graphLayoutSession) || "{}",
      );
      for (const [key, p] of Object.entries(saved))
        if (p && Number.isFinite(p.x) && Number.isFinite(p.y))
          graphPositions[key] = {
            x: Math.max(80, Math.min(880, p.x)),
            y: Math.max(50, Math.min(10000, p.y)),
          };
    } catch {}
  }
  const signature = JSON.stringify([
    snapshot.boundaries.version,
    (snapshot.agents || [])
      .filter((a) => a.state === "active")
      .map((a) => a.id),
  ]);
  if (graphVersion !== signature && !graphLoading) {
    graphLoading = true;
    const version = snapshot.boundaries.version,
      agents = (snapshot.agents || [])
        .filter((a) => a.state === "active")
        .slice(0, 40);
    try {
      const values = await Promise.all(
        agents.map((a) => call("boundaries.get", { agent: a.id })),
      );
      if (values.every((v) => v.version === version)) {
        graphCache = new Map(values.map((v) => [v.agent, v]));
        graphVersion = signature;
      } else notice("Permissions changed while loading the graph. Refreshing…");
    } catch (error) {
      notice(error.message, true);
    } finally {
      graphLoading = false;
    }
  }
  drawGraph();
  graphInspector();
}
function drawGraph() {
  if (!snapshot || tab !== "graph") return;
  const signature = JSON.stringify([
    graphVersion,
    graphMode,
    selected,
    graphPositions,
    snapshot.boundaries,
  ]);
  if (signature === graphDrawSignature) return;
  graphDrawSignature = signature;
  const svg = $("interaction-graph"),
    agents = (snapshot.agents || [])
      .filter((a) => a.state === "active")
      .slice(0, 40),
    nodes = [],
    edges = [];
  const scopeCount =
    (snapshot.boundaries.boards || []).length +
    (snapshot.boundaries.memory || []).length;
  graphHeight =
    graphMode === "scopes"
      ? Math.max(
          580,
          95 + Math.max(0, agents.length - 1) * 100 + 80,
          140 + Math.max(0, scopeCount - 1) * 130 + 80,
        )
      : 580;
  for (const position of Object.values(graphPositions))
    graphHeight = Math.max(graphHeight, position.y + 60);
  svg.setAttribute("viewBox", `0 0 ${graphWidth} ${graphHeight}`);
  svg.setAttribute("preserveAspectRatio", "xMidYMin meet");
  svg.style.height = graphHeight > 580 ? graphHeight + "px" : "100%";
  const defs = svgNode("defs"),
    pattern = svgNode("pattern", {
      id: "grid",
      width: 24,
      height: 24,
      patternUnits: "userSpaceOnUse",
    });
  pattern.append(svgNode("circle", { cx: 1, cy: 1, r: 0.7, fill: "#2e243c" }));
  defs.append(pattern);
  const marker = svgNode("marker", {
    id: "arrow",
    viewBox: "0 0 10 10",
    refX: 9,
    refY: 5,
    markerWidth: 6,
    markerHeight: 6,
    orient: "auto-start-reverse",
  });
  marker.append(
    svgNode("path", { d: "M 0 0 L 10 5 L 0 10 z", fill: "#a98cc9" }),
  );
  defs.append(marker);
  svg.replaceChildren(
    defs,
    svgNode("rect", {
      width: graphWidth,
      height: graphHeight,
      fill: "url(#grid)",
    }),
  );
  for (let i = 0; i < agents.length; i++) {
    const a = agents[i],
      angle = (2 * Math.PI * i) / Math.max(agents.length, 1) - Math.PI / 2;
    const fallback =
      graphMode === "agents"
        ? { x: 480 + 300 * Math.cos(angle), y: 290 + 200 * Math.sin(angle) }
        : { x: 190, y: 95 + i * 100 };
    const key = "agent:" + a.id,
      p = graphPosition(key, fallback);
    nodes.push({
      key,
      label: a.id,
      kind: a.external ? "external bridge" : a.config.interaction || "managed",
      source: a.id,
      ...p,
    });
  }
  if (graphMode === "agents") {
    for (const a of agents)
      for (const target of agents) {
        if (a.id === target.id) continue;
        const actions =
          graphCache.get(a.id)?.targets?.find((t) => t.to === target.id)
            ?.actions || [];
        if (
          actions.length ||
          (graphSelection?.kind === "edge" &&
            graphSelection.source === a.id &&
            graphSelection.target === target.id)
        )
          edges.push({
            source: "agent:" + a.id,
            target: "agent:" + target.id,
            actor: a.id,
            recipient: target.id,
            kind: "edge",
            label:
              actions
                .map(
                  (v) =>
                    ({
                      "message.send": "message",
                      "ping.send": "ping",
                      instruct: "command",
                    })[v],
                )
                .join(" · ") || "denied",
            enabled: actions.length > 0,
          });
      }
  } else {
    const b = snapshot.boundaries,
      scopes = [
        ...(b.boards || []).map((s) => ({ ...s, kind: "board" })),
        ...(b.memory || []).map((s) => ({ ...s, kind: "memory" })),
      ];
    for (let i = 0; i < scopes.length; i++) {
      const scope = scopes[i],
        key = scope.kind + ":" + scope.name,
        p = graphPosition(key, { x: 710, y: 140 + i * 130 });
      nodes.push({
        key,
        label: scopeLabel(scope.name),
        kind: scope.kind,
        scope: scope.name,
        ...p,
      });
      for (const a of agents) {
        const value = (
          graphCache.get(a.id)?.[
            scope.kind === "board" ? "boards" : "memory"
          ] || []
        ).find((v) => v.name === scope.name);
        if (value?.read || value?.write || a.id === selected)
          edges.push({
            source: "agent:" + a.id,
            target: key,
            actor: a.id,
            kind: scope.kind,
            namespace: scope.name,
            label:
              [value?.read ? "read" : "", value?.write ? "write" : ""]
                .filter(Boolean)
                .join(" · ") || "denied",
            enabled: !!(value?.read || value?.write),
          });
      }
    }
  }
  const positions = new Map(nodes.map((n) => [n.key, n]));
  for (const edge of edges) {
    const from = positions.get(edge.source),
      to = positions.get(edge.target);
    if (!from || !to) continue;
    const dx = to.x - from.x,
      dy = to.y - from.y,
      len = Math.max(1, Math.hypot(dx, dy)),
      ux = dx / len,
      uy = dy / len;
    const x1 = from.x + ux * 72,
      y1 = from.y + uy * 28,
      x2 = to.x - ux * 74,
      y2 = to.y - uy * 29;
    const bend = edge.kind === "edge" ? 32 : 12,
      cx = (x1 + x2) / 2 - uy * bend,
      cy = (y1 + y2) / 2 + ux * bend;
    const d = `M ${x1} ${y1} Q ${cx} ${cy} ${x2} ${y2}`;
    const line = svgNode("path", {
      d,
      class:
        "graph-edge" +
        (edge.actor === selected ? " selected" : "") +
        (!edge.enabled ? " denied" : ""),
      "marker-end": "url(#arrow)",
    });
    const hit = svgNode("path", {
      d,
      class: "edge-hit",
      tabindex: 0,
      role: "button",
      "aria-label":
        edge.actor +
        " to " +
        (edge.recipient || edge.namespace) +
        ": " +
        edge.label,
    });
    const choose = () =>
      graphPick(
        edge.actor,
        edge.recipient || "",
        edge.kind,
        edge.namespace || "",
      );
    hit.onclick = choose;
    hit.onkeydown = (e) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        choose();
      }
    };
    svg.append(line, hit);
    if (edge.actor === selected) {
      const label = svgNode(
        "text",
        { x: cx, y: cy - 7, class: "edge-label" },
        edge.label,
      );
      label.onclick = choose;
      svg.append(label);
    }
  }
  for (const node of nodes) {
    const group = svgNode("g", {
      class:
        "graph-node" +
        (node.source === selected ? " selected" : "") +
        (node.scope ? " scope" : ""),
      transform: `translate(${node.x},${node.y})`,
      "data-node": node.key,
      tabindex: 0,
      role: "button",
      "aria-label": node.label + " " + node.kind,
    });
    group.append(
      svgNode("rect", { x: -72, y: -28, width: 144, height: 56 }),
      svgNode("text", { y: -2 }, node.label),
      svgNode("text", { y: 16, class: "node-kind" }, node.kind),
    );
    group.onkeydown = (e) => {
      if (["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"].includes(e.key)) {
        e.preventDefault();
        const p = { x: node.x, y: node.y };
        p.x += e.key === "ArrowRight" ? 12 : e.key === "ArrowLeft" ? -12 : 0;
        p.y += e.key === "ArrowDown" ? 12 : e.key === "ArrowUp" ? -12 : 0;
        p.x = Math.max(80, Math.min(graphWidth - 80, p.x));
        p.y = Math.max(50, Math.min(graphHeight - 50, p.y));
        graphPositions[node.key] = p;
        saveGraphLayout();
        drawGraph();
        [...svg.querySelectorAll("[data-node]")]
          .find((n) => n.getAttribute("data-node") === node.key)
          ?.focus();
      } else if (e.key === "Enter") {
        if (node.source) {
          graphSelection = null;
          selectAgent(node.source);
          graphInspector();
        } else graphPick(selected, "", node.kind, node.scope);
      }
    };
    svg.append(group);
  }
  $("graph-limit").textContent =
    (snapshot.agents || []).filter((a) => a.state === "active").length > 40
      ? "First 40 active agents shown"
      : "Drag nodes · select a connection to edit";
  $("graph-agents").classList.toggle("active", graphMode === "agents");
  $("graph-scopes").classList.toggle("active", graphMode === "scopes");
}
function saveGraphLayout() {
  try {
    localStorage.setItem(
      "agentmux-layout-" + snapshot.runtime.session,
      JSON.stringify(graphPositions),
    );
  } catch {}
}
const graph = $("interaction-graph");
graph.addEventListener("pointerdown", (event) => {
  const node = event.target.closest("[data-node]");
  if (!node) return;
  const key = node.getAttribute("data-node"),
    transform = node.transform.baseVal.consolidate().matrix;
  graphDrag = {
    key,
    start: graphPoint(event),
    origin: { x: transform.e, y: transform.f },
    moved: false,
  };
  graph.setPointerCapture(event.pointerId);
});
graph.addEventListener("pointermove", (event) => {
  if (!graphDrag) return;
  const p = graphPoint(event),
    dx = p.x - graphDrag.start.x,
    dy = p.y - graphDrag.start.y;
  if (Math.hypot(dx, dy) > 3) graphDrag.moved = true;
  if (!graphDrag.moved) return;
  graphPositions[graphDrag.key] = {
    x: Math.max(80, Math.min(graphWidth - 80, graphDrag.origin.x + dx)),
    y: Math.max(50, Math.min(graphHeight - 50, graphDrag.origin.y + dy)),
  };
  drawGraph();
});
graph.addEventListener("pointerup", (event) => {
  if (!graphDrag) return;
  const drag = graphDrag;
  graphDrag = null;
  graph.releasePointerCapture(event.pointerId);
  if (drag.moved) {
    saveGraphLayout();
    return;
  }
  if (drag.key.startsWith("agent:")) {
    graphSelection = null;
    selectAgent(drag.key.slice(6));
  } else {
    const [kind, ...name] = drag.key.split(":");
    graphPick(selected, "", kind, name.join(":"));
  }
  drawGraph();
  graphInspector();
});
graph.addEventListener("pointercancel", () => {
  graphDrag = null;
});
$("graph-agents").onclick = () => {
  graphMode = "agents";
  graphSelection = null;
  renderGraph();
};
$("graph-scopes").onclick = () => {
  graphMode = "scopes";
  graphSelection = null;
  renderGraph();
};
$("close-inspector").onclick = () => {
  graphSelection = null;
  graphInspector();
};
$("graph-reset").onclick = () => {
  graphPositions = {};
  saveGraphLayout();
  drawGraph();
};

$("graph-edit").onclick = () => {
  const other = (snapshot?.agents || []).find(
    (a) => a.state === "active" && a.id !== selected,
  );
  if (!other) {
    notice("Add or connect another session first.");
    return;
  }
  graphMode = "agents";
  graphPick(
    selected,
    (snapshot.agents || []).some(
      (a) => a.id === peer && a.id !== selected && a.state === "active",
    )
      ? peer
      : other.id,
  );
  graphInspector();
};

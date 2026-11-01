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
let graphViewport = null, graphFit = true, graphListView = false, graphOriginKey = "", connectionQuery = "";
const connectionPageSize=50;
let connectionPage=0, connectionListSignature="", connectionDataSignature="", connectionRows=[], connectionCacheRevision=0, connectionPageContext="";
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
function graphAgents() {
  const active = (snapshot?.agents || []).filter(
      (agent) => agent.state === "active",
    ),
    priority = new Set([selected, peer]),
    required = [...priority]
      .map((id) => active.find((agent) => agent.id === id))
      .filter(Boolean),
    rest = active
      .filter((agent) => !priority.has(agent.id))
      .slice(0, 40 - required.length),
    shown = new Set([...required, ...rest].map((agent) => agent.id));
  return active.length > 12
    ? [...required, ...rest]
    : active.filter((agent) => shown.has(agent.id));
}
function graphDefaultPosition(index, count, mode) {
  if (mode !== "agents") return { x: 190, y: 95 + index * 100 };
  if (count > 12)
    return { x: 120 + (index % 5) * 180, y: 86 + Math.floor(index / 5) * 104 };
  const angle = (2 * Math.PI * index) / Math.max(count, 1) - Math.PI / 2;
  return { x: 480 + 300 * Math.cos(angle), y: 290 + 200 * Math.sin(angle) };
}
function graphPoint(event, matrix) {
  const point = $("interaction-graph").createSVGPoint();
  point.x = event.clientX;
  point.y = event.clientY;
  return point.matrixTransform(matrix || $("interaction-graph").getScreenCTM().inverse());
}
function applyGraphViewport() {
  if (graphFit || !graphViewport) graphViewport = {x:0,y:0,width:graphWidth,height:graphHeight};
  $("interaction-graph").setAttribute("viewBox", `${graphViewport.x} ${graphViewport.y} ${graphViewport.width} ${graphViewport.height}`);
  $("graph-zoom").textContent = Math.round(graphWidth / graphViewport.width * 100) + "%";
}
function zoomGraph(factor) {
  if (!graphViewport) applyGraphViewport();
  const current = graphWidth / graphViewport.width, scale = Math.max(.25,Math.min(4,current*factor)), ratio = current/scale;
  const width = graphViewport.width*ratio, height=graphViewport.height*ratio;
  graphViewport = {x:graphViewport.x+(graphViewport.width-width)/2,y:graphViewport.y+(graphViewport.height-height)/2,width,height};
  graphFit = false; applyGraphViewport();
}
function fitGraph() { graphFit = true; applyGraphViewport(); }
function graphPick(source, target, kind = "edge", namespace = "") {
  graphOriginKey=document.activeElement?.dataset?.focusKey || graphOriginKey;
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
    $("inspector-title").textContent =
      "Loading " + sessionReference(graphSelection.source) + " boundaries…";
    $("edge-section").classList.add("hidden");
    $("scope-section").classList.add("hidden");
    return;
  }
  const edge = graphSelection.kind === "edge";
  $("inspector-title").textContent =
    sessionReference(selected) +
    " → " +
    (edge ? sessionReference(peer) : scopeLabel(graphSelection.namespace));
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
    graphFit = true;
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
    snapshot.teams || [],
    graphAgents().map((a) => a.id),
  ]);
  if (graphVersion !== signature && !graphLoading) {
    graphLoading = true;
    const version = snapshot.boundaries.version,
      agents = graphAgents();
    try {
      const values = await Promise.all(
        agents.map((a) => call("boundaries.get", { agent: a.id })),
      );
      const current = JSON.stringify([
        snapshot.boundaries.version,
        snapshot.teams || [],
        graphAgents().map((a) => a.id),
      ]);
      if (current === signature && values.every((v) => v.version === version)) {
        graphCache = new Map(values.map((v) => [v.agent, v]));
        graphVersion = signature;
        connectionCacheRevision++;
      } else notice("Permissions changed while loading the graph. Refreshing…");
    } catch (error) {
      notice(error.message, true);
    } finally {
      graphLoading = false;
    }
  }
  drawGraph();
  renderConnectionList();
  graphInspector();
}
function drawGraph() {
  if (!snapshot || tab !== "graph") return;
  const signature = JSON.stringify([
    graphVersion,
    graphMode,
    selected,
    peer,
    graphSelection,
    graphPositions,
    snapshot.boundaries,
    snapshot.teams || [],
    (snapshot.agents || []).map((a) => [a.id, a.title, a.config.harness]),
  ]);
  if (signature === graphDrawSignature) return;
  graphDrawSignature = signature;
  const svg = $("interaction-graph"),
    agents = graphAgents(),
    nodes = [],
    edges = [];
  const viewState = captureViewState(svg);
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
      : Math.max(
          580,
          agents.length > 12
            ? 146 + (Math.ceil(agents.length / 5) - 1) * 104
            : 580,
        );
  for (const position of Object.values(graphPositions))
    graphHeight = Math.max(graphHeight, position.y + 60);
  applyGraphViewport();
  svg.setAttribute("preserveAspectRatio", "xMidYMid meet");
  svg.style.height = "100%";
  svg.style.minWidth = "0";
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
      fallback = graphDefaultPosition(i, agents.length, graphMode);
    const key = "agent:" + a.id,
      p = graphPosition(key, fallback);
    nodes.push({
      key,
      label: sessionTitle(a),
      kind: a.id + " · " + providerName(a.config.harness),
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
      tabindex: -1,
      role: "button",
      "data-focus-key": JSON.stringify([
        "edge",
        edge.actor,
        edge.recipient,
        edge.kind,
        edge.namespace,
      ]),
      "data-focus-x": cx,
      "data-focus-y": cy,
      "aria-label":
        sessionReference(edge.actor) +
        " to " +
        (edge.recipient ? sessionReference(edge.recipient) : edge.namespace) +
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
        graphInspector();
        $("close-inspector").focus();
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
      "data-focus-x": node.x,
      "data-focus-y": node.y,
      "data-focus-key": "node:" + node.key,
      "data-item-key": node.key,
      tabindex: -1,
      role: "button",
      "aria-label":
        node.label + " " + node.kind + (node.scope ? " · " + node.scope : ""),
    });
    const shorten = (text, max) =>
      Array.from(text).length > max
        ? Array.from(text)
            .slice(0, max - 1)
            .join("") + "…"
        : text;
    group.append(
      svgNode(
        "title",
        {},
        node.label + " · " + node.kind + (node.scope ? " · " + node.scope : ""),
      ),
      svgNode("rect", { x: -72, y: -28, width: 144, height: 56 }),
      svgNode("text", { y: -2 }, shorten(node.label, 20)),
      svgNode("text", { y: 16, class: "node-kind" }, shorten(node.kind, 26)),
    );
    group.onkeydown = (e) => {
      if (["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"].includes(e.key) && e.shiftKey) {
        e.preventDefault();
        const p = { x: node.x, y: node.y };
        p.x += e.key === "ArrowRight" ? 12 : e.key === "ArrowLeft" ? -12 : 0;
        p.y += e.key === "ArrowDown" ? 12 : e.key === "ArrowUp" ? -12 : 0;
        p.x = Math.max(80, Math.min(graphWidth - 80, p.x));
        p.y = Math.max(50, Math.min(graphHeight - 50, p.y));
        graphPositions[node.key] = p;
        saveGraphLayout();
        drawGraph();
      } else if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        if (node.source) {
          graphSelection = null;
          selectAgent(node.source);
          graphInspector();
        } else {
          graphPick(selected, "", node.kind, node.scope);
          graphInspector();
          $("close-inspector").focus();
        }
      }
    };
    svg.append(group);
  }
  $("graph-limit").textContent =
    (snapshot.agents || []).filter((a) => a.state === "active").length > 40
      ? "40 active sessions shown, including selected session and recipient"
      : "Drag nodes · drag background to pan · +/− zoom · 0 fits";
  $("graph-agents").classList.toggle("active", graphMode === "agents");
  $("graph-scopes").classList.toggle("active", graphMode === "scopes");
  restoreViewState(svg, viewState);
  renderConnectionList();
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
  if (!node) {
    if (event.target.closest(".edge-hit,.edge-label")) return;
    const matrix = graph.getScreenCTM().inverse();
    graphDrag = {pan:true,matrix,start:graphPoint(event,matrix),origin:{x:graphViewport?.x||0,y:graphViewport?.y||0},moved:false};
    graph.setPointerCapture(event.pointerId);
    return;
  }
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
  const p = graphPoint(event,graphDrag.matrix),
    dx = p.x - graphDrag.start.x,
    dy = p.y - graphDrag.start.y;
  if (Math.hypot(dx, dy) > 3) graphDrag.moved = true;
  if (!graphDrag.moved) return;
  if (graphDrag.pan) {
    graphFit = false;
    graphViewport.x = graphDrag.origin.x-dx;
    graphViewport.y = graphDrag.origin.y-dy;
    applyGraphViewport();
    return;
  }
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
    if (!drag.pan) saveGraphLayout();
    return;
  }
  if (drag.pan) return;
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
  graphSelection = null; graphInspector();
  if(graphListView)renderConnectionList(graphOriginKey);
  const roots=graphListView?[$("connection-list")]:[graph];
  const origin=roots.flatMap(root=>[...root.querySelectorAll("[data-focus-key]")]).find(node=>node.dataset.focusKey===graphOriginKey);
  revealConnectionControl(origin || (graphListView?$("connection-list"):graph),graphListView?$("connection-list"):graph);
};
$("graph-inspector").onkeydown = (event) => {
  if (event.key === "Escape") {
    event.preventDefault();
    $("close-inspector").click();
  }
};
$("graph-reset").onclick = () => {
  graphPositions = {};
  saveGraphLayout();
  drawGraph();
  fitGraph();
};
$("graph-fit").onclick = fitGraph;
$("graph-zoom-in").onclick = () => zoomGraph(1.25);
$("graph-zoom-out").onclick = () => zoomGraph(.8);
graph.addEventListener("keydown", event => {
  if (event.target !== graph) {
    if(["ArrowUp","ArrowDown","ArrowLeft","ArrowRight","Home","End"].includes(event.key)&&!event.shiftKey){event.preventDefault();moveConnectionFocus(graph,event);}
    return;
  }
  if (["ArrowUp","ArrowDown"].includes(event.key) && !event.shiftKey) {event.preventDefault();moveConnectionFocus(graph,event);return;}
  if (event.key === "+" || event.key === "=") { event.preventDefault(); zoomGraph(1.25); }
  else if (event.key === "-") { event.preventDefault(); zoomGraph(.8); }
  else if (event.key === "0" || event.key === "Home") { event.preventDefault(); fitGraph(); }
  else if (["ArrowUp","ArrowDown","ArrowLeft","ArrowRight"].includes(event.key)) {
    event.preventDefault(); if (!graphViewport) applyGraphViewport(); graphFit = false;
    const step = graphViewport.width*.08;
    graphViewport.x += event.key === "ArrowRight" ? step : event.key === "ArrowLeft" ? -step : 0;
    graphViewport.y += event.key === "ArrowDown" ? step : event.key === "ArrowUp" ? -step : 0;
    applyGraphViewport();
  }
});

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

function revealConnectionControl(node,root) {
  if(!node)return;
  if(root===graph && !graphFit && graphViewport){
    const x=Number(node.dataset.focusX),y=Number(node.dataset.focusY),pad=Math.min(80,graphViewport.width*.15);
    if(Number.isFinite(x)&&Number.isFinite(y)&&(x<graphViewport.x+pad||x>graphViewport.x+graphViewport.width-pad||y<graphViewport.y+pad||y>graphViewport.y+graphViewport.height-pad)){
      graphViewport.x=x-graphViewport.width/2;graphViewport.y=y-graphViewport.height/2;applyGraphViewport();
    }
  }
  node.focus({preventScroll:true});node.scrollIntoView?.({block:"nearest",inline:"nearest"});
}
function moveConnectionFocus(root,event) {
  const items=[...root.querySelectorAll("[data-focus-key]")],index=items.indexOf(document.activeElement),back=event.key==="ArrowUp"||event.key==="ArrowLeft";
  const next=event.key==="Home"?0:event.key==="End"?items.length-1:index<0?(back?items.length-1:0):index+(back?-1:1);
  revealConnectionControl(items[(next+items.length)%items.length],root);
}
function connectionSessionText(agent) {return [sessionReference(agent.id),agent.config?.harness,agent.config?.workdir,agent.harness,agent.project].filter(Boolean).join(" ").toLowerCase();}
function connectionListData() {
  const active=(snapshot.agents||[]).filter(agent=>agent.state==="active"),byID=new Map(active.map(agent=>[agent.id,agent]));
  const sources=[...(byID.has(selected)?[byID.get(selected)]:[]),...active.filter(agent=>agent.id!==selected)];
  const rows=[];let loaded=0,unloaded=0;
  for(const agent of sources){
    const b=agent.id===selected && snapshot.boundaries.agent===selected?snapshot.boundaries:graphCache.get(agent.id),sourceText=connectionSessionText(agent);
    if(!b){unloaded++;if(!connectionQuery||sourceText.includes(connectionQuery))rows.push({key:"list-session:"+agent.id,label:sessionReference(agent.id)+" · permissions not loaded; select to inspect this session",source:agent.id,unloaded:true});continue;}
    loaded++;
    if(graphMode==="agents"){
      const targets=new Map((b.targets||[]).map(target=>[target.to,target]));
      for(const recipient of active){
        if(recipient.id===agent.id)continue;
        const actions=targets.get(recipient.id)?.actions||[],description=actions.length?actions.map(action=>({"message.send":"messages","ping.send":"pings",instruct:"instructions"})[action]||action).join(" · "):"No directional permissions";
        const label=sessionReference(agent.id)+" → "+sessionReference(recipient.id)+" · "+description;
        if(connectionQuery && ![label.toLowerCase(),sourceText,connectionSessionText(recipient)].some(text=>text.includes(connectionQuery)))continue;
        rows.push({key:JSON.stringify(["list",agent.id,recipient.id]),label,source:agent.id,target:recipient.id,kind:"edge"});
      }
    }else for(const [kind,scopes] of [["board",b.boards||[]],["memory",b.memory||[]]])for(const scope of scopes){
      const label=sessionReference(agent.id)+" → "+scopeLabel(scope.name)+" · "+kind+" · "+(scope.read?"read ":"")+(scope.write?"write":"")+(!scope.read&&!scope.write?"denied":"");
      if(connectionQuery&&!label.toLowerCase().includes(connectionQuery)&&!sourceText.includes(connectionQuery))continue;
      rows.push({key:JSON.stringify(["list",agent.id,kind,scope.name]),label,source:agent.id,target:"",kind,namespace:scope.name});
    }
  }
  return {rows,loaded,unloaded,total:active.length};
}
function renderConnectionList(originKey="") {
  if(!snapshot || tab!=="graph" || !graphListView)return;
  const context=JSON.stringify([selected,graphMode,connectionQuery]),contextChanged=context!==connectionPageContext;if(contextChanged){connectionPage=0;connectionPageContext=context;}
  const dataSignature=JSON.stringify([snapshotIdentity(),graphVersion,connectionCacheRevision,graphMode,selected,connectionQuery,snapshot.boundaries.agent,snapshot.boundaries.version,(snapshot.agents||[]).map(agent=>[agent.id,agent.state,agent.title,agent.config?.harness,agent.config?.workdir])]);
  let counts;
  if(dataSignature!==connectionDataSignature){counts=connectionListData();connectionRows=counts.rows;connectionDataSignature=dataSignature;renderConnectionList.counts=counts;}
  counts=counts||renderConnectionList.counts;
  if(originKey){const index=connectionRows.findIndex(row=>row.key===originKey);if(index>=0)connectionPage=Math.floor(index/connectionPageSize);}
  connectionPage=Math.max(0,Math.min(connectionPage,Math.max(0,Math.ceil(connectionRows.length/connectionPageSize)-1)));
  const signature=JSON.stringify([dataSignature,connectionPage]);if(signature===connectionListSignature)return;connectionListSignature=signature;
  const list=$("connection-list"),state=captureViewState(list),start=connectionPage*connectionPageSize,page=connectionRows.slice(start,start+connectionPageSize);
  $("connection-list-status").textContent=(connectionRows.length?"Showing "+(start+1)+"–"+(start+page.length)+" of "+connectionRows.length+" matching entries":"No matching loaded connections or session names")+" · "+counts.loaded+" / "+counts.total+" session policies loaded"+(counts.unloaded?". "+counts.unloaded+" policies remain uninspected; permission searches cannot establish their access. Search a session name or ID, then select it to load its policy.":". Includes denied directional pairs and shared access.");
  $("connection-list-previous").disabled=connectionPage===0;$("connection-list-next").disabled=start+page.length>=connectionRows.length;
  const nodes=page.map(row=>{const button=el("button","connection-list-row",row.label);button.type="button";button.tabIndex=-1;button.setAttribute("role","option");button.dataset.focusKey=row.key;button.dataset.itemKey=row.key;button.onclick=()=>{graphOriginKey=row.key;if(row.unloaded){selectAgent(row.source);return;}graphPick(row.source,row.target,row.kind,row.namespace||"");graphInspector();$("close-inspector").focus();};return button;});
  list.replaceChildren(...nodes);if(!nodes.length)list.append(el("p","empty","No matching loaded connections. Uninspected permissions are unknown; search a session name to inspect them."));restoreViewState(list,state);if(contextChanged&&state.inside)revealConnectionControl(nodes[0]||list,list);
}
$("graph-list-toggle").onclick=()=>{graphListView=!graphListView;$("graph-list-toggle").setAttribute("aria-pressed",String(graphListView));$("graph-list-toggle").textContent=graphListView?"Graph view":"List view";$("connection-list-panel").hidden=!graphListView;$("graph-canvas").hidden=graphListView;renderConnectionList();};
$("connection-search").oninput=()=>{connectionQuery=$("connection-search").value.trim().toLowerCase();connectionPage=0;renderConnectionList();};
$("connection-list-previous").onclick=()=>{connectionPage--;renderConnectionList();revealConnectionControl($("connection-list").querySelectorAll("[data-focus-key]")[0]||$("connection-list"),$("connection-list"));};
$("connection-list-next").onclick=()=>{connectionPage++;renderConnectionList();revealConnectionControl($("connection-list").querySelectorAll("[data-focus-key]")[0]||$("connection-list"),$("connection-list"));};
$("connection-list").onkeydown=event=>{if(["ArrowUp","ArrowDown","Home","End"].includes(event.key)){event.preventDefault();moveConnectionFocus($("connection-list"),event);}else if((event.key==="Enter"||event.key===" ")&&event.target===$("connection-list")){event.preventDefault();revealConnectionControl($("connection-list").querySelectorAll("[data-focus-key]")[0],$("connection-list"));}};

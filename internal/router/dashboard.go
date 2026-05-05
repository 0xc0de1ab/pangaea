package router

const routerDashboardHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Pangaea Router</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f6f7f8;
      --panel: #ffffff;
      --ink: #1e2428;
      --muted: #61707a;
      --line: #d9e0e4;
      --accent: #176f6b;
      --warn: #b46b00;
      --bad: #b42318;
      --good: #177245;
      --shadow: 0 1px 2px rgba(17, 24, 39, 0.08);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: var(--bg);
      color: var(--ink);
      font: 14px/1.45 ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    header {
      position: sticky;
      top: 0;
      z-index: 2;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 16px;
      padding: 14px 24px;
      border-bottom: 1px solid var(--line);
      background: rgba(246, 247, 248, 0.92);
      backdrop-filter: blur(12px);
    }
    h1 {
      margin: 0;
      font-size: 18px;
      font-weight: 700;
    }
    main {
      width: min(1500px, 100%);
      margin: 0 auto;
      padding: 20px 24px 36px;
    }
    button {
      border: 1px solid var(--line);
      background: var(--panel);
      color: var(--ink);
      border-radius: 6px;
      padding: 7px 10px;
      cursor: pointer;
      box-shadow: var(--shadow);
    }
    button:hover { border-color: #aab7be; }
    .toolbar {
      display: flex;
      align-items: center;
      gap: 10px;
      color: var(--muted);
      white-space: nowrap;
    }
    .status-dot {
      width: 9px;
      height: 9px;
      border-radius: 999px;
      background: var(--warn);
      display: inline-block;
    }
    .status-dot.ok { background: var(--good); }
    .status-dot.bad { background: var(--bad); }
    .metrics {
      display: grid;
      grid-template-columns: repeat(6, minmax(120px, 1fr));
      gap: 10px;
      margin-bottom: 18px;
    }
    .metric {
      min-height: 76px;
      padding: 12px;
      border: 1px solid var(--line);
      background: var(--panel);
      border-radius: 8px;
      box-shadow: var(--shadow);
    }
    .metric .label {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0;
    }
    .metric .value {
      margin-top: 7px;
      font-size: 24px;
      font-weight: 750;
    }
    .grid {
      display: grid;
      grid-template-columns: minmax(0, 1.4fr) minmax(340px, 0.8fr);
      gap: 16px;
    }
    section {
      margin-bottom: 16px;
      border: 1px solid var(--line);
      background: var(--panel);
      border-radius: 8px;
      box-shadow: var(--shadow);
      overflow: hidden;
    }
    section h2 {
      margin: 0;
      padding: 12px 14px;
      border-bottom: 1px solid var(--line);
      font-size: 14px;
      font-weight: 700;
    }
    .table-wrap { overflow-x: auto; }
    table {
      width: 100%;
      border-collapse: collapse;
      min-width: 680px;
    }
    th, td {
      padding: 9px 10px;
      border-bottom: 1px solid #edf1f3;
      text-align: left;
      vertical-align: top;
      white-space: nowrap;
    }
    th {
      color: var(--muted);
      font-size: 12px;
      font-weight: 650;
      background: #fbfcfc;
    }
    tr:last-child td { border-bottom: 0; }
    code {
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 12px;
    }
    .pill {
      display: inline-flex;
      align-items: center;
      height: 22px;
      padding: 0 7px;
      border-radius: 999px;
      background: #eef4f4;
      color: #175b57;
      font-size: 12px;
      font-weight: 650;
    }
    .pill.warn { background: #fff3df; color: var(--warn); }
    .pill.bad { background: #ffe9e7; color: var(--bad); }
    .empty {
      padding: 16px;
      color: var(--muted);
    }
    .error {
      padding: 10px 14px;
      border-top: 1px solid var(--line);
      color: var(--bad);
      background: #fff7f6;
      display: none;
    }
    @media (max-width: 1100px) {
      .metrics { grid-template-columns: repeat(3, minmax(120px, 1fr)); }
      .grid { grid-template-columns: 1fr; }
    }
    @media (max-width: 640px) {
      header { align-items: flex-start; flex-direction: column; padding: 12px 14px; }
      main { padding: 14px; }
      .metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); }
      .metric .value { font-size: 21px; }
      .toolbar { width: 100%; justify-content: space-between; }
    }
  </style>
</head>
<body>
  <header>
    <h1>Pangaea Router</h1>
    <div class="toolbar">
      <span><span id="health-dot" class="status-dot"></span> <span id="health-text">checking</span></span>
      <span id="updated-at">never</span>
      <button id="refresh" type="button" title="Refresh dashboard data">Refresh</button>
    </div>
  </header>
  <main>
    <div class="metrics">
      <div class="metric"><div class="label">Providers</div><div id="metric-providers" class="value">0</div></div>
      <div class="metric"><div class="label">Ready</div><div id="metric-ready" class="value">0</div></div>
      <div class="metric"><div class="label">Nodes</div><div id="metric-nodes" class="value">0</div></div>
      <div class="metric"><div class="label">Containers</div><div id="metric-containers" class="value">0</div></div>
      <div class="metric"><div class="label">Requests</div><div id="metric-requests" class="value">0</div></div>
      <div class="metric"><div class="label">Tokens</div><div id="metric-tokens" class="value">0</div></div>
    </div>
    <div class="grid">
      <div>
        <section>
          <h2>Providers</h2>
          <div id="providers" class="table-wrap"></div>
        </section>
        <section>
          <h2>Nodes</h2>
          <div id="nodes" class="table-wrap"></div>
        </section>
        <section>
          <h2>Containers</h2>
          <div id="containers" class="table-wrap"></div>
        </section>
      </div>
      <div>
        <section>
          <h2>Usage</h2>
          <div id="usage" class="table-wrap"></div>
        </section>
        <section>
          <h2>Sessions</h2>
          <div id="sessions" class="table-wrap"></div>
        </section>
        <section>
          <h2>Audit</h2>
          <div id="audit" class="table-wrap"></div>
        </section>
        <section>
          <h2>Recent Traces</h2>
          <div id="traces" class="table-wrap"></div>
        </section>
      </div>
    </div>
    <div id="error" class="error"></div>
  </main>
  <script>
    const endpoints = {
      health: "/healthz",
      providers: "/router/v1/providers",
      nodes: "/router/v1/nodes",
      containers: "/router/v1/containers",
      usage: "/router/v1/usage/providers",
      control: "/router/v1/control/sessions",
      data: "/router/v1/data/sessions",
      audit: "/router/v1/audit/events?limit=8",
      traces: "/router/v1/traces?limit=8"
    };
    const state = {};
    const $ = (id) => document.getElementById(id);
    function text(value) {
      if (value === undefined || value === null || value === "") return "";
      return String(value);
    }
    function esc(value) {
      return text(value).replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" }[ch]));
    }
    function fmtTime(value) {
      if (!value) return "";
      const d = new Date(value);
      if (Number.isNaN(d.getTime())) return "";
      return d.toLocaleString();
    }
    function statusPill(value) {
      const v = text(value) || "unknown";
      const cls = ["expired", "revoked", "unavailable", "down", "failed"].includes(v) ? " bad" : (["refresh_soon", "draining", "degraded"].includes(v) ? " warn" : "");
      return '<span class="pill' + cls + '">' + esc(v) + '</span>';
    }
    function table(rows, columns, emptyText) {
      if (!rows || rows.length === 0) return '<div class="empty">' + esc(emptyText || "No data") + '</div>';
      return '<table><thead><tr>' + columns.map((c) => '<th>' + esc(c.label) + '</th>').join("") + '</tr></thead><tbody>' + rows.map((row) => '<tr>' + columns.map((c) => '<td>' + c.render(row) + '</td>').join("") + '</tr>').join("") + '</tbody></table>';
    }
    async function loadJSON(name, url) {
      const res = await fetch(url, { cache: "no-store" });
      if (!res.ok) throw new Error(name + ": " + res.status);
      const type = res.headers.get("content-type") || "";
      if (type.includes("application/json")) return res.json();
      return res.text();
    }
    async function refresh() {
      $("error").style.display = "none";
      try {
        const health = await loadJSON("health", endpoints.health);
        $("health-dot").className = "status-dot ok";
        $("health-text").textContent = text(health) || "ok";
        const [providers, nodes, containers, usage, control, data, audit, traces] = await Promise.all([
          loadJSON("providers", endpoints.providers),
          loadJSON("nodes", endpoints.nodes),
          loadJSON("containers", endpoints.containers),
          loadJSON("usage", endpoints.usage),
          loadJSON("control sessions", endpoints.control),
          loadJSON("data sessions", endpoints.data),
          loadJSON("audit", endpoints.audit),
          loadJSON("traces", endpoints.traces)
        ]);
        state.providers = providers.providers || [];
        state.nodes = nodes.nodes || [];
        state.containers = containers.containers || [];
        state.usage = usage.usage || [];
        state.control = control.sessions || [];
        state.data = data.sessions || [];
        state.audit = audit.events || [];
        state.traces = traces.traces || [];
        render();
      } catch (err) {
        $("health-dot").className = "status-dot bad";
        $("health-text").textContent = "error";
        $("error").textContent = err.message;
        $("error").style.display = "block";
      }
    }
    function render() {
      const providers = state.providers || [];
      const usage = state.usage || [];
      $("metric-providers").textContent = providers.length;
      $("metric-ready").textContent = providers.filter((p) => p.health && p.health.status === "ready").length;
      $("metric-nodes").textContent = (state.nodes || []).length;
      $("metric-containers").textContent = (state.containers || []).length;
      $("metric-requests").textContent = usage.reduce((n, u) => n + ((u.usage && u.usage.requests) || 0), 0).toLocaleString();
      $("metric-tokens").textContent = usage.reduce((n, u) => n + ((u.usage && u.usage.total_tokens) || 0), 0).toLocaleString();
      $("updated-at").textContent = new Date().toLocaleTimeString();
      renderProviders(providers);
      renderNodes(state.nodes || []);
      renderContainers(state.containers || []);
      renderUsage(usage);
      renderSessions(state.control || [], state.data || []);
      renderAudit(state.audit || []);
      renderTraces(state.traces || []);
    }
    function renderProviders(rows) {
      $("providers").innerHTML = table(rows, [
        { label: "Instance", render: (r) => '<code>' + esc(r.identity && r.identity.provider_instance_id) + '</code>' },
        { label: "Service", render: (r) => esc(r.identity && r.identity.service) },
        { label: "Host", render: (r) => esc(r.identity && r.identity.host_name) },
        { label: "Account", render: (r) => esc(r.identity && r.identity.account && r.identity.account.display) },
        { label: "Health", render: (r) => statusPill(r.health && r.health.status) },
        { label: "Auth", render: (r) => statusPill(r.auth && r.auth.status) },
        { label: "Queue", render: (r) => esc(r.limits && r.limits.queue_depth) }
      ], "No providers registered");
    }
    function renderNodes(rows) {
      $("nodes").innerHTML = table(rows, [
        { label: "Node", render: (r) => '<code>' + esc(r.node_id) + '</code>' },
        { label: "Host", render: (r) => esc(r.host_name) },
        { label: "Runtime", render: (r) => esc(r.runtime && [r.runtime.kind, r.runtime.version].filter(Boolean).join(" ")) },
        { label: "Health", render: (r) => statusPill(r.health && r.health.status) },
        { label: "CPU", render: (r) => esc(r.resources && r.resources.cpu_percent ? r.resources.cpu_percent + "%" : "") },
        { label: "Memory", render: (r) => esc(r.resources && r.resources.memory_bytes ? Math.round(r.resources.memory_bytes / 1048576) + " MiB" : "") }
      ], "No nodes connected");
    }
    function renderContainers(rows) {
      $("containers").innerHTML = table(rows, [
        { label: "Container", render: (r) => '<code>' + esc(r.container_id) + '</code>' },
        { label: "Provider", render: (r) => '<code>' + esc(r.provider_instance_id) + '</code>' },
        { label: "Host", render: (r) => esc(r.host_name) },
        { label: "Image", render: (r) => esc(r.image) },
        { label: "State", render: (r) => statusPill(r.state) },
        { label: "Updated", render: (r) => esc(fmtTime(r.reported_at || r.started_at)) }
      ], "No containers reported");
    }
    function renderUsage(rows) {
      $("usage").innerHTML = table(rows, [
        { label: "Provider", render: (r) => '<code>' + esc(r.provider_instance_id) + '</code>' },
        { label: "Host", render: (r) => esc(r.host_name) },
        { label: "Requests", render: (r) => esc(r.usage && r.usage.requests) },
        { label: "Input", render: (r) => esc(r.usage && r.usage.input_tokens) },
        { label: "Output", render: (r) => esc(r.usage && r.usage.output_tokens) },
        { label: "Total", render: (r) => esc(r.usage && r.usage.total_tokens) }
      ], "No usage reports");
    }
    function renderSessions(controlRows, dataRows) {
      const rows = [
        ...controlRows.map((r) => ({ type: "control", ...r })),
        ...dataRows.map((r) => ({ type: "data", ...r }))
      ];
      $("sessions").innerHTML = table(rows, [
        { label: "Type", render: (r) => esc(r.type) },
        { label: "Provider", render: (r) => '<code>' + esc(r.provider_instance_id) + '</code>' },
        { label: "Host", render: (r) => esc(r.host_name) },
        { label: "Pending", render: (r) => esc(r.pending_requests) },
        { label: "Connected", render: (r) => esc(fmtTime(r.connected_at)) }
      ], "No active sessions");
    }
    function renderAudit(rows) {
      $("audit").innerHTML = table(rows, [
        { label: "Time", render: (r) => esc(fmtTime(r.created_at)) },
        { label: "Type", render: (r) => esc(r.type) },
        { label: "Outcome", render: (r) => statusPill(r.outcome) },
        { label: "Target", render: (r) => '<code>' + esc((r.target && (r.target.provider_instance_id || r.target.api_key_id || r.target.model)) || "") + '</code>' }
      ], "No audit events");
    }
    function renderTraces(rows) {
      $("traces").innerHTML = table(rows, [
        { label: "Request", render: (r) => '<code>' + esc(r.request_id) + '</code>' },
        { label: "Model", render: (r) => esc(r.route_request && r.route_request.model) },
        { label: "Status", render: (r) => statusPill(r.status) },
        { label: "Provider", render: (r) => '<code>' + esc(r.provider && r.provider.provider_instance_id) + '</code>' },
        { label: "ms", render: (r) => esc(r.duration_ms) }
      ], "No traces");
    }
    $("refresh").addEventListener("click", refresh);
    refresh();
    setInterval(refresh, 10000);
  </script>
</body>
</html>`

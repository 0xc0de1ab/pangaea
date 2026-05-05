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
    .auth-token {
      width: 220px;
      color: var(--muted);
      font-size: 12px;
    }
    .auth-token input {
      min-height: 31px;
      padding: 5px 8px;
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
    .inline-form {
      display: grid;
      grid-template-columns: minmax(160px, 1.4fr) minmax(110px, 0.8fr) auto auto;
      gap: 8px;
      padding: 12px;
      align-items: end;
      border-bottom: 1px solid var(--line);
    }
    label {
      display: grid;
      gap: 4px;
      color: var(--muted);
      font-size: 12px;
      font-weight: 650;
    }
    input, select {
      width: 100%;
      min-height: 34px;
      border: 1px solid var(--line);
      border-radius: 6px;
      background: #fff;
      color: var(--ink);
      padding: 6px 8px;
      font: inherit;
    }
    .checkline {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      min-height: 34px;
      color: var(--ink);
      white-space: nowrap;
    }
    .checkline input { width: auto; min-height: auto; }
    .control-form {
      display: grid;
      grid-template-columns: minmax(180px, 1.3fr) minmax(120px, 0.6fr) minmax(180px, 1fr) auto auto;
      gap: 8px;
      padding: 12px;
      align-items: end;
      border-bottom: 1px solid var(--line);
    }
    .quota-form {
      display: grid;
      grid-template-columns: repeat(4, minmax(110px, 1fr)) repeat(2, minmax(90px, 0.6fr)) auto;
      gap: 8px;
      padding: 12px;
      align-items: end;
      border-bottom: 1px solid var(--line);
    }
    .result {
      padding: 12px;
    }
    .kv {
      display: grid;
      grid-template-columns: 110px minmax(0, 1fr);
      gap: 7px 10px;
      align-items: baseline;
    }
    .kv .key {
      color: var(--muted);
      font-size: 12px;
      font-weight: 650;
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
      .toolbar { width: 100%; justify-content: space-between; flex-wrap: wrap; }
      .auth-token { width: 100%; }
      .inline-form, .control-form, .quota-form { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body>
  <header>
    <h1>Pangaea Router</h1>
    <div class="toolbar">
      <span><span id="health-dot" class="status-dot"></span> <span id="health-text">checking</span></span>
      <span id="updated-at">never</span>
      <label class="auth-token">API Key<input id="api-token" type="password" autocomplete="off" placeholder="Bearer token"></label>
      <button id="save-token" type="button" title="Use API key">Use</button>
      <button id="clear-token" type="button" title="Clear API key">Clear</button>
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
          <h2>Route Dry Run</h2>
          <form id="dry-run-form" class="inline-form">
            <label>Model<input id="dry-run-model" name="model" value="providersim-default" autocomplete="off"></label>
            <label>Dialect<select id="dry-run-dialect" name="api_dialect"><option value="openai">openai</option><option value="anthropic">anthropic</option><option value="gemini">gemini</option></select></label>
            <label class="checkline"><input id="dry-run-stream" name="stream" type="checkbox"> Stream</label>
            <button type="submit">Run</button>
          </form>
          <div id="dry-run-result" class="result"><div class="empty">No dry run yet</div></div>
        </section>
        <section>
          <h2>Provider Controls</h2>
          <form id="control-form" class="control-form">
            <label>Provider<select id="control-provider" name="provider_instance_id"></select></label>
            <label>Action<select id="control-action" name="action"><option value="refresh">refresh auth</option><option value="drain">drain</option><option value="resume">resume</option></select></label>
            <label>Reason<input id="control-reason" name="reason" autocomplete="off"></label>
            <label class="checkline"><input id="control-confirm" name="confirm" type="checkbox"> Confirm</label>
            <button type="submit">Send</button>
          </form>
          <div id="control-result" class="result"><div class="empty">No command sent</div></div>
        </section>
        <section>
          <h2>Usage</h2>
          <div id="usage" class="table-wrap"></div>
        </section>
        <section>
          <h2>API Keys</h2>
          <form id="api-key-form" class="inline-form">
            <label>Tenant<input id="api-key-tenant" name="tenant_id" value="dev" autocomplete="off"></label>
            <label>User<input id="api-key-user" name="user_id" value="dev" autocomplete="off"></label>
            <label class="checkline"><input id="api-key-disabled" name="disabled" type="checkbox"> Disabled</label>
            <button type="submit">Create</button>
          </form>
          <div id="api-key-result" class="result"><div class="empty">No key created</div></div>
          <div id="api-keys" class="table-wrap"></div>
        </section>
        <section>
          <h2>Quotas</h2>
          <form id="quota-form" class="quota-form">
            <label>Tenant<input id="quota-tenant" name="tenant_id" autocomplete="off"></label>
            <label>User<input id="quota-user" name="user_id" autocomplete="off"></label>
            <label>API Key<input id="quota-api-key" name="api_key_id" autocomplete="off"></label>
            <label>Model<input id="quota-model" name="model" value="providersim-default" autocomplete="off"></label>
            <label>Tokens<input id="quota-tokens" name="max_tokens" type="number" min="0" step="1"></label>
            <label>Requests<input id="quota-requests" name="max_requests" type="number" min="0" step="1"></label>
            <button type="submit">Set</button>
          </form>
          <div id="quota-result" class="result"><div class="empty">No quota change</div></div>
          <div id="quotas" class="table-wrap"></div>
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
      traces: "/router/v1/traces?limit=8",
      dryRun: "/router/v1/routes/dry-run",
      providerBase: "/router/v1/providers",
      apiKeys: "/router/v1/api-keys",
      quotas: "/router/v1/quotas"
    };
    const state = {};
    const tokenStorageKey = "pangaea.router.api_key";
    let apiToken = "";
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
    function loadToken() {
      try {
        apiToken = localStorage.getItem(tokenStorageKey) || "";
      } catch (_) {
        apiToken = "";
      }
      $("api-token").value = apiToken;
    }
    function saveToken(value) {
      apiToken = (value || "").trim();
      try {
        if (apiToken) localStorage.setItem(tokenStorageKey, apiToken);
        else localStorage.removeItem(tokenStorageKey);
      } catch (_) {}
      $("api-token").value = apiToken;
    }
    function authHeaders(extra) {
      const headers = Object.assign({}, extra || {});
      if (apiToken) headers.Authorization = "Bearer " + apiToken;
      return headers;
    }
    function authError(name) {
      return name + ": authorization required";
    }
    async function loadJSON(name, url) {
      const res = await fetch(url, { cache: "no-store", headers: authHeaders() });
      if (res.status === 401) throw new Error(authError(name));
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
        const [providers, nodes, containers, usage, control, data, audit, traces, apiKeys, quotas] = await Promise.all([
          loadJSON("providers", endpoints.providers),
          loadJSON("nodes", endpoints.nodes),
          loadJSON("containers", endpoints.containers),
          loadJSON("usage", endpoints.usage),
          loadJSON("control sessions", endpoints.control),
          loadJSON("data sessions", endpoints.data),
          loadJSON("audit", endpoints.audit),
          loadJSON("traces", endpoints.traces),
          loadJSON("api keys", endpoints.apiKeys),
          loadJSON("quotas", endpoints.quotas)
        ]);
        state.providers = providers.providers || [];
        state.nodes = nodes.nodes || [];
        state.containers = containers.containers || [];
        state.usage = usage.usage || [];
        state.control = control.sessions || [];
        state.data = data.sessions || [];
        state.audit = audit.events || [];
        state.traces = traces.traces || [];
        state.apiKeys = apiKeys.api_keys || [];
        state.quotas = quotas.quotas || [];
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
      renderProviderControlOptions(providers);
      renderNodes(state.nodes || []);
      renderContainers(state.containers || []);
      renderUsage(usage);
      renderAPIKeys(state.apiKeys || []);
      renderQuotas(state.quotas || []);
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
    function renderProviderControlOptions(rows) {
      const select = $("control-provider");
      const current = select.value;
      select.innerHTML = (rows || []).map((r) => {
        const id = text(r.identity && r.identity.provider_instance_id);
        const host = text(r.identity && r.identity.host_name);
        const account = text(r.identity && r.identity.account && r.identity.account.display);
        const label = [id, host, account].filter(Boolean).join(" / ");
        return '<option value="' + esc(id) + '">' + esc(label || id) + '</option>';
      }).join("");
      if ([...select.options].some((option) => option.value === current)) {
        select.value = current;
      }
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
    function renderAPIKeys(rows) {
      $("api-keys").innerHTML = table(rows, [
        { label: "Key", render: (r) => '<code>' + esc(r.id) + '</code>' },
        { label: "Prefix", render: (r) => '<code>' + esc(r.prefix) + '</code>' },
        { label: "Tenant", render: (r) => esc(r.tenant_id) },
        { label: "User", render: (r) => esc(r.user_id) },
        { label: "Status", render: (r) => statusPill(r.disabled ? "disabled" : (r.expires_at && new Date(r.expires_at) <= new Date() ? "expired" : "active")) },
        { label: "Expires", render: (r) => esc(fmtTime(r.expires_at)) },
        { label: "Last Used", render: (r) => esc(fmtTime(r.last_used_at)) },
        { label: "Action", render: (r) => '<button type="button" data-api-key-delete="' + esc(r.id) + '">Delete</button>' }
      ], "No API keys configured");
      for (const button of $("api-keys").querySelectorAll("[data-api-key-delete]")) {
        button.addEventListener("click", deleteAPIKey);
      }
    }
    async function createAPIKey(event) {
      event.preventDefault();
      $("error").style.display = "none";
      const request = {
        tenant_id: $("api-key-tenant").value.trim(),
        user_id: $("api-key-user").value.trim(),
        disabled: $("api-key-disabled").checked
      };
      try {
        const res = await fetch(endpoints.apiKeys, {
          method: "POST",
          headers: authHeaders({ "content-type": "application/json" }),
          body: JSON.stringify(request)
        });
        const payload = await res.json().catch(() => ({}));
        if (res.ok && payload.raw_key) {
          saveToken(payload.raw_key);
        }
        renderAPIKeyResult(payload, res.status);
        if (res.ok) await refresh();
      } catch (err) {
        renderAPIKeyResult({ error: err.message }, 0);
      }
    }
    function renderAPIKeyResult(payload, status) {
      const ok = status >= 200 && status < 300;
      const key = payload.api_key || {};
      let html = '<div class="kv">';
      html += '<div class="key">Status</div><div>' + statusPill(ok ? "created" : "failed") + (status ? ' <code>' + esc(status) + '</code>' : '') + '</div>';
      html += '<div class="key">Key</div><div><code>' + esc(key.id || "") + '</code></div>';
      html += '<div class="key">Prefix</div><div><code>' + esc(key.prefix || "") + '</code></div>';
      html += '<div class="key">Result</div><div>' + esc(payload.error || (payload.raw_key ? "raw key stored for this browser" : "")) + '</div>';
      if (payload.raw_key) {
        html += '<div class="key">Raw</div><div><code>' + esc(payload.raw_key) + '</code></div>';
      }
      html += '</div>';
      $("api-key-result").innerHTML = html;
    }
    async function deleteAPIKey(event) {
      const id = event.currentTarget.getAttribute("data-api-key-delete") || "";
      if (!id || !window.confirm("Delete API key " + id + "?")) return;
      try {
        const res = await fetch(endpoints.apiKeys + "/" + encodeURIComponent(id), {
          method: "DELETE",
          headers: authHeaders()
        });
        if (res.status === 204) {
          renderAPIKeyResult({ api_key: { id }, error: "deleted" }, res.status);
          await refresh();
          return;
        }
        const payload = await res.json().catch(() => ({}));
        renderAPIKeyResult(payload, res.status);
      } catch (err) {
        renderAPIKeyResult({ error: err.message }, 0);
      }
    }
    function renderQuotas(rows) {
      $("quotas").innerHTML = table(rows, [
        { label: "Scope", render: (r) => '<code>' + esc(fmtScope(r.scope || {})) + '</code>' },
        { label: "Limit", render: (r) => esc(fmtLimit(r.limit || {})) },
        { label: "Committed", render: (r) => esc(fmtUsage(r.committed || {})) },
        { label: "Reserved", render: (r) => esc(fmtUsage(r.reserved || {})) }
      ], "No quota records");
    }
    function fmtScope(scope) {
      return [
        scope.tenant_id && "tenant=" + scope.tenant_id,
        scope.user_id && "user=" + scope.user_id,
        scope.api_key_id && "key=" + scope.api_key_id,
        scope.model && "model=" + scope.model
      ].filter(Boolean).join(" ");
    }
    function fmtLimit(limit) {
      return [
        limit.max_tokens ? limit.max_tokens + " tokens" : "",
        limit.max_requests ? limit.max_requests + " requests" : ""
      ].filter(Boolean).join(" / ");
    }
    function fmtUsage(usage) {
      return [
        usage.tokens ? usage.tokens + " tokens" : "0 tokens",
        usage.requests ? usage.requests + " requests" : "0 requests"
      ].join(" / ");
    }
    async function setQuotaLimit(event) {
      event.preventDefault();
      $("error").style.display = "none";
      const maxTokens = parseInt($("quota-tokens").value || "0", 10);
      const maxRequests = parseInt($("quota-requests").value || "0", 10);
      const request = {
        scope: {
          tenant_id: $("quota-tenant").value.trim(),
          user_id: $("quota-user").value.trim(),
          api_key_id: $("quota-api-key").value.trim(),
          model: $("quota-model").value.trim()
        },
        limit: {
          max_tokens: Number.isFinite(maxTokens) ? maxTokens : 0,
          max_requests: Number.isFinite(maxRequests) ? maxRequests : 0
        }
      };
      try {
        const res = await fetch("/router/v1/quotas/limits", {
          method: "PUT",
          headers: authHeaders({ "content-type": "application/json" }),
          body: JSON.stringify(request)
        });
        const payload = await res.json().catch(() => ({}));
        renderQuotaResult(payload, res.status);
        if (res.ok) await refresh();
      } catch (err) {
        renderQuotaResult({ error: err.message }, 0);
      }
    }
    function renderQuotaResult(payload, status) {
      const ok = status >= 200 && status < 300;
      let html = '<div class="kv">';
      html += '<div class="key">Status</div><div>' + statusPill(ok ? "set" : "failed") + (status ? ' <code>' + esc(status) + '</code>' : '') + '</div>';
      html += '<div class="key">Scope</div><div><code>' + esc(fmtScope(payload.scope || {})) + '</code></div>';
      html += '<div class="key">Limit</div><div>' + esc(fmtLimit(payload.limit || {})) + '</div>';
      html += '<div class="key">Result</div><div>' + esc(payload.error || "") + '</div>';
      html += '</div>';
      $("quota-result").innerHTML = html;
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
        { label: "Error", render: (r) => esc(traceError(r)) },
        { label: "ms", render: (r) => esc(r.duration_ms) }
      ], "No traces");
    }
    function traceError(trace) {
      if (!trace || !trace.error) return "";
      const parts = [];
      if (trace.error_status) parts.push("HTTP " + trace.error_status);
      if (trace.error_code) parts.push(trace.error_code);
      if (trace.retry_after) parts.push("retry-after " + trace.retry_after);
      if (parts.length > 0) return parts.join(" / ");
      return trace.error;
    }
    async function runDryRun(event) {
      event.preventDefault();
      $("error").style.display = "none";
      const model = $("dry-run-model").value.trim();
      if (!model) {
        renderDryRunResult({ allowed: false, reason: "model is required" }, 0);
        return;
      }
      const request = {
        model,
        api_dialect: $("dry-run-dialect").value,
        stream: $("dry-run-stream").checked
      };
      try {
        const res = await fetch(endpoints.dryRun, {
          method: "POST",
          headers: authHeaders({ "content-type": "application/json" }),
          body: JSON.stringify(request)
        });
        const decision = await res.json();
        renderDryRunResult(decision, res.status);
      } catch (err) {
        renderDryRunResult({ allowed: false, reason: err.message }, 0);
      }
    }
    function renderDryRunResult(decision, status) {
      const selected = decision.selected || "";
      const rejections = decision.rejections || [];
      let html = '<div class="kv">';
      html += '<div class="key">Status</div><div>' + statusPill(decision.allowed ? "allowed" : "rejected") + (status ? ' <code>' + esc(status) + '</code>' : '') + '</div>';
      html += '<div class="key">Route</div><div><code>' + esc(decision.route_id) + '</code></div>';
      html += '<div class="key">Model</div><div>' + esc([decision.model_alias, decision.canonical_model].filter(Boolean).join(" -> ")) + '</div>';
      html += '<div class="key">Selected</div><div><code>' + esc(selected) + '</code></div>';
      html += '<div class="key">Reason</div><div>' + esc(decision.reason) + '</div>';
      html += '</div>';
      if (rejections.length > 0) {
        html += table(rejections, [
          { label: "Provider", render: (r) => '<code>' + esc(r.provider_instance_id) + '</code>' },
          { label: "Reason", render: (r) => esc(r.reason) }
        ], "");
      }
      $("dry-run-result").innerHTML = html;
    }
    async function sendProviderControl(event) {
      event.preventDefault();
      $("error").style.display = "none";
      const providerID = $("control-provider").value.trim();
      const action = $("control-action").value;
      const reason = $("control-reason").value.trim();
      const confirm = $("control-confirm").checked;
      if (!providerID) {
        renderControlResult({ error: "provider is required" }, 0);
        return;
      }
      if (!reason || !confirm) {
        renderControlResult({ error: "reason and confirm are required" }, 0);
        return;
      }
      const base = endpoints.providerBase + "/" + encodeURIComponent(providerID);
      const path = action === "refresh" ? base + "/auth/refresh" : base + "/drain";
      const body = action === "refresh"
        ? { reason, confirm, timeout_seconds: 30 }
        : { drain: action === "drain", reason, confirm, timeout_seconds: 5 };
      try {
        const res = await fetch(path, {
          method: "POST",
          headers: authHeaders({ "content-type": "application/json" }),
          body: JSON.stringify(body)
        });
        const payload = await res.json().catch(() => ({}));
        renderControlResult(payload, res.status);
        $("control-confirm").checked = false;
        await refresh();
      } catch (err) {
        renderControlResult({ error: err.message }, 0);
      }
    }
    function renderControlResult(payload, status) {
      const ok = status >= 200 && status < 300;
      let html = '<div class="kv">';
      html += '<div class="key">Status</div><div>' + statusPill(ok ? "accepted" : "failed") + (status ? ' <code>' + esc(status) + '</code>' : '') + '</div>';
      html += '<div class="key">Provider</div><div><code>' + esc(payload.provider_instance_id || $("control-provider").value) + '</code></div>';
      html += '<div class="key">Result</div><div>' + esc(payload.error || payload.reason || (payload.ok === false ? "failed" : "sent")) + '</div>';
      if (payload.refresh_id) {
        html += '<div class="key">Refresh</div><div><code>' + esc(payload.refresh_id) + '</code></div>';
      }
      if (payload.auth && payload.auth.status) {
        html += '<div class="key">Auth</div><div>' + statusPill(payload.auth.status) + '</div>';
      }
      html += '</div>';
      $("control-result").innerHTML = html;
    }
    $("refresh").addEventListener("click", refresh);
    $("save-token").addEventListener("click", () => { saveToken($("api-token").value); refresh(); });
    $("clear-token").addEventListener("click", () => { saveToken(""); refresh(); });
    $("api-token").addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        saveToken($("api-token").value);
        refresh();
      }
    });
    $("dry-run-form").addEventListener("submit", runDryRun);
    $("control-form").addEventListener("submit", sendProviderControl);
    $("api-key-form").addEventListener("submit", createAPIKey);
    $("quota-form").addEventListener("submit", setQuotaLimit);
    loadToken();
    refresh();
    setInterval(refresh, 10000);
  </script>
</body>
</html>`

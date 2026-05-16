import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, ChevronLeft, ChevronRight, Clock3, Eye, FileJson, RadioTower, Settings2, ShieldAlert, X } from "lucide-react";
import type { DashboardViewProps } from "../app/dashboard";
import { DataTable, type DashboardColumn } from "../components/DataTable";
import { MetricTile } from "../components/MetricTile";
import { Section } from "../components/Section";
import { StatusBadge } from "../components/StatusBadge";
import { capacityRows, deriveIncidents, failedTraceRate, providerAccountLabel, quotaPressure, quotaPressureLabel } from "../lib/derive";
import { age, compactNumber, cx, fmtTime, hasText, middleEllipsis, n, scopeLabel } from "../lib/format";
import type { Incident, ProviderRegistration, QuotaSnapshot, RequestTrace } from "../lib/types";

const overviewCapacityPageSizeOptions = [5, 10, 25, 50];

type FailureRow = {
  ordinal: number;
  trace: RequestTrace;
};

export function Overview({ data, queries, search }: DashboardViewProps) {
  const [capacityPageIndex, setCapacityPageIndex] = useState(0);
  const [capacityPageSize, setCapacityPageSize] = useState(10);
  const [capacitySettingsOpen, setCapacitySettingsOpen] = useState(false);
  const [selectedErrorTrace, setSelectedErrorTrace] = useState<RequestTrace | null>(null);
  const incidents = useMemo(() => deriveIncidents(data).filter((incident) => hasText(incident, search)), [data, search]);
  const capacity = useMemo(() => capacityRows(data.providers).filter((row) => hasText(row, search)), [data.providers, search]);
  const capacityPageCount = Math.max(1, Math.ceil(capacity.length / capacityPageSize));
  const providerCount = data.providers.length;
  const ready = data.providers.filter((provider) => provider.health?.status === "ready").length;
  const degraded = data.providers.filter((provider) => provider.health?.status === "degraded" || provider.health?.status === "draining").length;
  const down = data.providers.filter((provider) => provider.health?.status === "down").length;
  const activeStreams = data.providers.reduce((sum, provider) => sum + (provider.limits?.active_streams ?? 0), 0);
  const pendingRequests = data.dataSessions.reduce((sum, session) => sum + (session.pending_requests ?? 0), 0);
  const tokens = data.usage.reduce((sum, usage) => sum + (usage.usage?.total_tokens ?? 0), 0);
  const requests = data.usage.reduce((sum, usage) => sum + (usage.usage?.requests ?? 0), 0);
  const authRisk = data.providers
    .filter((provider) => ["refresh_soon", "expired", "revoked", "conflict", "unavailable", "no_login"].includes(provider.auth?.status || ""))
    .filter((provider) => hasText(provider, search));
  const quotaRisk = data.quotas.filter((quota) => quotaPressure(quota) >= 0.7).filter((quota) => hasText(quota, search));
  const recentFailures: FailureRow[] = data.traces
    .filter((trace) => trace.status !== "completed")
    .filter((trace) => hasText(trace, search))
    .slice(0, 12)
    .map((trace, index) => ({ ordinal: index + 1, trace }));

  useEffect(() => {
    setCapacityPageIndex(0);
  }, [search, capacityPageSize]);

  useEffect(() => {
    setCapacityPageIndex((value) => Math.min(value, capacityPageCount - 1));
  }, [capacityPageCount]);

  const incidentColumns: DashboardColumn<Incident>[] = [
    {
      id: "severity",
      header: "Severity",
      sortValue: (row) => row.severity,
      cell: (row) => <StatusBadge value={row.severity} tone={row.severity === "critical" ? "danger" : row.severity === "warning" ? "warn" : "unknown"} />,
      width: "118px",
    },
    {
      id: "scope",
      header: "Scope",
      sortValue: (row) => row.scope,
      cell: (row) => <span className="mono">{middleEllipsis(row.scope, 18, 12)}</span>,
    },
    {
      id: "title",
      header: "Condition",
      sortValue: (row) => row.title,
      cell: (row) => (
        <div className="cell-stack">
          <strong>{row.title}</strong>
          <span>{row.detail}</span>
        </div>
      ),
    },
    {
      id: "at",
      header: "Observed",
      sortValue: (row) => row.at,
      cell: (row) => age(row.at),
      width: "90px",
    },
  ];

  const capacityColumns: DashboardColumn<(typeof capacity)[number]>[] = [
    { id: "service", header: "Service", sortValue: (row) => row.service, cell: (row) => row.service, width: "110px" },
    { id: "model", header: "Model", sortValue: (row) => row.model, cell: (row) => <OverviewCapacityModelCell row={row} /> },
    { id: "hosts", header: "Hosts", sortValue: (row) => row.hosts.size, cell: (row) => n(row.hosts.size), align: "right", width: "76px" },
    { id: "ready", header: "Ready", sortValue: (row) => row.ready, cell: (row) => n(row.ready), align: "right", width: "76px" },
    { id: "degraded", header: "Degraded", sortValue: (row) => row.degraded, cell: (row) => n(row.degraded), align: "right", width: "92px" },
    { id: "down", header: "Down", sortValue: (row) => row.down, cell: (row) => n(row.down), align: "right", width: "76px" },
    { id: "streams", header: "Streams", sortValue: (row) => row.activeStreams, cell: (row) => n(row.activeStreams), align: "right", width: "84px" },
    { id: "queue", header: "Queue", sortValue: (row) => row.queueDepth, cell: (row) => n(row.queueDepth), align: "right", width: "76px" },
  ];

  const authColumns: DashboardColumn<ProviderRegistration>[] = [
    { id: "provider", header: "Provider", sortValue: (row) => row.identity.provider_instance_id, cell: (row) => <span className="mono">{middleEllipsis(row.identity.provider_instance_id)}</span> },
    { id: "scope", header: "Service / Host / Account", sortValue: (row) => `${row.identity.service}:${row.identity.host_name}:${providerAccountLabel(row)}`, cell: (row) => [row.identity.service, row.identity.host_name, providerAccountLabel(row)].filter(Boolean).join(" / ") },
    { id: "auth", header: "Auth", sortValue: (row) => row.auth?.status, cell: (row) => <StatusBadge value={row.auth?.status} />, width: "132px" },
    { id: "expires", header: "Expires", sortValue: (row) => row.auth?.expires_at, cell: (row) => fmtTime(row.auth?.expires_at), width: "136px" },
  ];

  const quotaColumns: DashboardColumn<QuotaSnapshot>[] = [
    { id: "scope", header: "Scope", sortValue: (row) => scopeLabel(row.scope), cell: (row) => <span className="mono">{middleEllipsis(scopeLabel(row.scope), 26, 12)}</span> },
    { id: "pressure", header: "Pressure", sortValue: (row) => quotaPressure(row), cell: (row) => <StatusBadge value={quotaPressureLabel(row)} tone={quotaPressure(row) >= 1 ? "danger" : "warn"} />, width: "112px" },
    { id: "tokens", header: "Tokens", sortValue: (row) => row.committed?.tokens ?? 0, cell: (row) => `${n(row.committed?.tokens ?? 0)} / ${n(row.limit?.max_tokens ?? 0)}`, align: "right", width: "138px" },
    { id: "requests", header: "Requests", sortValue: (row) => row.committed?.requests ?? 0, cell: (row) => `${n(row.committed?.requests ?? 0)} / ${n(row.limit?.max_requests ?? 0)}`, align: "right", width: "138px" },
  ];

  const failureColumns: DashboardColumn<FailureRow>[] = [
    { id: "ordinal", header: "#", sortValue: (row) => row.ordinal, cell: (row) => <span className="mono failure-ordinal">{n(row.ordinal)}</span>, align: "right", width: "58px" },
    { id: "time", header: "Time", sortValue: (row) => row.trace.started_at, cell: (row) => fmtTime(row.trace.started_at), width: "126px" },
    { id: "request", header: "Request", sortValue: (row) => row.trace.request_id, cell: (row) => <span className="mono failure-request-id">{row.trace.request_id}</span>, width: "320px" },
    { id: "model", header: "Model", sortValue: (row) => row.trace.route_request?.model, cell: (row) => row.trace.route_request?.model || "" },
    { id: "status", header: "Status", sortValue: (row) => row.trace.status, cell: (row) => <StatusBadge value={row.trace.status} />, width: "128px" },
    {
      id: "error",
      header: "Error",
      sortValue: (row) => failureErrorText(row.trace),
      cell: (row) => (
        <FailureErrorCell
          trace={row.trace}
          onOpen={() => setSelectedErrorTrace(row.trace)}
        />
      ),
    },
  ];

  return (
    <div className="view-stack">
      <div className="metric-grid">
        <MetricTile label="Providers" value={n(providerCount)} subvalue={`${ready} ready / ${degraded} degraded / ${down} down`} tone={down ? "danger" : degraded ? "warn" : "ok"} />
        <MetricTile label="Sessions" value={`${data.controlSessions.length} / ${data.dataSessions.length}`} subvalue="control / data" tone={data.providers.length && data.dataSessions.length < data.providers.length ? "warn" : "ok"} />
        <MetricTile label="Live Pressure" value={n(activeStreams + pendingRequests)} subvalue={`${activeStreams} streams, ${pendingRequests} pending`} tone={activeStreams + pendingRequests > 0 ? "warn" : "neutral"} />
        <MetricTile label="Usage" value={compactNumber(tokens)} subvalue={`${compactNumber(requests)} requests`} />
        <MetricTile label="Trace Failures" value={failedTraceRate(data.traces)} subvalue={`${data.traces.length} recent traces`} tone={data.traces.some((trace) => trace.status !== "completed") ? "warn" : "ok"} />
        <MetricTile label="Incidents" value={n(incidents.length)} subvalue="derived from current endpoints" tone={incidents.some((incident) => incident.severity === "critical") ? "danger" : incidents.length ? "warn" : "ok"} />
      </div>

      <div className="overview-layout">
        <div className="main-column">
          <Section title="Fleet Health" subtitle="Provider, session, and route pressure from live router endpoints" error={queries.providers.error}>
            <div className="health-strip">
              <div><RadioTower aria-hidden="true" size={16} /><span>Providers</span><strong>{ready}/{providerCount}</strong></div>
              <div><ShieldAlert aria-hidden="true" size={16} /><span>Auth risk</span><strong>{authRisk.length}</strong></div>
              <div><Clock3 aria-hidden="true" size={16} /><span>Data age</span><strong>{age(Math.min(...Object.values(queries).map((query) => query.dataUpdatedAt || Date.now())))}</strong></div>
              <div><AlertTriangle aria-hidden="true" size={16} /><span>Failures</span><strong>{recentFailures.length}</strong></div>
            </div>
          </Section>

          <Section
            title="Capacity Matrix"
            subtitle={`${n(capacity.length)} rows grouped by service and reported model`}
            error={queries.providers.error}
            actions={
              <OverviewCapacityPageSizeMenu
                pageSize={capacityPageSize}
                open={capacitySettingsOpen}
                onOpenChange={setCapacitySettingsOpen}
                onPageSizeChange={setCapacityPageSize}
              />
            }
          >
            <DataTable
              rows={capacity}
              columns={capacityColumns}
              empty="No provider capacity is available"
              compact
              pagination={{ pageIndex: capacityPageIndex, pageSize: capacityPageSize }}
            />
            <OverviewCapacityPagination
              pageIndex={capacityPageIndex}
              pageCount={capacityPageCount}
              pageSize={capacityPageSize}
              total={capacity.length}
              onPageChange={setCapacityPageIndex}
            />
          </Section>
        </div>

        <div className="side-column">
          <Section title="Incident Queue" subtitle="Derived incident list" error={queries.providers.error || queries.traces.error || queries.quotas.error}>
            <DataTable rows={incidents} columns={incidentColumns} empty="No derived incidents" compact />
          </Section>

          <Section title="Auth Risk" subtitle="Providers needing credential attention" error={queries.providers.error}>
            <DataTable rows={authRisk} columns={authColumns} empty="No auth risk providers" compact />
          </Section>

          <Section title="Quota Pressure" subtitle="Scopes above 70 percent pressure" error={queries.quotas.error}>
            <DataTable rows={quotaRisk} columns={quotaColumns} empty="No quota pressure" compact />
          </Section>
        </div>
      </div>

      <Section title="Recent Route Failures" subtitle="Rejected, failed, and upstream-error traces" error={queries.traces.error}>
        <DataTable rows={recentFailures} columns={failureColumns} empty="No recent route failures" compact />
      </Section>
      <FailureErrorModal trace={selectedErrorTrace} onClose={() => setSelectedErrorTrace(null)} />
    </div>
  );
}

function OverviewCapacityModelCell({ row }: { row: ReturnType<typeof capacityRows>[number] }) {
  const groupMembers = Array.from(row.groupMembers ?? []);
  const title = [
    row.groupModel ? `Group model${groupMembers.length ? `: ${groupMembers.join(", ")}` : ""}` : "",
    row.aliasModel ? "Alias model" : "",
  ].filter(Boolean).join(" / ");
  return (
    <span className={cx("model-name-line", "capacity-model-name")} title={title || row.model}>
      {row.groupModel ? <span className="model-group-badge mini" title="Group model">G</span> : null}
      {row.aliasModel ? <span className="model-alias-badge mini" title="Alias model">A</span> : null}
      <span className="mono">{middleEllipsis(row.model, 22, 10)}</span>
    </span>
  );
}

function FailureErrorCell({ trace, onOpen }: { trace: RequestTrace; onOpen: () => void }) {
  const text = failureErrorText(trace);
  if (!text) {
    return "";
  }
  const parsed = parseFailureError(text);
  const showDetails = text.length > 88 || Boolean(parsed.payload);
  return (
    <div className="failure-error-cell">
      <span className="failure-error-text">{middleEllipsis(text, 72, 22)}</span>
      {showDetails ? (
        <button
          className="icon-button small"
          type="button"
          aria-label="Show error detail"
          title="Show error detail"
          onClick={(event) => {
            event.stopPropagation();
            onOpen();
          }}
        >
          <Eye aria-hidden="true" size={15} />
        </button>
      ) : null}
    </div>
  );
}

function FailureErrorModal({ trace, onClose }: { trace: RequestTrace | null; onClose: () => void }) {
  if (!trace) {
    return null;
  }
  const text = failureErrorText(trace);
  const parsed = parseFailureError(text);
  return (
    <div className="modal-layer" role="presentation">
      <button className="modal-scrim" type="button" aria-label="Close error detail" onClick={onClose} />
      <div className="modal error-detail-modal" role="dialog" aria-modal="true" aria-labelledby="failure-error-title">
        <div className="modal-header">
          <div className="modal-title-row">
            <AlertTriangle aria-hidden="true" size={18} />
            <h2 id="failure-error-title">Route Failure Error</h2>
          </div>
          <button className="icon-button" type="button" aria-label="Close error detail" onClick={onClose}>
            <X aria-hidden="true" size={17} />
          </button>
        </div>
        <div className="modal-body error-detail-body">
          <div className="badge-row">
            <StatusBadge value={trace.status} />
            {trace.error_status ? <StatusBadge value={`HTTP ${trace.error_status}`} tone="warn" /> : null}
            {trace.error_code ? <StatusBadge value={trace.error_code} tone="unknown" /> : null}
          </div>
          <div className="kv-list">
            <div className="kv-key">Request</div><div className="kv-value mono">{trace.request_id}</div>
            <div className="kv-key">Model</div><div className="kv-value mono">{trace.route_request?.model || ""}</div>
            <div className="kv-key">Started</div><div className="kv-value">{fmtTime(trace.started_at)}</div>
          </div>
          <ErrorDetailBlock title="Raw Error" value={text || "(empty error)"} />
          {parsed.payload ? (
            <ErrorDetailBlock title="Parsed Error Payload" value={formatPrettyValue(parsed.payload)} json />
          ) : null}
        </div>
      </div>
    </div>
  );
}

function ErrorDetailBlock({ title, value, json }: { title: string; value: string; json?: boolean }) {
  return (
    <section className="error-detail-section">
      <div className="error-detail-section-head">
        {json ? <FileJson aria-hidden="true" size={15} /> : null}
        <strong>{title}</strong>
      </div>
      <pre className="trace-http-block error-detail-pre">{value}</pre>
    </section>
  );
}

function failureErrorText(trace: RequestTrace) {
  return trace.error || trace.decision?.reason || trace.error_code || "";
}

function parseFailureError(text: string) {
  const payload = parseJSONLike(text);
  return { payload };
}

function parseJSONLike(value: string): unknown | undefined {
  const trimmed = value.trim();
  if (!trimmed || !/^[\[{"]/.test(trimmed)) {
    return undefined;
  }
  try {
    return JSON.parse(trimmed);
  } catch {
    const jsonPrefix = extractJSONPrefix(trimmed);
    if (!jsonPrefix) {
      return undefined;
    }
    try {
      return JSON.parse(jsonPrefix);
    } catch {
      return undefined;
    }
  }
}

function extractJSONPrefix(value: string) {
  const start = value.search(/[\[{]/);
  if (start < 0) {
    return "";
  }
  const opener = value[start];
  const closer = opener === "{" ? "}" : "]";
  let depth = 0;
  let inString = false;
  let escaped = false;
  for (let index = start; index < value.length; index += 1) {
    const char = value[index];
    if (inString) {
      if (escaped) {
        escaped = false;
      } else if (char === "\\") {
        escaped = true;
      } else if (char === "\"") {
        inString = false;
      }
      continue;
    }
    if (char === "\"") {
      inString = true;
      continue;
    }
    if (char === opener) {
      depth += 1;
    } else if (char === closer) {
      depth -= 1;
      if (depth === 0) {
        return value.slice(start, index + 1);
      }
    }
  }
  return "";
}

function formatPrettyValue(value: unknown) {
  if (typeof value === "string") {
    const parsed = parseJSONLike(value);
    return parsed === undefined ? value : JSON.stringify(parsed, null, 2);
  }
  return JSON.stringify(value, null, 2);
}

function OverviewCapacityPageSizeMenu({
  pageSize,
  open,
  onOpenChange,
  onPageSizeChange,
}: {
  pageSize: number;
  open: boolean;
  onOpenChange: (value: boolean) => void;
  onPageSizeChange: (value: number) => void;
}) {
  return (
    <div className="table-settings">
      <button className="icon-button" type="button" aria-label="Capacity matrix settings" aria-expanded={open} onClick={() => onOpenChange(!open)}>
        <Settings2 aria-hidden="true" size={16} />
      </button>
      {open ? (
        <div className="table-settings-popover" role="dialog" aria-label="Capacity matrix page size">
          <strong>Rows per page</strong>
          <div className="page-size-options">
            {overviewCapacityPageSizeOptions.map((option) => (
              <button
                key={option}
                className={cx("page-size-option", option === pageSize && "selected")}
                type="button"
                onClick={() => {
                  onPageSizeChange(option);
                  onOpenChange(false);
                }}
              >
                {n(option)}
              </button>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function OverviewCapacityPagination({
  pageIndex,
  pageCount,
  pageSize,
  total,
  onPageChange,
}: {
  pageIndex: number;
  pageCount: number;
  pageSize: number;
  total: number;
  onPageChange: (value: number) => void;
}) {
  const start = total === 0 ? 0 : pageIndex * pageSize + 1;
  const end = Math.min(total, (pageIndex + 1) * pageSize);
  return (
    <div className="table-pagination">
      <span>
        {n(start)}-{n(end)} of {n(total)} rows
      </span>
      <div className="pagination-actions">
        <span>Page {n(pageIndex + 1)} / {n(pageCount)}</span>
        <button className="icon-button small" type="button" aria-label="Previous capacity matrix page" disabled={pageIndex === 0} onClick={() => onPageChange(Math.max(0, pageIndex - 1))}>
          <ChevronLeft aria-hidden="true" size={15} />
        </button>
        <button className="icon-button small" type="button" aria-label="Next capacity matrix page" disabled={pageIndex >= pageCount - 1} onClick={() => onPageChange(Math.min(pageCount - 1, pageIndex + 1))}>
          <ChevronRight aria-hidden="true" size={15} />
        </button>
      </div>
    </div>
  );
}

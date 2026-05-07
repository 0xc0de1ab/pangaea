import { useMemo } from "react";
import { AlertTriangle, Clock3, RadioTower, ShieldAlert } from "lucide-react";
import type { DashboardViewProps } from "../app/dashboard";
import { DataTable, type DashboardColumn } from "../components/DataTable";
import { MetricTile } from "../components/MetricTile";
import { Section } from "../components/Section";
import { StatusBadge } from "../components/StatusBadge";
import { capacityRows, deriveIncidents, failedTraceRate, providerAccountLabel, quotaPressure, quotaPressureLabel } from "../lib/derive";
import { age, compactNumber, fmtTime, hasText, middleEllipsis, n, scopeLabel } from "../lib/format";
import type { Incident, ProviderRegistration, QuotaSnapshot, RequestTrace } from "../lib/types";

export function Overview({ data, queries, search }: DashboardViewProps) {
  const incidents = useMemo(() => deriveIncidents(data).filter((incident) => hasText(incident, search)), [data, search]);
  const capacity = useMemo(() => capacityRows(data.providers).filter((row) => hasText(row, search)), [data.providers, search]);
  const providerCount = data.providers.length;
  const ready = data.providers.filter((provider) => provider.health?.status === "ready").length;
  const degraded = data.providers.filter((provider) => provider.health?.status === "degraded" || provider.health?.status === "draining").length;
  const down = data.providers.filter((provider) => provider.health?.status === "down").length;
  const activeStreams = data.providers.reduce((sum, provider) => sum + (provider.limits?.active_streams ?? 0), 0);
  const pendingRequests = data.dataSessions.reduce((sum, session) => sum + (session.pending_requests ?? 0), 0);
  const tokens = data.usage.reduce((sum, usage) => sum + (usage.usage?.total_tokens ?? 0), 0);
  const requests = data.usage.reduce((sum, usage) => sum + (usage.usage?.requests ?? 0), 0);
  const authRisk = data.providers
    .filter((provider) => ["refresh_soon", "expired", "revoked", "conflict", "unavailable"].includes(provider.auth?.status || ""))
    .filter((provider) => hasText(provider, search));
  const quotaRisk = data.quotas.filter((quota) => quotaPressure(quota) >= 0.7).filter((quota) => hasText(quota, search));
  const recentFailures = data.traces.filter((trace) => trace.status !== "completed").filter((trace) => hasText(trace, search)).slice(0, 12);

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
    { id: "model", header: "Model", sortValue: (row) => row.model, cell: (row) => <span className="mono">{middleEllipsis(row.model, 22, 10)}</span> },
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

  const failureColumns: DashboardColumn<RequestTrace>[] = [
    { id: "time", header: "Time", sortValue: (row) => row.started_at, cell: (row) => fmtTime(row.started_at), width: "126px" },
    { id: "request", header: "Request", sortValue: (row) => row.request_id, cell: (row) => <span className="mono">{middleEllipsis(row.request_id)}</span> },
    { id: "model", header: "Model", sortValue: (row) => row.route_request?.model, cell: (row) => row.route_request?.model || "" },
    { id: "status", header: "Status", sortValue: (row) => row.status, cell: (row) => <StatusBadge value={row.status} />, width: "128px" },
    { id: "error", header: "Error", sortValue: (row) => row.error || row.decision?.reason, cell: (row) => row.error || row.decision?.reason || row.error_code || "" },
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

          <Section title="Capacity Matrix" subtitle="Grouped by service and reported model" error={queries.providers.error}>
            <DataTable rows={capacity} columns={capacityColumns} empty="No provider capacity is available" compact />
          </Section>

          <Section title="Recent Route Failures" subtitle="Rejected, failed, and upstream-error traces" error={queries.traces.error}>
            <DataTable rows={recentFailures} columns={failureColumns} empty="No recent route failures" compact />
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
    </div>
  );
}

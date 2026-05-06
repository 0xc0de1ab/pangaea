import { useMemo, useState } from "react";
import { Clipboard, RefreshCw } from "lucide-react";
import type { DashboardViewProps } from "../app/dashboard";
import { DataTable, type DashboardColumn } from "../components/DataTable";
import { Drawer } from "../components/Drawer";
import { Section } from "../components/Section";
import { StatusBadge } from "../components/StatusBadge";
import { accountLabel, age, copyText, fmtTime, hasText, middleEllipsis, n } from "../lib/format";
import type { RequestTrace } from "../lib/types";

export function RequestsView({ data, queries, search, refresh }: DashboardViewProps) {
  const [selected, setSelected] = useState<RequestTrace | null>(null);
  const rows = useMemo(() => data.traces.filter((trace) => hasText(trace, search)), [data.traces, search]);

  const columns: DashboardColumn<RequestTrace>[] = [
    { id: "time", header: "Time", sortValue: (row) => row.started_at, cell: (row) => fmtTime(row.started_at), width: "128px" },
    {
      id: "request",
      header: "Request ID",
      sortValue: (row) => row.request_id,
      cell: (row) => (
        <span className="id-cell">
          <span className="mono">{middleEllipsis(row.request_id, 12, 8)}</span>
          <button className="mini-icon" type="button" aria-label="Copy request id" onClick={(event) => { event.stopPropagation(); copyText(row.request_id); }}>
            <Clipboard aria-hidden="true" size={13} />
          </button>
        </span>
      ),
      width: "200px",
    },
    { id: "protocol", header: "Protocol", sortValue: (row) => row.route_request?.api_dialect, cell: (row) => row.route_request?.api_dialect || "", width: "104px" },
    { id: "model", header: "Model", sortValue: (row) => row.route_request?.model, cell: (row) => row.route_request?.model || "" },
    { id: "provider", header: "Provider", sortValue: (row) => row.provider?.provider_instance_id, cell: (row) => <span className="mono">{middleEllipsis(row.provider?.provider_instance_id || "")}</span> },
    { id: "host", header: "Host", sortValue: (row) => row.provider?.host_name, cell: (row) => row.provider?.host_name || "", width: "130px" },
    { id: "account", header: "Account", sortValue: (row) => accountLabel(row.provider?.account), cell: (row) => accountLabel(row.provider?.account), width: "170px" },
    { id: "status", header: "Status", sortValue: (row) => row.status, cell: (row) => <StatusBadge value={row.status} />, width: "128px" },
    { id: "duration", header: "Duration", sortValue: (row) => row.duration_ms ?? 0, cell: (row) => `${n(row.duration_ms ?? 0)} ms`, align: "right", width: "104px" },
    { id: "tokens", header: "Tokens", sortValue: (row) => row.actual_usage?.tokens ?? row.estimated_usage?.tokens ?? 0, cell: (row) => n(row.actual_usage?.tokens ?? row.estimated_usage?.tokens ?? 0), align: "right", width: "92px" },
  ];

  return (
    <div className="view-stack">
      <Section
        title="Request Traces"
        subtitle="Recent route decisions, quota accounting, and upstream outcomes"
        error={queries.traces.error}
        actions={
          <button className="button secondary" type="button" onClick={refresh}>
            <RefreshCw aria-hidden="true" size={15} />
            Refresh
          </button>
        }
      >
        <DataTable rows={rows} columns={columns} empty="No traces" getRowId={(row) => row.request_id} onRowClick={setSelected} compact />
      </Section>

      <Drawer open={!!selected} onClose={() => setSelected(null)} title={selected ? middleEllipsis(selected.request_id, 18, 12) : "Trace"} subtitle={selected?.route_request?.model}>
        {selected ? <TraceDetail trace={selected} /> : null}
      </Drawer>
    </div>
  );
}

function TraceDetail({ trace }: { trace: RequestTrace }) {
  return (
    <div className="detail-stack">
      <div className="badge-row">
        <StatusBadge value={trace.status} />
        <StatusBadge value={trace.route_request?.api_dialect} tone="unknown" />
        {trace.route_request?.stream ? <StatusBadge value="stream" tone="ok" /> : <StatusBadge value="non-stream" tone="unknown" />}
      </div>
      <div className="detail-section">
        <h3>Routing</h3>
        <div className="kv-list">
          <div className="kv-key">Route</div><div className="kv-value mono">{trace.decision?.route_id || ""}</div>
          <div className="kv-key">Selected</div><div className="kv-value mono">{trace.decision?.selected || trace.provider?.provider_instance_id || ""}</div>
          <div className="kv-key">Canonical model</div><div className="kv-value mono">{trace.decision?.canonical_model || ""}</div>
          <div className="kv-key">Reason</div><div className="kv-value">{trace.decision?.reason || trace.error || ""}</div>
        </div>
      </div>
      <div className="detail-section">
        <h3>Provider</h3>
        <div className="kv-list">
          <div className="kv-key">Instance</div><div className="kv-value mono">{trace.provider?.provider_instance_id || ""}</div>
          <div className="kv-key">Service</div><div className="kv-value">{trace.provider?.service || ""}</div>
          <div className="kv-key">Host</div><div className="kv-value">{trace.provider?.host_name || ""}</div>
          <div className="kv-key">Account</div><div className="kv-value">{accountLabel(trace.provider?.account)}</div>
        </div>
      </div>
      <div className="detail-section">
        <h3>Timing And Usage</h3>
        <div className="kv-list">
          <div className="kv-key">Started</div><div className="kv-value">{fmtTime(trace.started_at)}</div>
          <div className="kv-key">Completed</div><div className="kv-value">{fmtTime(trace.completed_at)}</div>
          <div className="kv-key">Age</div><div className="kv-value">{age(trace.completed_at || trace.started_at)}</div>
          <div className="kv-key">Duration</div><div className="kv-value">{n(trace.duration_ms ?? 0)} ms</div>
          <div className="kv-key">Estimated tokens</div><div className="kv-value">{n(trace.estimated_usage?.tokens ?? 0)}</div>
          <div className="kv-key">Actual tokens</div><div className="kv-value">{n(trace.actual_usage?.tokens ?? 0)}</div>
        </div>
      </div>
    </div>
  );
}

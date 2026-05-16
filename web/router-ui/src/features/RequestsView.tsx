import { useCallback, useEffect, useMemo, useState } from "react";
import { ChevronLeft, ChevronRight, Clipboard, Download, RefreshCw, Trash2 } from "lucide-react";
import type { DashboardViewProps } from "../app/dashboard";
import { DataTable, type DashboardColumn } from "../components/DataTable";
import { Drawer } from "../components/Drawer";
import { Section } from "../components/Section";
import { ProtocolIcon, ServiceIcon } from "../components/ServiceIcon";
import { StatusBadge } from "../components/StatusBadge";
import { api } from "../lib/api";
import { accountLabel, age, copyText, fmtTime, hasText, middleEllipsis, n } from "../lib/format";
import { protocolLabel, type ProviderProtocol } from "../lib/protocols";
import type { RequestTrace, RequestTraceHTTPBody, RequestTracePage } from "../lib/types";

const tracePageSize = 25;

export function RequestsView({ data, queries, search, token, onAction, refresh }: DashboardViewProps) {
  const [selected, setSelected] = useState<RequestTrace | null>(null);
  const [selectedIDs, setSelectedIDs] = useState<Set<string>>(() => new Set());
  const [pageIndex, setPageIndex] = useState(0);
  const [page, setPage] = useState<RequestTracePage>(() => ({ traces: data.traces, total: data.traces.length, limit: tracePageSize, offset: 0, has_more: false }));
  const [pageLoading, setPageLoading] = useState(false);
  const [pageError, setPageError] = useState<Error | null>(null);

  const loadPage = useCallback(async (index: number) => {
    setPageLoading(true);
    setPageError(null);
    try {
      const next = await api.tracePage(token, tracePageSize, index * tracePageSize);
      setPage(next);
      setSelectedIDs(new Set());
    } catch (err) {
      setPageError(err instanceof Error ? err : new Error("Trace page failed to load"));
    } finally {
      setPageLoading(false);
    }
  }, [token]);

  useEffect(() => {
    void loadPage(pageIndex);
  }, [loadPage, pageIndex]);

  useEffect(() => {
    setPageIndex(0);
  }, [search]);

  const rows = useMemo(() => page.traces.filter((trace) => hasText(trace, search)), [page.traces, search]);
  const selectedCount = selectedIDs.size;
  const allVisibleSelected = rows.length > 0 && rows.every((row) => selectedIDs.has(row.request_id));
  const pageCount = Math.max(1, Math.ceil((page.total || rows.length) / tracePageSize));

  function toggleSelected(requestID: string, checked: boolean) {
    setSelectedIDs((current) => {
      const next = new Set(current);
      if (checked) {
        next.add(requestID);
      } else {
        next.delete(requestID);
      }
      return next;
    });
  }

  function toggleVisible(checked: boolean) {
    setSelectedIDs((current) => {
      const next = new Set(current);
      for (const row of rows) {
        if (checked) {
          next.add(row.request_id);
        } else {
          next.delete(row.request_id);
        }
      }
      return next;
    });
  }

  function deleteSelected() {
    const ids = [...selectedIDs];
    if (!ids.length) return;
    onAction({
      title: "Delete request traces",
      target: ids.length === 1 ? ids[0] : `${ids.length} traces`,
      detail: "Selected trace records and their captured HTTP request/response snapshots will be removed from router memory.",
      requireReason: true,
      danger: true,
      confirmLabel: "Delete traces",
      execute: async () => {
        await api.deleteTraces(ids, token);
        setSelectedIDs(new Set());
        setSelected((trace) => trace && ids.includes(trace.request_id) ? null : trace);
        await loadPage(pageIndex);
        refresh();
      },
    });
  }

  const columns: DashboardColumn<RequestTrace>[] = [
    {
      id: "select",
      header: "Sel",
      headerAction: {
        disabled: rows.length === 0,
        onLongPress: () => toggleVisible(!allVisibleSelected),
        pressed: allVisibleSelected,
        title: allVisibleSelected ? "Long press to clear this page selection" : "Long press to select this page",
      },
      cell: (row) => (
        <input
          type="checkbox"
          aria-label={`Select ${row.request_id}`}
          checked={selectedIDs.has(row.request_id)}
          onChange={(event) => toggleSelected(row.request_id, event.currentTarget.checked)}
          onClick={(event) => event.stopPropagation()}
        />
      ),
      width: "48px",
      align: "center",
    },
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
    { id: "protocol", header: "API", sortValue: (row) => row.route_request?.api_dialect, cell: (row) => <ProtocolCell value={row.route_request?.api_dialect} />, align: "center", width: "58px" },
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
        subtitle={`${n(page.total)} traces retained, ${n(rows.length)} shown on this page`}
        error={pageError || queries.traces.error}
        actions={
          <>
            <button className="button danger" type="button" disabled={selectedCount === 0} onClick={deleteSelected}>
              <Trash2 aria-hidden="true" size={15} />
              Delete {selectedCount ? n(selectedCount) : ""}
            </button>
            <button className="button secondary" type="button" onClick={() => { refresh(); void loadPage(pageIndex); }}>
              <RefreshCw aria-hidden="true" className={pageLoading ? "spin" : undefined} size={15} />
              Refresh
            </button>
          </>
        }
      >
        <DataTable rows={rows} columns={columns} empty="No traces" getRowId={(row) => row.request_id} onRowClick={setSelected} compact />
        <div className="table-pagination">
          <span>
            Page {n(pageIndex + 1)} / {n(pageCount)}
          </span>
          <div className="pagination-actions">
            <button className="icon-button small" type="button" aria-label="Previous page" disabled={pageIndex === 0 || pageLoading} onClick={() => setPageIndex((value) => Math.max(0, value - 1))}>
              <ChevronLeft aria-hidden="true" size={15} />
            </button>
            <button className="icon-button small" type="button" aria-label="Next page" disabled={!page.has_more || pageLoading} onClick={() => setPageIndex((value) => value + 1)}>
              <ChevronRight aria-hidden="true" size={15} />
            </button>
          </div>
        </div>
      </Section>

      <Drawer open={!!selected} onClose={() => setSelected(null)} title={selected ? middleEllipsis(selected.request_id, 18, 12) : "Trace"} subtitle={selected?.route_request?.model}>
        {selected ? <TraceDetail trace={selected} /> : null}
      </Drawer>
    </div>
  );
}

function ProtocolCell({ value }: { value?: string }) {
  const label = traceProtocolLabel(value);
  if (isProviderProtocol(value)) {
    return (
      <span className="trace-protocol-icon" title={label}>
        <ProtocolIcon protocol={value} size={20} label={label} />
      </span>
    );
  }
  return (
    <span className="trace-protocol-icon" title={label}>
      <ServiceIcon service={value || "unknown"} size={20} label={label} />
    </span>
  );
}

function isProviderProtocol(value?: string): value is ProviderProtocol {
  return value === "openai" || value === "anthropic" || value === "gemini";
}

function traceProtocolLabel(value?: string) {
  switch (value) {
    case "openai":
    case "anthropic":
    case "gemini":
      return protocolLabel(value as ProviderProtocol);
    default:
      return value || "Unknown API";
  }
}

function TraceDetail({ trace }: { trace: RequestTrace }) {
  return (
    <div className="detail-stack">
      <div className="drawer-action-row">
        <button className="button secondary" type="button" onClick={() => downloadTraceJSON(trace)}>
          <Download aria-hidden="true" size={15} />
          Save JSON
        </button>
      </div>
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
      <HTTPExchange trace={trace} />
    </div>
  );
}

function HTTPExchange({ trace }: { trace: RequestTrace }) {
  if (!trace.http) {
    return (
      <div className="detail-section">
        <h3>HTTP Exchange</h3>
        <div className="chat-empty">No HTTP request/response snapshot captured</div>
      </div>
    );
  }
  return (
    <div className="detail-section">
      <h3>HTTP Exchange</h3>
      <div className="trace-http-grid">
        <HTTPMessage title="Request" summary={`${trace.http.request.method} ${trace.http.request.path}${trace.http.request.query ? `?${trace.http.request.query}` : ""}`} headers={trace.http.request.headers} body={trace.http.request.body} />
        <HTTPMessage title="Response" summary={`HTTP ${trace.http.response.status}`} headers={trace.http.response.headers} body={trace.http.response.body} />
      </div>
    </div>
  );
}

function HTTPMessage({ title, summary, headers, body }: { title: string; summary: string; headers?: Record<string, string[]>; body?: RequestTraceHTTPBody }) {
  return (
    <article className="trace-http-message">
      <div className="trace-http-title">
        <strong>{title}</strong>
        <span className="mono">{summary}</span>
      </div>
      <pre className="trace-http-block">{formatHeaders(headers)}</pre>
      <HTTPBody body={body} />
    </article>
  );
}

function HTTPBody({ body }: { body?: RequestTraceHTTPBody }) {
  if (!body) {
    return <div className="chat-empty compact">No body</div>;
  }
  const label = body.jsonl?.length ? "JSONL" : body.json !== undefined ? "JSON" : "Text";
  return (
    <div className="trace-http-body">
      <div className="trace-http-body-head">
        <span>{label}</span>
        {body.content_type ? <span className="mono">{body.content_type}</span> : null}
        {body.truncated ? <StatusBadge value="truncated" tone="warn" /> : null}
      </div>
      {body.jsonl?.length ? (
        <div className="trace-jsonl-list">
          {body.jsonl.map((entry, index) => (
            <pre className="trace-http-block" key={index}>{formatJSON(entry)}</pre>
          ))}
        </div>
      ) : (
        <pre className="trace-http-block">{body.json !== undefined ? formatJSON(body.json) : body.text || ""}</pre>
      )}
    </div>
  );
}

function formatHeaders(headers?: Record<string, string[]>) {
  if (!headers || Object.keys(headers).length === 0) {
    return "(no headers)";
  }
  return Object.entries(headers)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, values]) => `${key}: ${values.join(", ")}`)
    .join("\n");
}

function formatJSON(value: unknown) {
  if (typeof value === "string") {
    try {
      return JSON.stringify(JSON.parse(value), null, 2);
    } catch {
      return value;
    }
  }
  return JSON.stringify(value, null, 2);
}

function downloadTraceJSON(trace: RequestTrace) {
  const blob = new Blob([JSON.stringify(trace, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `${trace.request_id || "request-trace"}.json`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

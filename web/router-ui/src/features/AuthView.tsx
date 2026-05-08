import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Clipboard, Download, History, KeyRound, RefreshCw, Server } from "lucide-react";
import type { DashboardViewProps } from "../app/dashboard";
import { DataTable, type DashboardColumn } from "../components/DataTable";
import { Drawer } from "../components/Drawer";
import { Section } from "../components/Section";
import { StatusBadge } from "../components/StatusBadge";
import { api } from "../lib/api";
import { age, copyText, fmtTime, hasText, middleEllipsis, n } from "../lib/format";
import type { AuthEvent, AuthRecord, AuthReplica } from "../lib/types";

export function AuthView({ data, queries, search, token, refresh }: DashboardViewProps) {
  const [selected, setSelected] = useState<AuthRecord | null>(null);
  const rows = useMemo(() => {
    return [...data.auth]
      .sort((a, b) => `${a.service}:${authAccountLabel(a)}:${a.id}`.localeCompare(`${b.service}:${authAccountLabel(b)}:${b.id}`))
      .filter((record) => hasText(record, search));
  }, [data.auth, search]);

  const columns: DashboardColumn<AuthRecord>[] = [
    {
      id: "account",
      header: "Account",
      sortValue: authAccountLabel,
      cell: (row) => (
        <span className="id-cell">
          <KeyRound aria-hidden="true" size={15} />
          <span>{authAccountLabel(row)}</span>
        </span>
      ),
      width: "260px",
    },
    { id: "service", header: "Service", sortValue: (row) => row.service, cell: (row) => row.service, width: "118px" },
    { id: "status", header: "Auth", sortValue: (row) => row.status, cell: (row) => <StatusBadge value={row.status} title={row.last_refresh_error} />, width: "130px" },
    { id: "host", header: "Latest Host", sortValue: (row) => row.host_name, cell: (row) => row.host_name || "", width: "150px" },
    { id: "provider", header: "Latest Provider", sortValue: (row) => row.provider_instance_id, cell: (row) => <span className="mono">{middleEllipsis(row.provider_instance_id || "", 12, 8)}</span>, width: "180px" },
    { id: "expires", header: "Expires", sortValue: (row) => row.expires_at, cell: (row) => fmtTime(row.expires_at), width: "150px" },
    { id: "updated", header: "Observed", sortValue: (row) => row.updated_at, cell: (row) => age(row.updated_at), width: "110px" },
    { id: "replicas", header: "Replicas", sortValue: (row) => row.replicas?.length ?? 0, cell: (row) => n(row.replicas?.length ?? 0), align: "right", width: "90px" },
    {
      id: "fingerprint",
      header: "Fingerprint",
      sortValue: (row) => row.fingerprint,
      cell: (row) => (
        <span className="id-cell">
          <span className="mono">{middleEllipsis(row.fingerprint || "", 10, 8)}</span>
          {row.fingerprint ? (
            <button className="mini-icon" type="button" aria-label="Copy fingerprint" onClick={(event) => { event.stopPropagation(); copyText(row.fingerprint || ""); }}>
              <Clipboard aria-hidden="true" size={13} />
            </button>
          ) : null}
        </span>
      ),
      width: "190px",
    },
    {
      id: "download",
      header: "File",
      sortValue: (row) => row.filename,
      cell: (row) => (
        <div className="row-actions">
          <span className="mono">{row.filename}</span>
          <button className="icon-button small" type="button" title="Download latest auth file" disabled={!row.has_download} onClick={(event) => { event.stopPropagation(); void downloadAuth(row, token); }}>
            <Download aria-hidden="true" size={15} />
          </button>
        </div>
      ),
      width: "170px",
    },
  ];

  return (
    <div className="view-stack">
      <Section
        title="Auth"
        subtitle="Latest shared auth per service account, with download and propagation history"
        error={queries.auth.error}
        actions={
          <button className="button secondary" type="button" onClick={refresh}>
            <RefreshCw aria-hidden="true" size={15} />
            Refresh
          </button>
        }
      >
        <DataTable rows={rows} columns={columns} empty="No auth snapshots reported" getRowId={(row) => row.id} onRowClick={setSelected} compact />
      </Section>

      <Drawer
        open={!!selected}
        onClose={() => setSelected(null)}
        title={selected ? authAccountLabel(selected) : "Auth"}
        subtitle={selected ? `${selected.service} · ${selected.filename}` : undefined}
      >
        {selected ? <AuthDetail record={selected} token={token} /> : null}
      </Drawer>
    </div>
  );
}

function AuthDetail({ record, token }: { record: AuthRecord; token?: string }) {
  const events = useQuery({
    queryKey: ["auth-events", record.id, token],
    queryFn: () => api.authEvents(record.id, token),
    refetchInterval: 15_000,
  });
  return (
    <div className="detail-stack auth-detail">
      <div className="drawer-action-row">
        <button className="button secondary" type="button" disabled={!record.has_download} onClick={() => void downloadAuth(record, token)}>
          <Download aria-hidden="true" size={15} />
          Download {record.filename}
        </button>
      </div>

      <div className="detail-section">
        <h3>Latest</h3>
        <div className="badge-row">
          <StatusBadge value={record.status} title={record.last_refresh_error} />
          <StatusBadge value={record.has_download ? "download ready" : "metadata only"} tone={record.has_download ? "ok" : "warn"} />
          <StatusBadge value={`${n(record.replicas?.length ?? 0)} replicas`} tone="unknown" />
        </div>
        <div className="kv-list">
          <div className="kv-key">Auth ID</div><div className="kv-value mono">{record.id}</div>
          <div className="kv-key">Account</div><div className="kv-value">{authAccountLabel(record)}</div>
          <div className="kv-key">Service</div><div className="kv-value">{record.service}</div>
          <div className="kv-key">Format</div><div className="kv-value mono">{record.format || ""}</div>
          <div className="kv-key">Filename</div><div className="kv-value mono">{record.filename}</div>
          <div className="kv-key">Fingerprint</div><div className="kv-value mono">{record.fingerprint || ""}</div>
          <div className="kv-key">Source</div><div className="kv-value">{record.source || record.selected_source || ""}</div>
          <div className="kv-key">Expires</div><div className="kv-value">{fmtTime(record.expires_at)}</div>
          <div className="kv-key">Last refresh</div><div className="kv-value">{fmtTime(record.last_refresh_at)}</div>
          <div className="kv-key">Observed</div><div className="kv-value">{fmtTime(record.observed_at || record.updated_at)}</div>
        </div>
      </div>

      <div className="detail-section">
        <h3>Replicas</h3>
        <AuthReplicaTable replicas={record.replicas ?? []} />
      </div>

      <div className="detail-section">
        <h3>History</h3>
        {events.isError ? <div className="inline-error">Failed to load auth history</div> : <AuthTimeline events={events.data ?? []} />}
      </div>
    </div>
  );
}

function AuthReplicaTable({ replicas }: { replicas: AuthReplica[] }) {
  if (!replicas.length) {
    return <div className="chat-empty">No replicas reported</div>;
  }
  return (
    <div className="table-frame table-compact auth-replica-table">
      <table>
        <thead>
          <tr>
            <th>Host</th>
            <th>Node</th>
            <th>Provider</th>
            <th>Status</th>
            <th>Observed</th>
          </tr>
        </thead>
        <tbody>
          {replicas.map((replica) => (
            <tr key={replica.provider_instance_id}>
              <td>{replica.host_name || ""}</td>
              <td className="mono">{replica.node_id || ""}</td>
              <td className="mono">{replica.provider_instance_id}</td>
              <td><StatusBadge value={replica.status} /></td>
              <td>{age(replica.updated_at || replica.observed_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function AuthTimeline({ events }: { events: AuthEvent[] }) {
  if (!events.length) {
    return <div className="chat-empty">No auth events</div>;
  }
  return (
    <div className="auth-timeline">
      {events.map((event) => (
        <article className="auth-event" key={event.id}>
          <div className="auth-event-icon">
            {event.type.includes("push") ? <Server aria-hidden="true" size={15} /> : <History aria-hidden="true" size={15} />}
          </div>
          <div>
            <div className="auth-event-head">
              <strong>{event.type}</strong>
              <span>{fmtTime(event.at)}</span>
            </div>
            <p>{event.message || authEventMessage(event)}</p>
            <div className="auth-event-meta">
              <span>{event.host_name || ""}</span>
              <span className="mono">{event.provider_instance_id || ""}</span>
              {event.status ? <StatusBadge value={event.status} /> : null}
            </div>
          </div>
        </article>
      ))}
    </div>
  );
}

async function downloadAuth(record: AuthRecord, token?: string) {
  const blob = await api.authDownload(record.id, token);
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = record.filename || "auth.json";
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

function authAccountLabel(record: AuthRecord) {
  return record.account?.display || record.account?.id || record.provider_instance_id || record.id;
}

function authEventMessage(event: AuthEvent) {
  if (event.type === "auth.snapshot") {
    return "A node reported an observed auth snapshot.";
  }
  if (event.type === "auth.refresh.result") {
    return "A provider completed auth refresh.";
  }
  if (event.type === "auth.push.sent") {
    return "Router pushed latest auth state to a provider.";
  }
  if (event.type === "auth.download") {
    return "Operator downloaded the latest auth file.";
  }
  return "Auth state changed.";
}

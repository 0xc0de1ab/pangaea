import { FormEvent, useMemo, useState } from "react";
import { KeyRound, RefreshCw, Trash2 } from "lucide-react";
import type { DashboardViewProps } from "../app/dashboard";
import { DataTable, type DashboardColumn } from "../components/DataTable";
import { Section } from "../components/Section";
import { StatusBadge } from "../components/StatusBadge";
import { api } from "../lib/api";
import { auditTarget, quotaPressure, quotaPressureLabel } from "../lib/derive";
import { accountLabel, fmtTime, hasText, middleEllipsis, n, scopeLabel } from "../lib/format";
import type { APIKeyPrincipal, AuditEvent, QuotaSnapshot } from "../lib/types";

export function AdminView({ data, queries, search, token, onAction, refresh }: DashboardViewProps) {
  const [tenantID, setTenantID] = useState("");
  const [userID, setUserID] = useState("");
  const [createdRawKey, setCreatedRawKey] = useState("");
  const [createError, setCreateError] = useState("");
  const apiKeys = useMemo(() => data.apiKeys.filter((key) => hasText(key, search)), [data.apiKeys, search]);
  const quotas = useMemo(() => data.quotas.filter((quota) => hasText(quota, search)), [data.quotas, search]);
  const audit = useMemo(() => data.audit.filter((event) => hasText(event, search)), [data.audit, search]);

  async function createKey(event: FormEvent) {
    event.preventDefault();
    setCreateError("");
    setCreatedRawKey("");
    try {
      const response = await api.createAPIKey({ tenant_id: tenantID.trim(), user_id: userID.trim() }, token);
      setCreatedRawKey(response.raw_key || "");
      refresh();
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : "API key create failed");
    }
  }

  function deleteKey(key: APIKeyPrincipal) {
    onAction({
      title: "Delete API key",
      target: key.id,
      detail: `Requests using ${key.prefix} will stop authenticating after deletion.`,
      requireReason: true,
      danger: true,
      confirmLabel: "Delete key",
      execute: async () => {
        await api.deleteAPIKey(key.id, token);
        refresh();
      },
    });
  }

  const keyColumns: DashboardColumn<APIKeyPrincipal>[] = [
    { id: "id", header: "Key ID", sortValue: (row) => row.id, cell: (row) => <span className="mono">{middleEllipsis(row.id)}</span> },
    { id: "prefix", header: "Prefix", sortValue: (row) => row.prefix, cell: (row) => <span className="mono">{row.prefix}</span>, width: "120px" },
    { id: "tenant", header: "Tenant", sortValue: (row) => row.tenant_id, cell: (row) => row.tenant_id || "", width: "120px" },
    { id: "user", header: "User", sortValue: (row) => row.user_id, cell: (row) => row.user_id || "", width: "140px" },
    { id: "state", header: "State", sortValue: (row) => row.disabled ? "disabled" : "active", cell: (row) => <StatusBadge value={row.disabled ? "disabled" : "active"} tone={row.disabled ? "danger" : "ok"} />, width: "110px" },
    { id: "expires", header: "Expires", sortValue: (row) => row.expires_at, cell: (row) => fmtTime(row.expires_at), width: "132px" },
    {
      id: "actions",
      header: "Actions",
      cell: (row) => (
        <button className="icon-button small" type="button" title="Delete key" onClick={() => deleteKey(row)}>
          <Trash2 aria-hidden="true" size={15} />
        </button>
      ),
      width: "84px",
    },
  ];

  const quotaColumns: DashboardColumn<QuotaSnapshot>[] = [
    { id: "scope", header: "Scope", sortValue: (row) => scopeLabel(row.scope), cell: (row) => <span className="mono">{middleEllipsis(scopeLabel(row.scope), 28, 12)}</span> },
    { id: "pressure", header: "Pressure", sortValue: (row) => quotaPressure(row), cell: (row) => <StatusBadge value={quotaPressureLabel(row)} tone={quotaPressure(row) >= 1 ? "danger" : quotaPressure(row) >= 0.8 ? "warn" : "ok"} />, width: "112px" },
    { id: "committed", header: "Committed", sortValue: (row) => row.committed?.tokens ?? 0, cell: (row) => `${n(row.committed?.tokens ?? 0)} tok / ${n(row.committed?.requests ?? 0)} req`, align: "right", width: "150px" },
    { id: "reserved", header: "Reserved", sortValue: (row) => row.reserved?.tokens ?? 0, cell: (row) => `${n(row.reserved?.tokens ?? 0)} tok / ${n(row.reserved?.requests ?? 0)} req`, align: "right", width: "150px" },
    { id: "limit", header: "Limit", sortValue: (row) => row.limit?.max_tokens ?? 0, cell: (row) => `${n(row.limit?.max_tokens ?? 0)} tok / ${n(row.limit?.max_requests ?? 0)} req`, align: "right", width: "150px" },
  ];

  const auditColumns: DashboardColumn<AuditEvent>[] = [
    { id: "time", header: "Time", sortValue: (row) => row.created_at, cell: (row) => fmtTime(row.created_at), width: "128px" },
    { id: "type", header: "Type", sortValue: (row) => row.type, cell: (row) => row.type },
    { id: "target", header: "Target", sortValue: auditTarget, cell: (row) => <span className="mono">{middleEllipsis(auditTarget(row))}</span> },
    { id: "actor", header: "Actor", sortValue: (row) => row.actor?.api_key_id || row.actor?.user_id, cell: (row) => row.actor?.api_key_id || row.actor?.user_id || row.actor?.source || "" },
    { id: "outcome", header: "Outcome", sortValue: (row) => row.outcome, cell: (row) => <StatusBadge value={row.outcome} />, width: "120px" },
    { id: "reason", header: "Reason", sortValue: (row) => row.reason, cell: (row) => row.reason || row.error || "" },
  ];

  return (
    <div className="view-stack">
      <Section
        title="API Keys"
        subtitle="Public/admin key store currently shares one backend; raw keys are one-time display only"
        error={queries.apiKeys.error}
        actions={
          <button className="button secondary" type="button" onClick={refresh}>
            <RefreshCw aria-hidden="true" size={15} />
            Refresh
          </button>
        }
      >
        <form className="admin-form" onSubmit={createKey}>
          <label className="field">
            <span>Tenant</span>
            <input value={tenantID} onChange={(event) => setTenantID(event.target.value)} autoComplete="off" />
          </label>
          <label className="field">
            <span>User</span>
            <input value={userID} onChange={(event) => setUserID(event.target.value)} autoComplete="off" />
          </label>
          <button className="button primary" type="submit">
            <KeyRound aria-hidden="true" size={15} />
            Create key
          </button>
        </form>
        {createError ? <div className="inline-error">{createError}</div> : null}
        {createdRawKey ? (
          <div className="one-time-secret">
            <strong>One-time raw key</strong>
            <span className="mono">{createdRawKey}</span>
          </div>
        ) : null}
        <DataTable rows={apiKeys} columns={keyColumns} empty="No API keys" compact />
      </Section>

      <Section title="Quotas" subtitle="Router-side committed and reserved usage" error={queries.quotas.error}>
        <DataTable rows={quotas} columns={quotaColumns} empty="No quota snapshots" compact />
      </Section>

      <Section title="Audit Events" subtitle="Recent administrative actions and outcomes" error={queries.audit.error}>
        <DataTable rows={audit} columns={auditColumns} empty="No audit events" compact />
      </Section>
    </div>
  );
}

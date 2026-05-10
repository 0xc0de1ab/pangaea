import { FormEvent, useMemo, useState } from "react";
import { Play, RefreshCw } from "lucide-react";
import type { DashboardViewProps } from "../app/dashboard";
import { DataTable, type DashboardColumn } from "../components/DataTable";
import { Section } from "../components/Section";
import { StatusBadge } from "../components/StatusBadge";
import { api } from "../lib/api";
import { capacityRows } from "../lib/derive";
import { hasText, middleEllipsis, n } from "../lib/format";
import type { RouteDecision, RouteRequest } from "../lib/types";

export function RoutesView({ data, queries, search, token, refresh }: DashboardViewProps) {
  const [model, setModel] = useState(data.models[0]?.id || "");
  const [dialect, setDialect] = useState<RouteRequest["api_dialect"]>("openai");
  const [stream, setStream] = useState(true);
  const [decision, setDecision] = useState<RouteDecision | null>(null);
  const [error, setError] = useState("");
  const [running, setRunning] = useState(false);
  const capacity = useMemo(() => capacityRows(data.providers).filter((row) => hasText(row, search)), [data.providers, search]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setRunning(true);
    setError("");
    setDecision(null);
    try {
      const result = await api.dryRun({ model: model.trim(), api_dialect: dialect, stream }, token);
      setDecision(result);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Dry run failed");
    } finally {
      setRunning(false);
    }
  }

  const capacityColumns: DashboardColumn<(typeof capacity)[number]>[] = [
    { id: "service", header: "Service", sortValue: (row) => row.service, cell: (row) => row.service, width: "110px" },
    { id: "model", header: "Model", sortValue: (row) => row.model, cell: (row) => <span className="mono">{middleEllipsis(row.model, 26, 12)}</span> },
    { id: "hosts", header: "Hosts", sortValue: (row) => row.hosts.size, cell: (row) => n(row.hosts.size), align: "right", width: "76px" },
    { id: "providers", header: "Providers", sortValue: (row) => row.providers, cell: (row) => n(row.providers), align: "right", width: "92px" },
    { id: "ready", header: "Ready", sortValue: (row) => row.ready, cell: (row) => n(row.ready), align: "right", width: "76px" },
    { id: "degraded", header: "Degraded", sortValue: (row) => row.degraded, cell: (row) => n(row.degraded), align: "right", width: "92px" },
    { id: "down", header: "Down", sortValue: (row) => row.down, cell: (row) => n(row.down), align: "right", width: "76px" },
    { id: "queue", header: "Queue", sortValue: (row) => row.queueDepth, cell: (row) => n(row.queueDepth), align: "right", width: "76px" },
  ];

  const scoreColumns: DashboardColumn<NonNullable<RouteDecision["scores"]>[number]>[] = [
    { id: "provider", header: "Provider", sortValue: (row) => row.provider_instance_id || row.provider_type, cell: (row) => <span className="mono">{middleEllipsis(row.provider_instance_id || row.provider_type || "")}</span> },
    { id: "score", header: "Score", sortValue: (row) => row.score, cell: (row) => n(row.score), align: "right", width: "76px" },
    { id: "weight", header: "Weight", sortValue: (row) => row.weight ?? 0, cell: (row) => n(row.weight ?? 0), align: "right", width: "82px" },
    { id: "reason", header: "Reason", sortValue: (row) => row.reason, cell: (row) => row.reason || "" },
  ];

  const rejectionColumns: DashboardColumn<NonNullable<RouteDecision["rejections"]>[number]>[] = [
    { id: "provider", header: "Provider", sortValue: (row) => row.provider_instance_id || row.provider_type, cell: (row) => <span className="mono">{middleEllipsis(row.provider_instance_id || row.provider_type || "")}</span> },
    { id: "reason", header: "Reason", sortValue: (row) => row.reason, cell: (row) => row.reason },
  ];

  return (
    <div className="view-stack">
      <Section
        title="Route Dry Run"
        subtitle="Validate model routing, stream eligibility, and provider rejection reasons"
        actions={
          <button className="button secondary" type="button" onClick={refresh}>
            <RefreshCw aria-hidden="true" size={15} />
            Refresh
          </button>
        }
      >
        <form className="dry-run-form" onSubmit={submit}>
          <label className="field">
            <span>Model</span>
            <input value={model} onChange={(event) => setModel(event.target.value)} placeholder="gpt-5-codex" />
          </label>
          <label className="field">
            <span>Protocol</span>
            <select value={dialect} onChange={(event) => setDialect(event.target.value)}>
              <option value="openai">OpenAI</option>
              <option value="anthropic">Anthropic</option>
              <option value="gemini">Gemini</option>
            </select>
          </label>
          <label className="check-row inline">
            <input type="checkbox" checked={stream} onChange={(event) => setStream(event.target.checked)} />
            <span>stream</span>
          </label>
          <button className="button primary" type="submit" disabled={running || !model.trim()}>
            <Play aria-hidden="true" size={15} />
            Dry run
          </button>
        </form>
        {error ? <div className="inline-error">{error}</div> : null}
        {decision ? (
          <div className="decision-panel">
            <div className="decision-head">
              <StatusBadge value={decision.allowed ? "allowed" : "rejected"} tone={decision.allowed ? "ok" : "danger"} />
              <span className="mono">{decision.route_id || "no route"}</span>
              <span>{decision.canonical_model || decision.model_alias || ""}</span>
              <strong>{decision.selected || decision.reason || ""}</strong>
            </div>
            <div className="split-grid">
              <Section title="Scores">
                <DataTable rows={decision.scores ?? []} columns={scoreColumns} empty="No score rows returned" compact />
              </Section>
              <Section title="Rejections">
                <DataTable rows={decision.rejections ?? []} columns={rejectionColumns} empty="No rejected candidates" compact />
              </Section>
            </div>
          </div>
        ) : null}
      </Section>

      <Section title="Capacity By Model" subtitle="Current provider pool grouped by service and reported model" error={queries.providers.error}>
        <DataTable rows={capacity} columns={capacityColumns} empty="No provider capacity is available" compact />
      </Section>
    </div>
  );
}

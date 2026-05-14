import type { DragEvent, FormEvent } from "react";
import { useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUp, Copy, GripVertical, Play, Plus, RefreshCw, Save, Trash2 } from "lucide-react";
import type { DashboardViewProps } from "../app/dashboard";
import { DataTable, type DashboardColumn } from "../components/DataTable";
import { Section } from "../components/Section";
import { StatusBadge } from "../components/StatusBadge";
import { api } from "../lib/api";
import { capacityRows } from "../lib/derive";
import { copyText, hasText, middleEllipsis, n } from "../lib/format";
import type { RouteDecision, RouteRequest, RoutingFilter, RoutingRule, RoutingRuleDryRunResponse, RoutingRuleStep } from "../lib/types";

type DraftRule = Omit<RoutingRule, "id" | "created_at" | "updated_at"> & { id?: string };

const emptyFilter: RoutingFilter = {
  id: "any",
  type: "any",
  label: "Any available provider",
  criteria: {},
};

const emptyRule: DraftRule = {
  name: "default",
  scope: "public",
  owner_email: "",
  description: "",
  filters: [emptyFilter],
};

export function RoutesView({ data, queries, search, token, refresh }: DashboardViewProps) {
  const [draft, setDraft] = useState<DraftRule>(emptyRule);
  const [editingID, setEditingID] = useState("");
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [model, setModel] = useState(data.models[0]?.id || "");
  const [dialect, setDialect] = useState<RouteRequest["api_dialect"]>("openai");
  const [stream, setStream] = useState(true);
  const [dryRun, setDryRun] = useState<RoutingRuleDryRunResponse | null>(null);
  const [activeStep, setActiveStep] = useState(-1);
  const [error, setError] = useState("");
  const [running, setRunning] = useState(false);
  const [saving, setSaving] = useState(false);
  const capacity = useMemo(() => capacityRows(data.providers).filter((row) => hasText(row, search)), [data.providers, search]);
  const rules = useMemo(() => data.routingRules.filter((rule) => hasText(rule, search)), [data.routingRules, search]);
  const ruleNameValid = validRoutingRuleName(draft.name);

  useEffect(() => {
    if (model || data.models.length === 0) {
      return;
    }
    setModel(data.models[0]?.id || "");
  }, [data.models, model]);

  useEffect(() => {
    const steps = dryRun?.steps ?? [];
    if (steps.length === 0) {
      setActiveStep(-1);
      return;
    }
    setActiveStep(0);
    let index = 0;
    const timer = window.setInterval(() => {
      index += 1;
      if (index >= steps.length) {
        window.clearInterval(timer);
        return;
      }
      setActiveStep(index);
    }, 460);
    return () => window.clearInterval(timer);
  }, [dryRun]);

  function edit(rule: RoutingRule) {
    setEditingID(rule.id);
    setDraft({
      id: rule.id,
      name: rule.name,
      scope: rule.scope,
      owner_email: rule.owner_email || "",
      description: rule.description || "",
      filters: rule.filters?.length ? rule.filters : [emptyFilter],
    });
    setDryRun(null);
  }

  function resetDraft() {
    setEditingID("");
    setDraft(emptyRule);
    setDryRun(null);
  }

  async function saveRule(event: FormEvent) {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      const payload = materializeDraft(draft);
      if (editingID) {
        await api.updateRoutingRule(editingID, payload, token);
      } else {
        await api.createRoutingRule(payload, token);
      }
      resetDraft();
      refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save routing rule");
    } finally {
      setSaving(false);
    }
  }

  async function deleteRule(rule: RoutingRule) {
    if (!window.confirm(`Delete route rule ${rule.name}?`)) {
      return;
    }
    setError("");
    try {
      await api.deleteRoutingRule(rule.id, token);
      if (editingID === rule.id) {
        resetDraft();
      }
      refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete routing rule");
    }
  }

  async function submitDryRun(event: FormEvent) {
    event.preventDefault();
    setRunning(true);
    setError("");
    setDryRun(null);
    try {
      const result = await api.dryRunRoutingRule({
        rule: materializeDraft(draft),
        request: {
          model: model.trim(),
          api_dialect: dialect,
          stream,
          routing_rule_name: draft.name,
          routing_rule_owner: draft.owner_email,
        },
      }, token);
      setDryRun(result);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Dry run failed");
    } finally {
      setRunning(false);
    }
  }

  function updateFilter(index: number, patch: Partial<RoutingFilter>) {
    setDraft((value) => ({
      ...value,
      filters: value.filters.map((filter, i) => i === index ? { ...filter, ...patch } : filter),
    }));
  }

  function updateFilterCriteria(index: number, key: string, raw: string) {
    setDraft((value) => ({
      ...value,
      filters: value.filters.map((filter, i) => i === index ? {
        ...filter,
        criteria: {
          ...(filter.criteria ?? {}),
          [key]: splitCSV(raw),
        },
      } : filter),
    }));
  }

  function addFilter() {
    setDraft((value) => ({
      ...value,
      filters: [
        ...value.filters,
        { id: `filter-${value.filters.length + 1}`, type: "criteria", label: "Criteria", criteria: {} },
      ],
    }));
  }

  function removeFilter(index: number) {
    setDraft((value) => ({
      ...value,
      filters: value.filters.filter((_, i) => i !== index),
    }));
  }

  function moveFilter(from: number, to: number) {
    if (from === to || from < 0 || to < 0 || from >= draft.filters.length || to >= draft.filters.length) {
      return;
    }
    setDraft((value) => {
      const filters = [...value.filters];
      const [filter] = filters.splice(from, 1);
      filters.splice(to, 0, filter);
      return { ...value, filters };
    });
  }

  function onDropFilter(event: DragEvent<HTMLDivElement>, to: number) {
    event.preventDefault();
    if (dragIndex !== null) {
      moveFilter(dragIndex, to);
    }
    setDragIndex(null);
  }

  const ruleColumns: DashboardColumn<RoutingRule>[] = [
    {
      id: "name",
      header: "Rule",
      sortValue: (row) => row.name,
      cell: (row) => (
        <div className="stacked-cell">
          <strong>{row.name}</strong>
          <span>{routeURL(row)}</span>
        </div>
      ),
    },
    { id: "scope", header: "Scope", sortValue: (row) => row.scope, cell: (row) => <StatusBadge value={row.scope} tone={row.scope === "public" ? "ok" : "unknown"} />, width: "110px" },
    { id: "owner", header: "Owner", sortValue: (row) => row.owner_email || "", cell: (row) => row.owner_email || "all users", width: "220px" },
    { id: "filters", header: "Filters", sortValue: (row) => row.filters?.length ?? 0, cell: (row) => n(row.filters?.length ?? 0), align: "right", width: "90px" },
    {
      id: "actions",
      header: "Actions",
      cell: (row) => (
        <div className="row-actions">
          <button className="button compact" type="button" onClick={() => edit(row)}>Edit</button>
          <button className="icon-button small" type="button" aria-label={`Copy URL for ${row.name}`} onClick={() => copyText(routeURL(row))}>
            <Copy aria-hidden="true" size={14} />
          </button>
          <button className="icon-button small danger-text" type="button" aria-label={`Delete ${row.name}`} onClick={() => void deleteRule(row)}>
            <Trash2 aria-hidden="true" size={14} />
          </button>
        </div>
      ),
      width: "170px",
    },
  ];

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

  const decision = dryRun?.decision ?? null;
  const scoreColumns: DashboardColumn<NonNullable<RouteDecision["scores"]>[number]>[] = [
    { id: "provider", header: "Provider", sortValue: (row) => row.provider_instance_id || row.provider_type, cell: (row) => <span className="mono">{middleEllipsis(row.provider_instance_id || row.provider_type || "")}</span> },
    { id: "score", header: "Score", sortValue: (row) => row.score, cell: (row) => n(row.score), align: "right", width: "76px" },
    { id: "reason", header: "Reason", sortValue: (row) => row.reason, cell: (row) => row.reason || "" },
  ];

  return (
    <div className="view-stack">
      <Section
        title="Routing Rules"
        subtitle="Public and per-user filter chains for named route URLs"
        error={queries.routingRules.error}
        actions={
          <button className="button secondary" type="button" onClick={refresh}>
            <RefreshCw aria-hidden="true" size={15} />
            Refresh
          </button>
        }
      >
        <DataTable rows={rules} columns={ruleColumns} empty="No routing rules are defined" getRowId={(row) => row.id} compact />
      </Section>

      <div className="split-grid route-editor-grid">
        <Section title={editingID ? "Edit Rule" : "New Rule"} subtitle="Drag filters to define priority. The Any filter is the catch-all fallback.">
          <form className="route-rule-form" onSubmit={saveRule}>
            <div className="route-rule-fields">
              <label className="field">
                <span>Name</span>
                <input
                  value={draft.name}
                  onChange={(event) => setDraft((value) => ({ ...value, name: event.target.value }))}
                  placeholder="default-route"
                  pattern="[A-Za-z0-9._~-]+"
                  maxLength={128}
                  title="Use only A-Z, a-z, 0-9, '.', '_', '~', or '-'"
                />
              </label>
              <label className="field">
                <span>Scope</span>
                <select value={draft.scope} onChange={(event) => setDraft((value) => ({ ...value, scope: event.target.value, owner_email: event.target.value === "public" ? "" : value.owner_email }))}>
                  <option value="public">Public</option>
                  <option value="user">User</option>
                </select>
              </label>
              <label className="field">
                <span>Owner</span>
                <input value={draft.owner_email || ""} onChange={(event) => setDraft((value) => ({ ...value, owner_email: event.target.value }))} disabled={draft.scope === "public"} placeholder="user@example.com" />
              </label>
            </div>
            <label className="field">
              <span>Description</span>
              <input value={draft.description || ""} onChange={(event) => setDraft((value) => ({ ...value, description: event.target.value }))} placeholder="Human-readable purpose" />
            </label>

            <div className="filter-chain">
              {draft.filters.map((filter, index) => (
                <div
                  key={`${filter.id || filter.type}-${index}`}
                  className="filter-card"
                  draggable
                  onDragStart={() => setDragIndex(index)}
                  onDragOver={(event) => event.preventDefault()}
                  onDrop={(event) => onDropFilter(event, index)}
                >
                  <div className="filter-card-head">
                    <GripVertical aria-hidden="true" size={16} />
                    <strong>{index + 1}. {filter.label || filter.type}</strong>
                    <StatusBadge value={filter.type} tone={filter.type === "any" ? "ok" : "warn"} />
                    <div className="row-actions">
                      <button className="icon-button small" type="button" aria-label="Move filter up" disabled={index === 0} onClick={() => moveFilter(index, index - 1)}>
                        <ArrowUp aria-hidden="true" size={14} />
                      </button>
                      <button className="icon-button small" type="button" aria-label="Move filter down" disabled={index === draft.filters.length - 1} onClick={() => moveFilter(index, index + 1)}>
                        <ArrowDown aria-hidden="true" size={14} />
                      </button>
                      <button className="icon-button small danger-text" type="button" aria-label="Remove filter" disabled={draft.filters.length === 1} onClick={() => removeFilter(index)}>
                        <Trash2 aria-hidden="true" size={14} />
                      </button>
                    </div>
                  </div>
                  <div className="filter-fields">
                    <label className="field">
                      <span>Type</span>
                      <select value={filter.type} onChange={(event) => updateFilter(index, { type: event.target.value })}>
                        <option value="any">Any</option>
                        <option value="criteria">Criteria</option>
                      </select>
                    </label>
                    <label className="field">
                      <span>Label</span>
                      <input value={filter.label || ""} onChange={(event) => updateFilter(index, { label: event.target.value })} />
                    </label>
                    <CriteriaField label="Service" value={filter.criteria?.services} onChange={(value) => updateFilterCriteria(index, "services", value)} />
                    <CriteriaField label="Protocol" value={filter.criteria?.api_dialects} onChange={(value) => updateFilterCriteria(index, "api_dialects", value)} />
                    <CriteriaField label="Provider Type" value={filter.criteria?.provider_types} onChange={(value) => updateFilterCriteria(index, "provider_types", value)} />
                    <CriteriaField label="Model" value={filter.criteria?.models} onChange={(value) => updateFilterCriteria(index, "models", value)} />
                    <CriteriaField label="Account" value={filter.criteria?.accounts} onChange={(value) => updateFilterCriteria(index, "accounts", value)} />
                    <CriteriaField label="Node" value={filter.criteria?.node_ids} onChange={(value) => updateFilterCriteria(index, "node_ids", value)} />
                  </div>
                </div>
              ))}
            </div>

            <div className="form-actions">
              <button className="button secondary" type="button" onClick={addFilter}>
                <Plus aria-hidden="true" size={15} />
                Filter
              </button>
              <button className="button primary" type="submit" disabled={saving || !ruleNameValid}>
                <Save aria-hidden="true" size={15} />
                Save
              </button>
              {editingID ? <button className="button ghost" type="button" onClick={resetDraft}>Cancel</button> : null}
            </div>
          </form>
        </Section>

        <Section title="Dry Run" subtitle="Simulate provider selection through the current filter chain">
          <form className="route-dry-run-form" onSubmit={submitDryRun}>
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
            <button className="button primary" type="submit" disabled={running || !model.trim() || !ruleNameValid}>
              <Play aria-hidden="true" size={15} />
              Dry run
            </button>
          </form>
          {error ? <div className="inline-error">{error}</div> : null}
          {decision ? (
            <div className="decision-panel">
              <div className="decision-head">
                <StatusBadge value={decision.allowed ? "allowed" : "rejected"} tone={decision.allowed ? "ok" : "danger"} />
                <span className="mono">{decision.routing_rule_id || decision.route_id || "inline rule"}</span>
                <strong>{decision.selected || decision.reason || ""}</strong>
              </div>
              <RouteStepAnimation steps={dryRun?.steps ?? []} activeStep={activeStep} />
              <DataTable rows={decision.scores ?? []} columns={scoreColumns} empty="No score rows returned" compact />
            </div>
          ) : null}
        </Section>
      </div>

      <Section title="Capacity By Model" subtitle="Current provider pool grouped by service and reported model" error={queries.providers.error}>
        <DataTable rows={capacity} columns={capacityColumns} empty="No provider capacity is available" compact />
      </Section>
    </div>
  );
}

function CriteriaField({ label, value, onChange }: { label: string; value?: string[]; onChange: (value: string) => void }) {
  return (
    <label className="field">
      <span>{label}</span>
      <input value={(value ?? []).join(", ")} onChange={(event) => onChange(event.target.value)} placeholder="comma separated" />
    </label>
  );
}

function RouteStepAnimation({ steps, activeStep }: { steps: RoutingRuleStep[]; activeStep: number }) {
  if (steps.length === 0) {
    return null;
  }
  return (
    <div className="route-stepper" aria-label="Route rule dry run steps">
      {steps.map((step, index) => (
        <div key={`${step.filter_id || step.filter_type}-${index}`} className={`route-step ${index === activeStep ? "active" : ""} ${step.selected ? "selected" : ""}`}>
          <span className="route-step-index">{index + 1}</span>
          <div>
            <strong>{step.label || step.filter_type}</strong>
            <p>{step.selected ? `Selected ${step.selected}` : step.reason || "No match"}</p>
            <small>{n(step.matched?.length ?? 0)} matched / {n(step.rejected?.length ?? 0)} rejected</small>
          </div>
        </div>
      ))}
    </div>
  );
}

function materializeDraft(draft: DraftRule): Partial<RoutingRule> {
  return {
    id: draft.id,
    name: draft.name.trim(),
    scope: draft.scope,
    owner_email: draft.scope === "user" ? draft.owner_email?.trim() : "",
    description: draft.description?.trim(),
    filters: draft.filters.map((filter, index) => ({
      id: filter.id?.trim() || `filter-${index + 1}`,
      type: filter.type || "criteria",
      label: filter.label?.trim() || filter.type || "criteria",
      criteria: compactCriteria(filter.criteria ?? {}),
    })),
  };
}

function compactCriteria(criteria: RoutingFilter["criteria"]) {
  if (!criteria) {
    return {};
  }
  return Object.fromEntries(Object.entries(criteria).filter(([, value]) => Array.isArray(value) && value.length > 0));
}

function splitCSV(value: string) {
  return value.split(",").map((part) => part.trim()).filter(Boolean);
}

function validRoutingRuleName(name: string) {
  return /^[A-Za-z0-9._~-]{1,128}$/.test(name.trim());
}

function routeURL(rule: Pick<RoutingRule, "name" | "owner_email" | "scope">) {
  const owner = rule.scope === "user" ? rule.owner_email || "user@example.com" : "public";
  return `/route/${encodeURIComponent(owner)}/${rule.name}/v1/...`;
}

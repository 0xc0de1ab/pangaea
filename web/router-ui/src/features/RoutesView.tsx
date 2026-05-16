import type { DragEvent, FormEvent } from "react";
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, ArrowUp, ChevronLeft, ChevronRight, Copy, GripVertical, Play, Plus, RefreshCw, Save, Search, Settings2, Trash2, X } from "lucide-react";
import type { DashboardViewProps } from "../app/dashboard";
import { DataTable, type DashboardColumn } from "../components/DataTable";
import { Section } from "../components/Section";
import { StatusBadge } from "../components/StatusBadge";
import { api } from "../lib/api";
import { capacityRows, providerAccountLabel } from "../lib/derive";
import { copyText, cx, hasText, middleEllipsis, n } from "../lib/format";
import type { RouteDecision, RouteRequest, RouterUser, RoutingFilter, RoutingRule, RoutingRuleDryRunResponse, RoutingRuleStep } from "../lib/types";

type DraftRule = Omit<RoutingRule, "id" | "created_at" | "updated_at"> & { id?: string };
type CapacityRow = ReturnType<typeof capacityRows>[number];
type CriteriaOption = {
  value: string;
  label?: string;
  description?: string;
};
type RouteCriteriaOptions = {
  services: CriteriaOption[];
  protocols: CriteriaOption[];
  providerTypes: CriteriaOption[];
  models: CriteriaOption[];
  modelsByService: Record<string, CriteriaOption[]>;
  accounts: CriteriaOption[];
  accountsByService: Record<string, CriteriaOption[]>;
  nodes: CriteriaOption[];
  nodesByService: Record<string, CriteriaOption[]>;
};

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
const capacityPageSizeOptions = [10, 25, 50, 100];
const protocolOptions: CriteriaOption[] = [
  { value: "openai", label: "OpenAI", description: "OpenAI-compatible chat/completions, models, and usage routes" },
  { value: "anthropic", label: "Anthropic", description: "Anthropic-compatible messages, models, and usage routes" },
  { value: "gemini", label: "Gemini", description: "Gemini-compatible generateContent and streamGenerateContent routes" },
];

export function RoutesView({ data, queries, search, token, refresh }: DashboardViewProps) {
  const [error, setError] = useState("");
  const [editingRule, setEditingRule] = useState<RoutingRule | null>(null);
  const [capacityModelQuery, setCapacityModelQuery] = useState("");
  const [capacityPageIndex, setCapacityPageIndex] = useState(0);
  const [capacityPageSize, setCapacityPageSize] = useState(25);
  const [capacitySettingsOpen, setCapacitySettingsOpen] = useState(false);
  const allCapacity = useMemo(() => capacityRows(data.providers).filter((row) => hasText(row, search)), [data.providers, search]);
  const filterOptions = useMemo(() => routeCriteriaOptions(data.providers, data.models), [data.models, data.providers]);
  const capacity = useMemo(() => {
    const query = capacityModelQuery.trim().toLowerCase();
    if (!query) {
      return allCapacity;
    }
    return allCapacity.filter((row) => row.model.toLowerCase().includes(query));
  }, [allCapacity, capacityModelQuery]);
  const capacityPageCount = Math.max(1, Math.ceil(capacity.length / capacityPageSize));
  const rules = useMemo(() => data.routingRules.filter((rule) => hasText(rule, search)), [data.routingRules, search]);

  useEffect(() => {
    setCapacityPageIndex(0);
  }, [capacityModelQuery, search, capacityPageSize]);

  useEffect(() => {
    setCapacityPageIndex((value) => Math.min(value, capacityPageCount - 1));
  }, [capacityPageCount]);

  const edit = useCallback((rule: RoutingRule) => {
    setEditingRule(rule);
    setError("");
  }, []);

  const deleteRule = useCallback(async (rule: RoutingRule) => {
    if (!window.confirm(`Delete route rule ${rule.name}?`)) {
      return;
    }
    setError("");
    try {
      await api.deleteRoutingRule(rule.id, token);
      if (editingRule?.id === rule.id) {
        setEditingRule(null);
      }
      refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete routing rule");
    }
  }, [editingRule?.id, refresh, token]);

  const cancelEditing = useCallback(() => {
    setEditingRule(null);
  }, []);

  const savedRule = useCallback(() => {
    setEditingRule(null);
    refresh();
  }, [refresh]);

  const ruleColumns = useMemo<DashboardColumn<RoutingRule>[]>(() => [
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
  ], [deleteRule, edit]);

  const capacityColumns = useMemo<DashboardColumn<CapacityRow>[]>(() => [
    { id: "service", header: "Service", sortValue: (row) => row.service, cell: (row) => row.service, width: "110px" },
    {
      id: "model",
      header: "Model",
      headerExtra: <CapacityModelFilter value={capacityModelQuery} onChange={setCapacityModelQuery} />,
      sortValue: (row) => row.model,
      cell: (row) => <CapacityModelCell row={row} />,
    },
    { id: "hosts", header: "Hosts", sortValue: (row) => row.hosts.size, cell: (row) => n(row.hosts.size), align: "right", width: "76px" },
    { id: "providers", header: "Providers", sortValue: (row) => row.providers, cell: (row) => n(row.providers), align: "right", width: "92px" },
    { id: "ready", header: "Ready", sortValue: (row) => row.ready, cell: (row) => n(row.ready), align: "right", width: "76px" },
    { id: "degraded", header: "Degraded", sortValue: (row) => row.degraded, cell: (row) => n(row.degraded), align: "right", width: "92px" },
    { id: "down", header: "Down", sortValue: (row) => row.down, cell: (row) => n(row.down), align: "right", width: "76px" },
    { id: "queue", header: "Queue", sortValue: (row) => row.queueDepth, cell: (row) => n(row.queueDepth), align: "right", width: "76px" },
  ], [capacityModelQuery]);

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
        {error ? <div className="inline-error">{error}</div> : null}
      </Section>

      <RoutingRuleWorkspace
        editingRule={editingRule}
        models={data.models}
        filterOptions={filterOptions}
        users={data.users}
        token={token}
        onCancel={cancelEditing}
        onSaved={savedRule}
      />

      <Section
        title="Capacity By Model"
        subtitle={`${n(capacity.length)} of ${n(allCapacity.length)} model capacity rows`}
        error={queries.providers.error}
        actions={
          <CapacityPageSizeMenu
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
        <CapacityPagination
          pageIndex={capacityPageIndex}
          pageCount={capacityPageCount}
          pageSize={capacityPageSize}
          total={capacity.length}
          onPageChange={setCapacityPageIndex}
        />
      </Section>
    </div>
  );
}

function CapacityModelCell({ row }: { row: CapacityRow }) {
  const groupMembers = Array.from(row.groupMembers ?? []);
  const title = [
    row.groupModel ? `Group model${groupMembers.length ? `: ${groupMembers.join(", ")}` : ""}` : "",
    row.aliasModel ? "Alias model" : "",
  ].filter(Boolean).join(" / ");
  return (
    <span className={cx("model-name-line", "capacity-model-name")} title={title || row.model}>
      {row.groupModel ? <span className="model-group-badge mini" title="Group model">G</span> : null}
      {row.aliasModel ? <span className="model-alias-badge mini" title="Alias model">A</span> : null}
      <span className="mono">{middleEllipsis(row.model, 26, 12)}</span>
    </span>
  );
}

function CapacityModelFilter({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return (
    <label className="table-header-search" onClick={(event) => event.stopPropagation()}>
      <Search aria-hidden="true" size={13} />
      <input
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        onClick={(event) => event.stopPropagation()}
        onKeyDown={(event) => event.stopPropagation()}
        placeholder="Search model"
        aria-label="Search model name"
      />
    </label>
  );
}

function CapacityPageSizeMenu({
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
      <button className="icon-button" type="button" aria-label="Capacity table settings" aria-expanded={open} onClick={() => onOpenChange(!open)}>
        <Settings2 aria-hidden="true" size={16} />
      </button>
      {open ? (
        <div className="table-settings-popover" role="dialog" aria-label="Capacity table page size">
          <strong>Rows per page</strong>
          <div className="page-size-options">
            {capacityPageSizeOptions.map((option) => (
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

function CapacityPagination({
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
    <div className="table-pagination capacity-pagination">
      <span>
        {n(start)}-{n(end)} of {n(total)} rows
      </span>
      <div className="pagination-actions">
        <span>Page {n(pageIndex + 1)} / {n(pageCount)}</span>
        <button className="icon-button small" type="button" aria-label="Previous capacity page" disabled={pageIndex === 0} onClick={() => onPageChange(Math.max(0, pageIndex - 1))}>
          <ChevronLeft aria-hidden="true" size={15} />
        </button>
        <button className="icon-button small" type="button" aria-label="Next capacity page" disabled={pageIndex >= pageCount - 1} onClick={() => onPageChange(Math.min(pageCount - 1, pageIndex + 1))}>
          <ChevronRight aria-hidden="true" size={15} />
        </button>
      </div>
    </div>
  );
}

const RoutingRuleWorkspace = memo(function RoutingRuleWorkspace({
  editingRule,
  models,
  filterOptions,
  users,
  token,
  onCancel,
  onSaved,
}: {
  editingRule: RoutingRule | null;
  models: DashboardViewProps["data"]["models"];
  filterOptions: RouteCriteriaOptions;
  users: RouterUser[];
  token?: string;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [draft, setDraft] = useState<DraftRule>(() => draftFromRule(editingRule));
  const draftNameRef = useRef(draft.name);
  const [nameResetID, setNameResetID] = useState(0);
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [model, setModel] = useState(models[0]?.id || "");
  const [dialect, setDialect] = useState<RouteRequest["api_dialect"]>("openai");
  const [stream, setStream] = useState(true);
  const [dryRun, setDryRun] = useState<RoutingRuleDryRunResponse | null>(null);
  const [activeStep, setActiveStep] = useState(-1);
  const [error, setError] = useState("");
  const [running, setRunning] = useState(false);
  const [saving, setSaving] = useState(false);
  const editingID = editingRule?.id || "";
  const decision = dryRun?.decision ?? null;
  const userOptions = useMemo(() => [...users].sort((left, right) => left.email.localeCompare(right.email)), [users]);
  const updateDraftNameRef = useCallback((value: string) => {
    draftNameRef.current = value;
  }, []);

  const scoreColumns = useMemo<DashboardColumn<NonNullable<RouteDecision["scores"]>[number]>[]>(() => [
    { id: "provider", header: "Provider", sortValue: (row) => row.provider_instance_id || row.provider_type, cell: (row) => <span className="mono">{middleEllipsis(row.provider_instance_id || row.provider_type || "")}</span> },
    { id: "score", header: "Score", sortValue: (row) => row.score, cell: (row) => n(row.score), align: "right", width: "76px" },
    { id: "reason", header: "Reason", sortValue: (row) => row.reason, cell: (row) => row.reason || "" },
  ], []);

  useEffect(() => {
    const nextDraft = draftFromRule(editingRule);
    draftNameRef.current = nextDraft.name;
    setDraft(nextDraft);
    setNameResetID((value) => value + 1);
    setDryRun(null);
    setError("");
    setDragIndex(null);
  }, [editingRule]);

  useEffect(() => {
    if (model || models.length === 0) {
      return;
    }
    setModel(models[0]?.id || "");
  }, [models, model]);

  useEffect(() => {
    if (draft.scope !== "user" || draft.owner_email || userOptions.length === 0) {
      return;
    }
    setDraft((value) => value.scope === "user" && !value.owner_email ? { ...value, owner_email: userOptions[0].email } : value);
  }, [draft.owner_email, draft.scope, userOptions]);

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

  function resetDraft() {
    onCancel();
  }

  function setRuleScope(scope: string) {
    setDraft((value) => {
      if (scope === "public") {
        return { ...value, scope, owner_email: "" };
      }
      const ownerStillExists = value.owner_email && userOptions.some((user) => user.email === value.owner_email);
      return {
        ...value,
        scope,
        owner_email: ownerStillExists ? value.owner_email : userOptions[0]?.email ?? value.owner_email ?? "",
      };
    });
  }

  async function saveRule(event: FormEvent) {
    event.preventDefault();
    setError("");
    const current = currentDraft(draft, draftNameRef.current);
    if (!validRoutingRuleName(current.name)) {
      setError("Route rule name must be URL-safe: A-Z, a-z, 0-9, '.', '_', '~', or '-'.");
      return;
    }
    if (current.scope === "user" && !current.owner_email?.trim()) {
      setError("Select a user before saving a user-scoped route rule.");
      return;
    }
    setSaving(true);
    try {
      const payload = materializeDraft(current);
      if (editingID) {
        await api.updateRoutingRule(editingID, payload, token);
      } else {
        await api.createRoutingRule(payload, token);
      }
      const nextDraft = draftFromRule(null);
      draftNameRef.current = nextDraft.name;
      setDraft(nextDraft);
      setNameResetID((value) => value + 1);
      setDryRun(null);
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save routing rule");
    } finally {
      setSaving(false);
    }
  }

  async function submitDryRun(event: FormEvent) {
    event.preventDefault();
    setError("");
    setDryRun(null);
    const current = currentDraft(draft, draftNameRef.current);
    if (!validRoutingRuleName(current.name)) {
      setError("Route rule name must be URL-safe before running a simulation.");
      return;
    }
    setRunning(true);
    try {
      const result = await api.dryRunRoutingRule({
        rule: materializeDraft(current),
        request: {
          model: model.trim(),
          api_dialect: dialect,
          stream,
          routing_rule_name: current.name,
          routing_rule_owner: current.owner_email,
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

  function updateFilterCriteriaValues(index: number, key: string, values: string[]) {
    setDraft((value) => ({
      ...value,
      filters: value.filters.map((filter, i) => i === index ? {
        ...filter,
        criteria: {
          ...(filter.criteria ?? {}),
          [key]: uniqueStrings(values),
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

  return (
    <div className="split-grid route-editor-grid">
      <Section title={editingID ? "Edit Rule" : "New Rule"} subtitle="Drag filters to define priority. The Any filter is the catch-all fallback.">
        <form className="route-rule-form" onSubmit={saveRule}>
          <div className="route-rule-fields">
            <label className="field">
              <span>Name</span>
              <RuleNameInput
                initialName={draft.name}
                resetID={nameResetID}
                onValueChange={updateDraftNameRef}
              />
            </label>
            <label className="field">
              <span>Scope</span>
              <select value={draft.scope} onChange={(event) => setRuleScope(event.target.value)}>
                <option value="public">Public</option>
                <option value="user">User</option>
              </select>
            </label>
            <label className="field">
              <span>Owner</span>
              <select value={draft.owner_email || ""} onChange={(event) => setDraft((value) => ({ ...value, owner_email: event.target.value }))} disabled={draft.scope === "public" || userOptions.length === 0}>
                {draft.scope === "public" ? <option value="">All users</option> : null}
                {draft.scope === "user" && draft.owner_email && !userOptions.some((user) => user.email === draft.owner_email) ? (
                  <option value={draft.owner_email}>{draft.owner_email} (not registered)</option>
                ) : null}
                {userOptions.map((user) => (
                  <option key={user.email} value={user.email}>
                    {user.email}{user.name ? ` - ${user.name}` : ""}{user.enabled ? "" : " (disabled)"}
                  </option>
                ))}
                {draft.scope === "user" && userOptions.length === 0 ? <option value="">No users available</option> : null}
              </select>
            </label>
          </div>
          <label className="field">
            <span>Description</span>
            <input value={draft.description || ""} onChange={(event) => setDraft((value) => ({ ...value, description: event.target.value }))} placeholder="Human-readable purpose" />
          </label>

          <div className="filter-chain">
            {draft.filters.map((filter, index) => (
              <RoutingFilterCard
                key={`${filter.id || filter.type}-${index}`}
                filter={filter}
                index={index}
                lastIndex={draft.filters.length - 1}
                filterOptions={filterOptions}
                canRemove={draft.filters.length > 1}
                onDragStart={() => setDragIndex(index)}
                onDrop={(event) => onDropFilter(event, index)}
                onMove={moveFilter}
                onRemove={removeFilter}
                onUpdateFilter={updateFilter}
                onUpdateCriteria={updateFilterCriteriaValues}
              />
            ))}
          </div>

          <div className="form-actions">
            <button className="button secondary" type="button" onClick={addFilter}>
              <Plus aria-hidden="true" size={15} />
              Filter
            </button>
            <button className="button primary" type="submit" disabled={saving}>
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
              <span className="mono">{decision.routing_rule_id || decision.route_id || "inline rule"}</span>
              <strong>{decision.selected || decision.reason || ""}</strong>
            </div>
            <RouteStepAnimation steps={dryRun?.steps ?? []} activeStep={activeStep} />
            <DataTable rows={decision.scores ?? []} columns={scoreColumns} empty="No score rows returned" compact />
          </div>
        ) : null}
      </Section>
    </div>
  );
});

function RoutingFilterCard({
  filter,
  index,
  lastIndex,
  filterOptions,
  canRemove,
  onDragStart,
  onDrop,
  onMove,
  onRemove,
  onUpdateFilter,
  onUpdateCriteria,
}: {
  filter: RoutingFilter;
  index: number;
  lastIndex: number;
  filterOptions: RouteCriteriaOptions;
  canRemove: boolean;
  onDragStart: () => void;
  onDrop: (event: DragEvent<HTMLDivElement>) => void;
  onMove: (from: number, to: number) => void;
  onRemove: (index: number) => void;
  onUpdateFilter: (index: number, patch: Partial<RoutingFilter>) => void;
  onUpdateCriteria: (index: number, key: string, values: string[]) => void;
}) {
  const modelOptions = useMemo(() => modelOptionsForFilter(filter, filterOptions), [filter, filterOptions]);
  const accountOptions = useMemo(() => scopedOptionsForFilter(filter, filterOptions.accounts, filterOptions.accountsByService), [filter, filterOptions]);
  const nodeOptions = useMemo(() => scopedOptionsForFilter(filter, filterOptions.nodes, filterOptions.nodesByService), [filter, filterOptions]);
  const selectedServices = uniqueStrings(filter.criteria?.services ?? []);
  const modelNote = selectedServices.length
    ? `Filtered to models reported by ${selectedServices.join(", ")}.`
    : "Examples come from public models and provider model reports.";
  const accountNote = selectedServices.length
    ? `Filtered to accounts reported by ${selectedServices.join(", ")}.`
    : "Examples come from provider auth/account snapshots.";
  const nodeNote = selectedServices.length
    ? `Filtered to nodes running ${selectedServices.join(", ")}.`
    : "Examples come from currently reported provider node IDs.";
  return (
    <div
      className="filter-card"
      draggable
      onDragStart={onDragStart}
      onDragOver={(event) => event.preventDefault()}
      onDrop={onDrop}
    >
      <div className="filter-card-head">
        <GripVertical aria-hidden="true" size={16} />
        <strong>{index + 1}. {filter.label || filter.type}</strong>
        <StatusBadge value={filter.type} tone={filter.type === "any" ? "ok" : "warn"} />
        <div className="row-actions">
          <button className="icon-button small" type="button" aria-label="Move filter up" disabled={index === 0} onClick={() => onMove(index, index - 1)}>
            <ArrowUp aria-hidden="true" size={14} />
          </button>
          <button className="icon-button small" type="button" aria-label="Move filter down" disabled={index === lastIndex} onClick={() => onMove(index, index + 1)}>
            <ArrowDown aria-hidden="true" size={14} />
          </button>
          <button className="icon-button small danger-text" type="button" aria-label="Remove filter" disabled={!canRemove} onClick={() => onRemove(index)}>
            <Trash2 aria-hidden="true" size={14} />
          </button>
        </div>
      </div>
      <div className="filter-fields">
        <label className="field">
          <span>Filter Type</span>
          <select value={filter.type} onChange={(event) => onUpdateFilter(index, { type: event.target.value })}>
            <option value="any">Any fallback</option>
            <option value="criteria">Criteria match</option>
          </select>
          <small className="field-note">
            {filter.type === "any" ? "Uses any routable provider for the requested API protocol and model." : "Narrows providers with the selected criteria below."}
          </small>
        </label>
        <label className="field">
          <span>Label</span>
          <input value={filter.label || ""} onChange={(event) => onUpdateFilter(index, { label: event.target.value })} />
        </label>
        <MultiCriteriaField
          label="Service"
          emptyLabel="Any connected service"
          note="Pick one or more services currently reported by providers."
          options={filterOptions.services}
          value={filter.criteria?.services}
          disabled={filter.type === "any"}
          onChange={(value) => onUpdateCriteria(index, "services", value)}
        />
        <MultiCriteriaField
          label="Protocol"
          emptyLabel="Any API protocol"
          note="Examples: openai, anthropic, gemini."
          options={filterOptions.protocols}
          value={filter.criteria?.api_dialects}
          disabled={filter.type === "any"}
          onChange={(value) => onUpdateCriteria(index, "api_dialects", value)}
        />
        <MultiCriteriaField
          label="Provider Type"
          emptyLabel="Any provider type"
          note="Examples come from connected provider registrations."
          options={filterOptions.providerTypes}
          value={filter.criteria?.provider_types}
          disabled={filter.type === "any"}
          onChange={(value) => onUpdateCriteria(index, "provider_types", value)}
        />
        <MultiCriteriaField
          label="Model"
          emptyLabel={selectedServices.length ? "No model from selected service" : "Any model"}
          note={modelNote}
          options={modelOptions}
          value={filter.criteria?.models}
          disabled={filter.type === "any"}
          onChange={(value) => onUpdateCriteria(index, "models", value)}
        />
        <MultiCriteriaField
          label="Account"
          emptyLabel={selectedServices.length ? "No account from selected service" : "Any account"}
          note={accountNote}
          options={accountOptions}
          value={filter.criteria?.accounts}
          disabled={filter.type === "any"}
          onChange={(value) => onUpdateCriteria(index, "accounts", value)}
        />
        <MultiCriteriaField
          label="Node"
          emptyLabel={selectedServices.length ? "No node from selected service" : "Any node"}
          note={nodeNote}
          options={nodeOptions}
          value={filter.criteria?.node_ids}
          disabled={filter.type === "any"}
          onChange={(value) => onUpdateCriteria(index, "node_ids", value)}
        />
      </div>
      {filter.type === "any" ? (
        <div className="any-filter-note">
          Any fallback ignores Service, Protocol, Provider Type, Model, Account, and Node criteria in this filter. Routing still respects the incoming request protocol, requested model, health, auth, capability, and quota.
        </div>
      ) : null}
    </div>
  );
}

function draftFromRule(rule: RoutingRule | null): DraftRule {
  if (!rule) {
    return {
      ...emptyRule,
      filters: [...emptyRule.filters],
    };
  }
  return {
    id: rule.id,
    name: rule.name,
    scope: rule.scope,
    owner_email: rule.owner_email || "",
    description: rule.description || "",
    filters: rule.filters?.length ? rule.filters : [emptyFilter],
  };
}

const RuleNameInput = memo(function RuleNameInput({
  initialName,
  resetID,
  onValueChange,
}: {
  initialName: string;
  resetID: number;
  onValueChange: (value: string) => void;
}) {
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (inputRef.current) {
      inputRef.current.value = initialName;
    }
    onValueChange(initialName);
  }, [initialName, onValueChange, resetID]);

  return (
    <input
      ref={inputRef}
      defaultValue={initialName}
      onInput={(event) => {
        onValueChange(event.currentTarget.value);
      }}
      placeholder="default-route"
      maxLength={128}
      title="Use only A-Z, a-z, 0-9, '.', '_', '~', or '-'"
      autoComplete="off"
      autoCorrect="off"
      spellCheck={false}
    />
  );
});

function currentDraft(draft: DraftRule, name: string): DraftRule {
  return {
    ...draft,
    name,
  };
}

function MultiCriteriaField({
  label,
  emptyLabel,
  note,
  options,
  value,
  disabled,
  onChange,
}: {
  label: string;
  emptyLabel: string;
  note: string;
  options: CriteriaOption[];
  value?: string[];
  disabled?: boolean;
  onChange: (value: string[]) => void;
}) {
  const selectedValues = uniqueStrings(value ?? []);
  const optionByValue = useMemo(() => new Map(options.map((option) => [option.value, option])), [options]);
  const availableOptions = options.filter((option) => !selectedValues.includes(option.value));
  const [selectedOption, setSelectedOption] = useState("");

  useEffect(() => {
    if (!selectedOption || availableOptions.some((option) => option.value === selectedOption)) {
      return;
    }
    setSelectedOption("");
  }, [availableOptions, selectedOption]);

  function addSelected() {
    if (!selectedOption) {
      return;
    }
    onChange([...selectedValues, selectedOption]);
    setSelectedOption("");
  }

  function removeSelected(nextValue: string) {
    onChange(selectedValues.filter((current) => current !== nextValue));
  }

  function optionLabel(nextValue: string) {
    return optionByValue.get(nextValue)?.label || nextValue;
  }

  function optionTitle(nextValue: string) {
    const option = optionByValue.get(nextValue);
    if (!option) {
      return `${nextValue} is not currently reported by connected providers.`;
    }
    return [option.label || option.value, option.description].filter(Boolean).join("\n");
  }

  return (
    <div className={cx("field route-criteria-field", disabled && "criteria-disabled")}>
      <span>{label}</span>
      <div className="criteria-picker">
        <select value={selectedOption} onChange={(event) => setSelectedOption(event.target.value)} disabled={disabled || availableOptions.length === 0}>
          <option value="">{selectedValues.length ? "Add another value" : emptyLabel}</option>
          {availableOptions.map((option) => (
            <option key={option.value} value={option.value} title={option.description}>
              {option.label || option.value}
            </option>
          ))}
        </select>
        <button className="icon-button small" type="button" aria-label={`Add ${label}`} disabled={disabled || !selectedOption} onClick={addSelected}>
          <Plus aria-hidden="true" size={14} />
        </button>
      </div>
      {selectedValues.length ? (
        <div className="criteria-chip-row" aria-label={`Selected ${label} values`}>
          {selectedValues.map((nextValue) => {
            const reported = optionByValue.has(nextValue);
            return (
              <span className={cx("criteria-chip", !reported && "orphan")} key={nextValue} title={optionTitle(nextValue)}>
                <span>{optionLabel(nextValue)}</span>
                {!reported ? <em>not reported</em> : null}
                <button type="button" aria-label={`Remove ${nextValue}`} disabled={disabled} onClick={() => removeSelected(nextValue)}>
                  <X aria-hidden="true" size={12} />
                </button>
              </span>
            );
          })}
        </div>
      ) : null}
      <small className="field-note" title={options.map((option) => option.value).join(", ") || note}>
        {disabled ? "Disabled because this filter is Any fallback." : options.length ? note : `No ${label.toLowerCase()} values reported yet.`}
      </small>
    </div>
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

function uniqueStrings(values: string[]) {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const value of values) {
    const trimmed = value.trim();
    if (!trimmed || seen.has(trimmed)) {
      continue;
    }
    seen.add(trimmed);
    out.push(trimmed);
  }
  return out;
}

function modelOptionsForFilter(filter: RoutingFilter, filterOptions: RouteCriteriaOptions) {
  return scopedOptionsForFilter(filter, filterOptions.models, filterOptions.modelsByService);
}

function scopedOptionsForFilter(filter: RoutingFilter, fallbackOptions: CriteriaOption[], optionsByService: Record<string, CriteriaOption[]>) {
  const selectedServices = uniqueStrings(filter.criteria?.services ?? []);
  if (selectedServices.length === 0) {
    return fallbackOptions;
  }
  const options = new Map<string, CriteriaOption>();
  for (const service of selectedServices) {
    for (const option of optionsByService[service] ?? []) {
      addCriteriaOption(options, option);
    }
  }
  return sortCriteriaOptions([...options.values()]);
}

function routeCriteriaOptions(providers: DashboardViewProps["data"]["providers"], publicModels: DashboardViewProps["data"]["models"]): RouteCriteriaOptions {
  const services = new Map<string, CriteriaOption>();
  const connectedServices = new Set<string>();
  const providerTypes = new Map<string, CriteriaOption>();
  const models = new Map<string, CriteriaOption>();
  const modelsByService = new Map<string, Map<string, CriteriaOption>>();
  const accounts = new Map<string, CriteriaOption>();
  const accountsByService = new Map<string, Map<string, CriteriaOption>>();
  const nodes = new Map<string, CriteriaOption>();
  const nodesByService = new Map<string, Map<string, CriteriaOption>>();

  for (const model of publicModels) {
    addCriteriaOption(models, {
      value: model.id,
      label: model.display && model.display !== model.id ? `${model.display} (${model.id})` : model.id,
      description: [model.protocol ? `Protocol: ${model.protocol}` : "", model.canonical_model ? `Canonical: ${model.canonical_model}` : ""].filter(Boolean).join("\n"),
    });
    if (model.canonical_model && model.canonical_model !== model.id) {
      addCriteriaOption(models, {
        value: model.canonical_model,
        label: `${model.canonical_model} (canonical)`,
        description: `Canonical model for ${model.id}`,
      });
    }
  }

  for (const provider of providers) {
    const service = provider.identity.service?.trim();
    if (service) {
      addCriteriaOption(services, {
        value: service,
        description: `Reported by ${provider.identity.provider_type || "provider"} on ${provider.identity.host_name || provider.identity.node_id || "unknown host"}`,
      });
      const status = provider.health?.status || "unknown";
      if (status !== "down") {
        connectedServices.add(service);
      }
    }

    addCriteriaOption(providerTypes, {
      value: provider.identity.provider_type,
      description: [provider.identity.service, provider.identity.kind, provider.identity.host_name].filter(Boolean).join(" / "),
    });

    const account = providerAccountLabel(provider);
    const accountOption = {
      value: account,
      description: [provider.identity.service, provider.identity.host_name, provider.identity.provider_type].filter(Boolean).join(" / "),
    };
    addCriteriaOption(accounts, accountOption);
    addServiceCriteriaOption(accountsByService, service, accountOption);

    const nodeOption = {
      value: provider.identity.node_id,
      description: [provider.identity.host_name, provider.identity.service, provider.identity.provider_type].filter(Boolean).join(" / "),
    };
    addCriteriaOption(nodes, nodeOption);
    addServiceCriteriaOption(nodesByService, service, nodeOption);

    for (const model of provider.models ?? []) {
      const modelOption = {
        value: model.id,
        description: [provider.identity.service, provider.identity.provider_type, model.kind ? `Kind: ${model.kind}` : ""].filter(Boolean).join(" / "),
      };
      addCriteriaOption(models, modelOption);
      addServiceCriteriaOption(modelsByService, service, modelOption);
      for (const alias of model.aliases ?? []) {
        const aliasOption = {
          value: alias,
          label: `${alias} (alias)`,
          description: `Alias for ${model.id}`,
        };
        addCriteriaOption(models, aliasOption);
        addServiceCriteriaOption(modelsByService, service, aliasOption);
      }
      for (const groupMember of model.group_members ?? []) {
        const groupMemberOption = {
          value: groupMember,
          label: `${groupMember} (group member)`,
          description: `Member of ${model.id}`,
        };
        addCriteriaOption(models, groupMemberOption);
        addServiceCriteriaOption(modelsByService, service, groupMemberOption);
      }
    }
  }

  const connectedServiceOptions = [...services.values()].filter((option) => connectedServices.has(option.value));
  return {
    services: sortCriteriaOptions(connectedServiceOptions.length ? connectedServiceOptions : [...services.values()]),
    protocols: protocolOptions,
    providerTypes: sortCriteriaOptions([...providerTypes.values()]),
    models: sortCriteriaOptions([...models.values()]),
    modelsByService: Object.fromEntries([...modelsByService.entries()].map(([service, serviceModels]) => [service, sortCriteriaOptions([...serviceModels.values()])])),
    accounts: sortCriteriaOptions([...accounts.values()]),
    accountsByService: Object.fromEntries([...accountsByService.entries()].map(([service, serviceAccounts]) => [service, sortCriteriaOptions([...serviceAccounts.values()])])),
    nodes: sortCriteriaOptions([...nodes.values()]),
    nodesByService: Object.fromEntries([...nodesByService.entries()].map(([service, serviceNodes]) => [service, sortCriteriaOptions([...serviceNodes.values()])])),
  };
}

function addServiceCriteriaOption(target: Map<string, Map<string, CriteriaOption>>, service: string | undefined, option: CriteriaOption) {
  const key = service?.trim();
  if (!key) {
    return;
  }
  let serviceModels = target.get(key);
  if (!serviceModels) {
    serviceModels = new Map<string, CriteriaOption>();
    target.set(key, serviceModels);
  }
  addCriteriaOption(serviceModels, option);
}

function addCriteriaOption(target: Map<string, CriteriaOption>, option: CriteriaOption) {
  const value = option.value?.trim();
  if (!value || target.has(value)) {
    return;
  }
  target.set(value, { ...option, value });
}

function sortCriteriaOptions(options: CriteriaOption[]) {
  return [...options].sort((left, right) => (left.label || left.value).localeCompare(right.label || right.value));
}

function validRoutingRuleName(name: string) {
  return /^[A-Za-z0-9._~-]{1,128}$/.test(name.trim());
}

function routeURL(rule: Pick<RoutingRule, "name" | "owner_email" | "scope">) {
  const owner = rule.scope === "user" ? rule.owner_email || "user@example.com" : "public";
  return `/route/${encodeURIComponent(owner)}/${rule.name}/v1/...`;
}

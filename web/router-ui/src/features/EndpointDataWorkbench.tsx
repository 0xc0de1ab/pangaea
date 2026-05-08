import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Activity, Bot, Braces, Clipboard, ListChecks, MessageSquare, Radio, RefreshCw, Sparkles, X, type LucideIcon } from "lucide-react";
import { ServiceIcon } from "../components/ServiceIcon";
import { api } from "../lib/api";
import { providerAccountLabel, providerID } from "../lib/derive";
import { copyText, cx, fmtTime, middleEllipsis, n } from "../lib/format";
import type { ProviderProtocol } from "../lib/protocols";
import type { ServiceEndpoint } from "../lib/service-endpoints";
import type { ProviderModel, ProviderRegistration, ProviderUsageSnapshot } from "../lib/types";

export type EndpointDataWorkbenchTarget = {
  kind: "models" | "usage";
  provider: ProviderRegistration;
  endpoint: ServiceEndpoint;
  usage?: ProviderUsageSnapshot;
};

type EndpointDataWorkbenchProps = {
  target: EndpointDataWorkbenchTarget | null;
  token?: string;
  onClose: () => void;
};

type ModelRow = {
  id: string;
  display?: string;
  aliases?: ModelAlias[];
  protocol: string;
  capabilities?: string[];
  contextTokens?: number;
  maxContextTokens?: number;
  owner?: string;
  version?: string;
};

type ModelAlias = {
  value: string;
  source: "provider" | "route";
};

type UsageWindowRow = {
  label: string;
  remainingPct?: number;
  used?: number;
  limit?: number;
  unit?: string;
  resetAt?: string;
};

type WorkbenchState = {
  loading: boolean;
  error?: string;
  rawModels?: unknown;
  usage?: ProviderUsageSnapshot;
};

const panelExitMs = 260;

export function EndpointDataWorkbench({ target, token, onClose }: EndpointDataWorkbenchProps) {
  const [renderTarget, setRenderTarget] = useState<EndpointDataWorkbenchTarget | null>(target);
  const [exiting, setExiting] = useState(false);
  const [state, setState] = useState<WorkbenchState>({ loading: false });
  const [clock, setClock] = useState(() => Date.now());
  const targetKey = target ? `${providerID(target.provider)}:${target.endpoint.id}:${target.kind}` : "";
  const activeTarget = target ?? renderTarget;

  const load = useCallback(async (activeTarget: EndpointDataWorkbenchTarget, cancelled: () => boolean) => {
    setState((current) => ({ ...current, loading: true, error: undefined }));
    try {
      if (activeTarget.kind === "models") {
        const rawModels = await modelsForProtocol(activeTarget.endpoint.protocol, token);
        if (!cancelled()) {
          setState({ loading: false, rawModels });
        }
        return;
      }
      const snapshots = await api.usage(token);
      const usage = snapshots.find((snapshot) => snapshot.provider_instance_id === providerID(activeTarget.provider)) ?? activeTarget.usage;
      if (!cancelled()) {
        setState({ loading: false, usage });
      }
    } catch (err) {
      if (!cancelled()) {
        setState({ loading: false, error: err instanceof Error ? err.message : "Request failed" });
      }
    }
  }, [token]);

  useEffect(() => {
    if (target) {
      setRenderTarget(target);
      setExiting(false);
      return;
    }
    if (!renderTarget) return;
    setExiting(true);
    const timeout = window.setTimeout(() => {
      setRenderTarget(null);
      setExiting(false);
    }, panelExitMs);
    return () => window.clearTimeout(timeout);
  }, [renderTarget, target]);

  useEffect(() => {
    if (!target) return;
    let cancelled = false;
    void load(target, () => cancelled);
    return () => {
      cancelled = true;
    };
  }, [load, target, targetKey]);

  useEffect(() => {
    if (!activeTarget || activeTarget.kind !== "usage") return;
    const ticker = window.setInterval(() => setClock(Date.now()), 1000);
    return () => window.clearInterval(ticker);
  }, [activeTarget, targetKey]);

  useEffect(() => {
    if (!target || target.kind !== "usage") return;
    const refresher = window.setInterval(() => {
      void load(target, () => false);
    }, 60_000);
    return () => window.clearInterval(refresher);
  }, [load, target, targetKey]);

  const modelRows = useMemo(() => {
    if (!activeTarget || activeTarget.kind !== "models") return [];
    return normalizeModelRows(activeTarget.endpoint, activeTarget.provider.models ?? [], state.rawModels);
  }, [activeTarget, state.rawModels]);

  const usageRows = useMemo(() => normalizeUsageRows(state.usage ?? activeTarget?.usage), [activeTarget?.usage, state.usage]);

  if (!activeTarget) {
    return null;
  }

  const { provider, endpoint } = activeTarget;
  const title = activeTarget.kind === "models" ? "Models" : "Usage";
  const Icon = activeTarget.kind === "models" ? ListChecks : Activity;
  const path = activeTarget.kind === "models" ? endpoint.modelsPath : "/router/v1/usage/providers";

  return (
    <div className={cx("chat-layer", exiting && "is-exiting")} role="presentation">
      <button className="chat-scrim" type="button" aria-label={`Close ${title.toLowerCase()}`} onClick={onClose} />
      <aside className="chat-workbench data-workbench" aria-label={`${endpoint.label} ${title.toLowerCase()}`}>
        <div className="chat-header">
          <div className="chat-title-row">
            <ServiceIcon service={endpoint.protocol} size={30} label={endpoint.protocolLabel} />
            <div>
              <h2>{endpoint.label} {title}</h2>
              <p>
                <span className="mono">{middleEllipsis(providerID(provider), 18, 12)}</span>
                <span>{providerAccountLabel(provider)}</span>
              </p>
            </div>
          </div>
          <button className="icon-button" type="button" aria-label={`Close ${title.toLowerCase()}`} onClick={onClose}>
            <X aria-hidden="true" size={18} />
          </button>
        </div>

        <div className="chat-toolbar data-toolbar">
          <div className="data-toolbar-title">
            <Icon aria-hidden="true" size={17} />
            <span>{endpoint.protocolLabel}</span>
          </div>
          <div className="chat-route">
            <span className="mono">{endpoint.model || endpoint.label}</span>
            <span className="mono">{path}</span>
          </div>
          <button className="icon-button small" type="button" title="Refresh" disabled={state.loading || exiting} onClick={() => void load(activeTarget, () => false)}>
            <RefreshCw aria-hidden="true" className={cx(state.loading && "spin")} size={15} />
          </button>
        </div>

        <div className={cx("chat-progress", state.loading && "is-active")} aria-hidden={!state.loading}>
          <span />
        </div>

        <div className="data-workbench-body">
          {state.error ? <div className="inline-error endpoint-error">{state.error}</div> : null}
          {activeTarget.kind === "models" ? <ModelsTable rows={modelRows} loading={state.loading} /> : <UsageTable snapshot={state.usage ?? activeTarget.usage} rows={usageRows} loading={state.loading} now={clock} scopeKey={targetKey} />}
        </div>
      </aside>
    </div>
  );
}

function ModelsTable({ rows, loading }: { rows: ModelRow[]; loading: boolean }) {
  if (!loading && rows.length === 0) {
    return <div className="chat-empty">No models reported</div>;
  }
  return (
    <div className="workbench-table-frame">
      <table className="workbench-table models-table">
        <thead>
          <tr>
            <th>Model</th>
            <th>Alias</th>
            <th>API</th>
            <th>Capabilities</th>
            <th>Context</th>
            <th>Owner / Version</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={`${row.protocol}:${row.id}`}>
              <td>
                <strong className="mono">{row.id}</strong>
                {row.display && row.display !== row.id ? <span className="table-subtext">{row.display}</span> : null}
              </td>
              <td><ModelAliases aliases={row.aliases ?? []} /></td>
              <td>{row.protocol}</td>
              <td><CapabilityIcons capabilities={row.capabilities ?? []} /></td>
              <td className="numeric mono" title={contextWindowTitle(row.contextTokens, row.maxContextTokens)}>
                {formatContextWindow(row.contextTokens, row.maxContextTokens)}
              </td>
              <td>{[row.owner, row.version].filter(Boolean).join(" / ")}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ModelAliases({ aliases }: { aliases: ModelAlias[] }) {
  if (!aliases.length) {
    return <span className="muted">-</span>;
  }
  return (
    <div className="model-alias-list">
      {aliases.map((alias) => (
        <span className={cx("model-alias-chip", alias.source)} key={`${alias.source}:${alias.value}`} title={alias.source === "route" ? "Pangaea routing alias" : "Provider model name"}>
          {alias.value}
        </span>
      ))}
    </div>
  );
}

function CapabilityIcons({ capabilities }: { capabilities: string[] }) {
  const unique = uniqueStrings(capabilities);
  if (!unique.length) {
    return <span className="muted">-</span>;
  }
  return (
    <div className="capability-icons" aria-label={unique.join(", ")}>
      {unique.map((capability) => {
        const meta = capabilityMeta(capability);
        const Icon = meta.icon;
        return (
          <span className={cx("capability-icon", meta.tone)} key={capability} title={meta.label} aria-label={meta.label}>
            <Icon aria-hidden="true" size={14} strokeWidth={2.15} />
          </span>
        );
      })}
    </div>
  );
}

function formatContextWindow(contextTokens?: number, maxContextTokens?: number) {
  const context = formatTokenWindow(contextTokens);
  const maxContext = formatTokenWindow(maxContextTokens);
  if (!context && !maxContext) return "";
  if (maxContext && maxContextTokens !== contextTokens) {
    return `${context || "-"} / ${maxContext}`;
  }
  return context || maxContext;
}

function contextWindowTitle(contextTokens?: number, maxContextTokens?: number) {
  const parts = [];
  if (contextTokens) {
    parts.push(`Context window: ${n(contextTokens)} tokens`);
  }
  if (maxContextTokens && maxContextTokens !== contextTokens) {
    parts.push(`Max context window: ${n(maxContextTokens)} tokens`);
  }
  return parts.join("\n");
}

function formatTokenWindow(value?: number) {
  if (!value || value <= 0) return "";
  if (value >= 1_000_000) {
    return `${formatCompactUnit(value / 1_000_000)}M`;
  }
  if (value >= 1_000) {
    return `${formatCompactUnit(value / 1_000)}K`;
  }
  return String(value);
}

function formatCompactUnit(value: number) {
  if (Number.isInteger(value)) return String(value);
  return value.toFixed(1).replace(/\.0$/, "");
}

function UsageTable({ snapshot, rows, loading, now, scopeKey }: { snapshot?: ProviderUsageSnapshot; rows: UsageWindowRow[]; loading: boolean; now: number; scopeKey: string }) {
  const usage = snapshot?.usage;
  const rawSource = usage?.source ?? "";
  const source = useMemo(() => formatUsageSource(rawSource), [rawSource]);
  const observedAt = fmtTime(snapshot?.updated_at || usage?.observed_at);
  const changeValues = useMemo(() => usageChangeValues(usage, rows, rawSource, source, observedAt), [usage, rows, rawSource, source, observedAt]);
  const changed = useChangedHighlights(scopeKey, changeValues, Boolean(usage || rows.length));
  if (!loading && !usage && rows.length === 0) {
    return <div className="chat-empty">No usage reported</div>;
  }
  return (
    <div className="data-stack">
      <div className="usage-summary-strip">
        <SummaryMetric label="Requests" value={n(usage?.requests ?? 0)} changed={changed.has("summary:requests")} />
        <SummaryMetric label="Input" value={n(usage?.input_tokens ?? 0)} changed={changed.has("summary:input")} />
        <SummaryMetric label="Output" value={n(usage?.output_tokens ?? 0)} changed={changed.has("summary:output")} />
        <SummaryMetric label="Total Tokens" value={n(usage?.total_tokens ?? 0)} emphasis changed={changed.has("summary:total")} />
        <div className={cx("usage-source-summary", changed.has("summary:source") && "usage-value-changed")} title={rawSource || undefined}>
          <span>Collected From</span>
          <strong>{source.label}</strong>
          {source.detail ? <em>{source.detail}</em> : null}
          {rawSource ? (
            <button className="mini-icon usage-source-copy" type="button" aria-label="Copy raw usage source" title="Copy raw source" onClick={() => copyText(rawSource)}>
              <Clipboard aria-hidden="true" size={12} />
            </button>
          ) : null}
        </div>
        <SummaryMetric label="Observed" value={observedAt} changed={changed.has("summary:observed")} />
      </div>
      <div className="workbench-table-frame">
        <table className="workbench-table usage-table">
          <thead>
            <tr>
              <th>Limit</th>
              <th>Remaining</th>
              <th>Used / Limit</th>
              <th>Unit</th>
              <th>Reset At (Local)</th>
              <th>Time Left</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row, index) => {
              const remaining = remainingPercent(row);
              const rowKey = usageRowKey(row, index);
              return (
                <tr key={`${row.label}:${row.resetAt ?? ""}`}>
                  <td>{row.label}</td>
                  <td>
                    <div className={cx("usage-progress-cell", changed.has(`${rowKey}:remaining`) && "usage-value-changed")}>
                      <div className="usage-progress" aria-label={`${Math.round(remaining)} percent remaining`}>
                        <span className={progressTone(remaining)} style={{ width: `${remaining}%` }} />
                      </div>
                      <strong>{formatPercent(remaining)}</strong>
                    </div>
                  </td>
                  <td className="numeric"><span className={cx("usage-value-box", changed.has(`${rowKey}:usedLimit`) && "usage-value-changed")}>{formatUsedLimit(row)}</span></td>
                  <td><span className={cx("usage-value-box", changed.has(`${rowKey}:unit`) && "usage-value-changed")}>{row.unit || ""}</span></td>
                  <td><span className={cx("usage-value-box", changed.has(`${rowKey}:resetAt`) && "usage-value-changed")}>{formatLocalReset(row.resetAt)}</span></td>
                  <td className="mono">{formatTimeLeft(row.resetAt, now)}</td>
                </tr>
              );
            })}
            {!rows.length ? (
              <tr>
                <td colSpan={6}>
                  <span className="muted">No quota windows reported</span>
                </td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function SummaryMetric({ label, value, emphasis, changed }: { label: string; value: string; emphasis?: boolean; changed?: boolean }) {
  return (
    <div className={cx("usage-summary-metric", emphasis && "emphasis", changed && "usage-value-changed")}>
      <span>{label}</span>
      <strong>{value || "n/a"}</strong>
    </div>
  );
}

function usageChangeValues(usage: ProviderUsageSnapshot["usage"] | undefined, rows: UsageWindowRow[], rawSource: string, source: ReturnType<typeof formatUsageSource>, observedAt: string) {
  const values: Record<string, string> = {
    "summary:requests": n(usage?.requests ?? 0),
    "summary:input": n(usage?.input_tokens ?? 0),
    "summary:output": n(usage?.output_tokens ?? 0),
    "summary:total": n(usage?.total_tokens ?? 0),
    "summary:source": `${rawSource}|${source.label}|${source.detail}`,
    "summary:observed": observedAt,
  };
  rows.forEach((row, index) => {
    const key = usageRowKey(row, index);
    values[`${key}:remaining`] = formatPercent(remainingPercent(row));
    values[`${key}:usedLimit`] = formatUsedLimit(row);
    values[`${key}:unit`] = row.unit || "";
    values[`${key}:resetAt`] = formatLocalReset(row.resetAt);
  });
  return values;
}

function usageRowKey(row: UsageWindowRow, index: number) {
  return `row:${index}:${row.label}:${row.unit ?? ""}`;
}

function useChangedHighlights(scopeKey: string, values: Record<string, string>, active: boolean, durationMs = 2_000) {
  const previousRef = useRef<Record<string, string> | null>(null);
  const scopeRef = useRef(scopeKey);
  const [changed, setChanged] = useState<Set<string>>(() => new Set());
  const signature = stableValueSignature(values);

  useEffect(() => {
    if (scopeRef.current === scopeKey) return;
    scopeRef.current = scopeKey;
    previousRef.current = null;
    setChanged(new Set());
  }, [scopeKey]);

  useEffect(() => {
    if (!active) return;
    const previous = previousRef.current;
    previousRef.current = values;
    if (!previous) {
      setChanged(new Set());
      return;
    }
    const changedKeys = Object.keys(values).filter((key) => previous[key] !== values[key]);
    if (!changedKeys.length) return;
    setChanged((current) => {
      const next = new Set(current);
      changedKeys.forEach((key) => next.add(key));
      return next;
    });
    const timeout = window.setTimeout(() => {
      setChanged((current) => {
        const next = new Set(current);
        changedKeys.forEach((key) => next.delete(key));
        return next;
      });
    }, durationMs);
    return () => window.clearTimeout(timeout);
  }, [active, durationMs, scopeKey, signature]);

  return changed;
}

function stableValueSignature(values: Record<string, string>) {
  return Object.keys(values).sort().map((key) => `${key}=${values[key]}`).join("\n");
}

function formatUsageSource(source?: string) {
  const raw = source?.trim();
  if (!raw) {
    return { label: "n/a", detail: "" };
  }
  const normalized = raw
    .replace(/^codex-appserver-websocket\+codex-auth-json-format\/usage-probe$/, "Codex AppServer / Auth JSON|websocket · usage probe")
    .replace(/^codex-appserver-websocket$/, "Codex AppServer|websocket");
  if (normalized.includes("|")) {
    const [label, detail] = normalized.split("|", 2);
    return { label, detail };
  }
  const parts = raw.split(/[+/]/).filter(Boolean);
  if (parts.length > 1) {
    return {
      label: titleCaseSource(parts[0]),
      detail: parts.slice(1).map(sourceTokenLabel).join(" · "),
    };
  }
  return { label: titleCaseSource(raw), detail: "" };
}

function titleCaseSource(value: string) {
  return value
    .split(/[-_]/)
    .filter(Boolean)
    .map((part) => part.toLowerCase() === "json" ? "JSON" : part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function sourceTokenLabel(value: string) {
  return value
    .replace(/-/g, " ")
    .replace(/\bjson\b/gi, "JSON")
    .replace(/\bauth\b/gi, "auth")
    .replace(/\bappserver\b/gi, "AppServer");
}

function modelsForProtocol(protocol: ProviderProtocol, token?: string) {
  switch (protocol) {
    case "openai":
      return api.openAIModels(token);
    case "anthropic":
      return api.anthropicModels(token);
    case "gemini":
      return api.geminiModels(token);
  }
}

function normalizeModelRows(endpoint: ServiceEndpoint, providerModels: ProviderModel[], rawModels: unknown): ModelRow[] {
  const rows = new Map<string, ModelRow>();
  const rawRows = normalizeRawModelRows(endpoint, rawModels);
  const rawByID = new Map(rawRows.map((row) => [row.id, row]));
  const publicModelIDs = new Set(rawRows.map((row) => row.id).filter(Boolean));
  for (const model of providerModels) {
    const raw = rawByID.get(model.id);
    const key = `${endpoint.protocol}:${model.id}`;
    rows.set(key, {
      id: model.id,
      display: raw?.display,
      aliases: normalizeModelAliases(model.id, model.aliases ?? [], publicModelIDs),
      protocol: endpoint.protocolLabel,
      capabilities: model.capabilities ?? [],
      contextTokens: model.context_tokens,
      maxContextTokens: model.max_context_tokens,
      owner: raw?.owner,
      version: raw?.version,
    });
  }
  if (rows.size > 0) {
    return [...rows.values()].sort((a, b) => a.id.localeCompare(b.id));
  }
  for (const row of rawRows) {
    const key = `${endpoint.protocol}:${row.id}`;
    rows.set(key, row);
  }
  return [...rows.values()].sort((a, b) => a.id.localeCompare(b.id));
}

function normalizeModelAliases(modelID: string, aliases: string[], publicModelIDs: Set<string>): ModelAlias[] {
  const out: ModelAlias[] = [];
  const seen = new Set<string>();
  for (const raw of aliases) {
    const value = raw.trim();
    if (!value || value === modelID || seen.has(value)) {
      continue;
    }
    seen.add(value);
    out.push({
      value,
      source: publicModelIDs.has(value) ? "route" : "provider",
    });
  }
  return out;
}

function normalizeRawModelRows(endpoint: ServiceEndpoint, rawModels: unknown): ModelRow[] {
  const root = asRecord(rawModels);
  if (!root) return [];
  if (endpoint.protocol === "gemini") {
    return asArray(root.models).map((item) => {
      const record = asRecord(item) ?? {};
      const name = stringValue(record.name);
      return {
        id: name.replace(/^models\//, "") || name,
        display: stringValue(record.displayName) || name,
        protocol: endpoint.protocolLabel,
        capabilities: asArray(record.supportedGenerationMethods).map((value) => String(value)),
        version: stringValue(record.version),
      };
    }).filter((row) => row.id);
  }
  return asArray(root.data).map((item) => {
    const record = asRecord(item) ?? {};
    return {
      id: stringValue(record.id),
      display: stringValue(record.display_name) || stringValue(record.id),
      protocol: endpoint.protocolLabel,
      owner: stringValue(record.owned_by) || stringValue(record.type),
      version: typeof record.created === "number" ? String(record.created) : "",
    };
  }).filter((row) => row.id);
}

function uniqueStrings(values: string[]) {
  const seen = new Set<string>();
  const out: string[] = [];
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

function capabilityMeta(capability: string): { label: string; icon: LucideIcon; tone: string } {
  switch (capability) {
    case "api.openai.chat":
      return { label: "OpenAI Chat", icon: MessageSquare, tone: "openai" };
    case "api.openai.responses":
      return { label: "OpenAI Responses", icon: Bot, tone: "openai" };
    case "api.anthropic.messages":
      return { label: "Anthropic Messages", icon: Braces, tone: "anthropic" };
    case "api.gemini.generateContent":
      return { label: "Gemini Generate Content", icon: Sparkles, tone: "gemini" };
    case "stream.sse":
      return { label: "SSE Streaming", icon: Radio, tone: "stream" };
    case "models.read":
      return { label: "Models Read", icon: ListChecks, tone: "meta" };
    case "usage.read":
      return { label: "Usage Read", icon: Activity, tone: "meta" };
    default:
      return { label: capability, icon: Braces, tone: "default" };
  }
}

function normalizeUsageRows(snapshot?: ProviderUsageSnapshot): UsageWindowRow[] {
  const native = asRecord(snapshot?.usage?.native_summary);
  if (!native) return [];
  const rows = asArray(native.windows).map((item) => usageWindowFromRecord(asRecord(item))).filter((row): row is UsageWindowRow => !!row);
  const summary = usageWindowFromRecord(native, "Current window");
  if (summary && !rows.some((row) => duplicateUsageWindow(summary, row))) {
    rows.unshift(summary);
  }
  return rows;
}

function duplicateUsageWindow(left: UsageWindowRow, right: UsageWindowRow) {
  if (left.label === right.label && left.resetAt === right.resetAt) return true;
  if (!left.resetAt || left.resetAt !== right.resetAt) return false;
  if (left.unit && right.unit && left.unit !== right.unit) return false;
  if (!sameOptionalNumber(left.remainingPct, right.remainingPct)) return false;
  if (!sameOptionalNumber(left.used, right.used)) return false;
  if (!sameOptionalNumber(left.limit, right.limit)) return false;
  return true;
}

function sameOptionalNumber(left?: number, right?: number) {
  if (left === undefined || right === undefined) return true;
  return Math.abs(left - right) < 0.001;
}

function usageWindowFromRecord(record?: Record<string, unknown> | null, fallbackLabel = "Limit"): UsageWindowRow | null {
  if (!record) return null;
  const label = stringValue(record.label) || fallbackLabel;
  const remainingPct = numberValue(record.remaining_pct);
  const used = numberValue(record.used);
  const limit = numberValue(record.limit);
  const unit = stringValue(record.unit);
  const resetAt = stringValue(record.reset_at);
  if (remainingPct === undefined && used === undefined && limit === undefined && !resetAt) {
    return null;
  }
  return { label, remainingPct, used, limit, unit, resetAt };
}

function remainingPercent(row: UsageWindowRow) {
  if (row.remainingPct !== undefined) {
    return clampPercent(row.remainingPct);
  }
  if (row.limit && row.used !== undefined) {
    return clampPercent(100 - (row.used / row.limit) * 100);
  }
  return 0;
}

function progressTone(value: number) {
  if (value < 10) return "danger";
  if (value < 25) return "warn";
  return "ok";
}

function clampPercent(value: number) {
  return Math.max(0, Math.min(100, value));
}

function formatPercent(value: number) {
  return `${Math.round(value)}%`;
}

function formatUsedLimit(row: UsageWindowRow) {
  if (row.used === undefined && row.limit === undefined) return "";
  return `${row.used !== undefined ? n(row.used) : "-"} / ${row.limit !== undefined ? n(row.limit) : "-"}`;
}

function formatLocalReset(value?: string) {
  const date = parseDate(value);
  if (!date) return "";
  return `${pad2(date.getMonth() + 1)}-${pad2(date.getDate())} ${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
}

function formatTimeLeft(value: string | undefined, now: number) {
  const date = parseDate(value);
  if (!date) return "";
  const totalSeconds = Math.max(0, Math.floor((date.getTime() - now) / 1000));
  const days = Math.floor(totalSeconds / 86_400);
  const secondsAfterDays = totalSeconds % 86_400;
  const minutes = Math.floor(secondsAfterDays / 60);
  const seconds = secondsAfterDays % 60;
  if (days > 0) {
    return `${days}d ${pad2(minutes)}:${pad2(seconds)}`;
  }
  return `${pad2(minutes)}:${pad2(seconds)}`;
}

function parseDate(value?: string) {
  if (!value) return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

function pad2(value: number) {
  return String(value).padStart(2, "0");
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value : "";
}

function numberValue(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

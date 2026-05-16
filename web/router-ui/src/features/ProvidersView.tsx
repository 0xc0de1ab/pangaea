import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Activity, Box, Clipboard, Database, HeartPulse, KeyRound, ListChecks, MessageSquare, PauseCircle, PlayCircle, RadioTower, RefreshCw, Trash2, Unplug } from "lucide-react";
import type { DashboardViewProps } from "../app/dashboard";
import { DataTable, type DashboardColumn } from "../components/DataTable";
import { Drawer } from "../components/Drawer";
import { Section } from "../components/Section";
import { ServiceBadge, ServiceIcon } from "../components/ServiceIcon";
import { StatusBadge } from "../components/StatusBadge";
import { ChatWorkbench, type ChatWorkbenchTarget } from "./ChatWorkbench";
import { EndpointDataWorkbench, type EndpointDataWorkbenchTarget } from "./EndpointDataWorkbench";
import { api } from "../lib/api";
import { providerAccountLabel, providerInstanceID, providerUsageMap, sessionSet, serviceHostAccount } from "../lib/derive";
import { age, copyText, cx, fmtTime, hasText, middleEllipsis, n } from "../lib/format";
import { isAliasProviderModel, isGroupProviderModel } from "../lib/model-flags";
import { providerServiceEndpoints, serviceLabel, type ServiceEndpoint } from "../lib/service-endpoints";
import type { ProviderIdentity, ProviderRegistration, ProviderUsageSnapshot } from "../lib/types";
import dockerIcon from "../assets/icons/docker-mark-ocean-blue.svg";
import kubernetesIcon from "../assets/icons/kubernetes.svg";

export function ProvidersView({ data, queries, search, token, onAction, refresh }: DashboardViewProps) {
  const [selectedProviderID, setSelectedProviderID] = useState<string | null>(null);
  const [selectedDeleteIDs, setSelectedDeleteIDs] = useState<Set<string>>(() => new Set());
  const [chatTarget, setChatTarget] = useState<ChatWorkbenchTarget | null>(null);
  const [dataTarget, setDataTarget] = useState<EndpointDataWorkbenchTarget | null>(null);
  const rows = useMemo(() => {
    return [...data.providers]
      .sort((a, b) => `${a.identity.service}:${a.identity.host_name}:${providerAccountLabel(a)}:${providerInstanceID(a)}`.localeCompare(`${b.identity.service}:${b.identity.host_name}:${providerAccountLabel(b)}:${providerInstanceID(b)}`))
      .filter((provider) => hasText(provider, search));
  }, [data.providers, search]);
  const control = useMemo(() => sessionSet(data.controlSessions), [data.controlSessions]);
  const dataSession = useMemo(() => sessionSet(data.dataSessions), [data.dataSessions]);
  const usage = useMemo(() => providerUsageMap(data.usage), [data.usage]);
  const selected = useMemo(
    () => selectedProviderID ? data.providers.find((provider) => providerInstanceID(provider) === selectedProviderID) ?? null : null,
    [data.providers, selectedProviderID],
  );
  const selectedUsage = selected ? usage.get(providerInstanceID(selected)) : undefined;
  const selectedControlConnected = selected ? control.has(providerInstanceID(selected)) : false;
  const selectedDataConnected = selected ? dataSession.has(providerInstanceID(selected)) : false;
  const disconnectedIDs = useMemo(() => {
    const out = new Set<string>();
    for (const provider of rows) {
      const id = providerInstanceID(provider);
      if (!control.has(id) && !dataSession.has(id)) {
        out.add(id);
      }
    }
    return out;
  }, [control, dataSession, rows]);
  const selectedDeleteCount = selectedDeleteIDs.size;
  const deletableRows = rows.filter((row) => disconnectedIDs.has(providerInstanceID(row)));
  const allVisibleDeletableSelected = deletableRows.length > 0 && deletableRows.every((row) => selectedDeleteIDs.has(providerInstanceID(row)));
  const detailChangeValues = useMemo(
    () => selected ? providerDetailChangeValues(selected, selectedUsage, selectedControlConnected, selectedDataConnected) : {},
    [selected, selectedUsage, selectedControlConnected, selectedDataConnected],
  );
  const detailChanged = useChangedHighlights(selectedProviderID ?? "", detailChangeValues, Boolean(selected));

  useEffect(() => {
    if (selectedProviderID && !selected && !queries.providers.isFetching) {
      setSelectedProviderID(null);
    }
  }, [queries.providers.isFetching, selected, selectedProviderID]);

  useEffect(() => {
    setSelectedDeleteIDs((current) => {
      const next = new Set([...current].filter((id) => disconnectedIDs.has(id)));
      return next.size === current.size ? current : next;
    });
  }, [disconnectedIDs]);

  function toggleDeleteSelected(providerID: string, checked: boolean) {
    if (!disconnectedIDs.has(providerID)) return;
    setSelectedDeleteIDs((current) => {
      const next = new Set(current);
      if (checked) {
        next.add(providerID);
      } else {
        next.delete(providerID);
      }
      return next;
    });
  }

  function toggleVisibleDeleteSelected(checked: boolean) {
    setSelectedDeleteIDs((current) => {
      const next = new Set(current);
      for (const provider of deletableRows) {
        const id = providerInstanceID(provider);
        if (checked) {
          next.add(id);
        } else {
          next.delete(id);
        }
      }
      return next;
    });
  }

  function deleteSelectedProviders() {
    const ids = [...selectedDeleteIDs];
    if (!ids.length) return;
    onAction({
      title: "Delete disconnected providers",
      target: ids.length === 1 ? ids[0] : `${ids.length} providers`,
      detail: "Selected disconnected provider registrations, usage snapshots, container snapshots, and auth sync records will be removed from router memory. A running provider will re-register if it reconnects.",
      requireReason: true,
      danger: true,
      confirmLabel: "Delete providers",
      execute: async (reason) => {
        await api.deleteProviders(ids, reason, token);
        setSelectedDeleteIDs(new Set());
        setSelectedProviderID((id) => id && ids.includes(id) ? null : id);
        refresh();
      },
    });
  }

  function providerAction(provider: ProviderRegistration, kind: "drain" | "resume" | "refresh") {
    const id = providerInstanceID(provider);
    const scope = serviceHostAccount(provider);
    onAction({
      title: kind === "drain" ? "Drain provider" : kind === "resume" ? "Resume routing" : "Refresh provider auth",
      target: id,
      detail:
        kind === "drain"
          ? `New traffic will stop routing to ${scope}. Existing streams may continue.`
          : kind === "resume"
            ? `Routing can resume for ${scope} after the provider acknowledges the control command.`
            : `The provider shim will run its configured auth refresh flow for ${scope}.`,
      requireReason: true,
      confirmLabel: kind === "refresh" ? "Refresh auth" : kind === "resume" ? "Resume" : "Drain",
      danger: kind === "drain",
      execute: async (reason) => {
        if (kind === "refresh") {
          await api.providerAuthRefresh(id, reason, token);
        } else {
          await api.providerDrain(id, kind === "drain", reason, token);
        }
        refresh();
      },
    });
  }

  const columns: DashboardColumn<ProviderRegistration>[] = [
    {
      id: "select",
      header: "Sel",
      cell: (row) => {
        const id = providerInstanceID(row);
        const disconnected = disconnectedIDs.has(id);
        return (
          <input
            type="checkbox"
            aria-label={`Select ${id}`}
            checked={selectedDeleteIDs.has(id)}
            disabled={!disconnected}
            title={disconnected ? "Select disconnected provider for deletion" : "Connected providers cannot be deleted"}
            onChange={(event) => toggleDeleteSelected(id, event.currentTarget.checked)}
            onClick={(event) => event.stopPropagation()}
          />
        );
      },
      width: "48px",
      align: "center",
    },
    {
      id: "node",
      header: "Node",
      sortValue: (row) => row.identity.node_id,
      cell: (row) => (
        <span className="id-cell provider-node-cell" title={providerInstanceID(row)}>
          <ServiceLogoCell service={row.identity.service} />
          <span className="mono">{middleEllipsis(row.identity.node_id, 10, 6)}</span>
          <button className="mini-icon" type="button" aria-label="Copy node id" onClick={(event) => { event.stopPropagation(); copyText(row.identity.node_id); }}>
            <Clipboard aria-hidden="true" size={13} />
          </button>
        </span>
      ),
      width: "152px",
    },
    { id: "kind", header: "Kind", sortValue: (row) => row.identity.kind, cell: (row) => row.identity.kind, width: "138px" },
    {
      id: "apis",
      header: "Service APIs",
      sortValue: (row) => providerServiceEndpoints(row).map((endpoint) => endpoint.label).join(","),
      cell: (row) => <ServiceBadgeRow provider={row} />,
      width: "122px",
    },
    { id: "host", header: "Host", sortValue: (row) => row.identity.host_name, cell: (row) => <HostCell identity={row.identity} />, width: "172px" },
    { id: "account", header: "Account", sortValue: (row) => providerAccountLabel(row), cell: (row) => providerAccountLabel(row), width: "190px" },
    {
      id: "health",
      header: "H",
      sortValue: (row) => row.health?.status,
      cell: (row) => <StatusBadge value={row.health?.status} title={providerHealthTitle(row)} icon={HeartPulse} iconOnly />,
      width: "48px",
      align: "center",
    },
    {
      id: "auth",
      header: "A",
      sortValue: (row) => row.auth?.status,
      cell: (row) => <StatusBadge value={row.auth?.status} title={providerAuthTitle(row)} icon={KeyRound} iconOnly />,
      width: "48px",
      align: "center",
    },
    {
      id: "sessions",
      header: "C/D",
      sortValue: (row) => Number(control.has(providerInstanceID(row))) + Number(dataSession.has(providerInstanceID(row))),
      cell: (row) => (
        <div className="session-pair session-icon-pair">
          <StatusBadge
            value={control.has(providerInstanceID(row)) ? "control connected" : "control missing"}
            tone={control.has(providerInstanceID(row)) ? "ok" : "warn"}
            title={sessionTitle("Control", control.has(providerInstanceID(row)), row)}
            icon={RadioTower}
            iconOnly
          />
          <StatusBadge
            value={dataSession.has(providerInstanceID(row)) ? "data connected" : "data missing"}
            tone={dataSession.has(providerInstanceID(row)) ? "ok" : "danger"}
            title={sessionTitle("Data", dataSession.has(providerInstanceID(row)), row)}
            icon={Database}
            iconOnly
          />
        </div>
      ),
      width: "74px",
      align: "center",
    },
    { id: "models", header: "Models", sortValue: (row) => row.models?.length ?? 0, cell: (row) => n(row.models?.length ?? 0), align: "right", width: "80px" },
    { id: "queue", header: "Queue", sortValue: (row) => row.limits?.queue_depth ?? 0, cell: (row) => n(row.limits?.queue_depth ?? 0), align: "right", width: "76px" },
    { id: "streams", header: "Streams", sortValue: (row) => row.limits?.active_streams ?? 0, cell: (row) => n(row.limits?.active_streams ?? 0), align: "right", width: "92px" },
    {
      id: "usage",
      header: "Requests",
      sortValue: (row) => usage.get(providerInstanceID(row))?.usage?.requests ?? 0,
      cell: (row) => n(usage.get(providerInstanceID(row))?.usage?.requests ?? 0),
      align: "right",
      width: "98px",
    },
    {
      id: "actions",
      header: "Actions",
      cell: (row) => (
        <div className="row-actions">
          <button className="icon-button small" type="button" title="Drain" onClick={(event) => { event.stopPropagation(); providerAction(row, "drain"); }}>
            <PauseCircle aria-hidden="true" size={15} />
          </button>
          <button className="icon-button small" type="button" title="Resume routing" onClick={(event) => { event.stopPropagation(); providerAction(row, "resume"); }}>
            <PlayCircle aria-hidden="true" size={15} />
          </button>
          <button className="icon-button small" type="button" title="Refresh auth" onClick={(event) => { event.stopPropagation(); providerAction(row, "refresh"); }}>
            <KeyRound aria-hidden="true" size={15} />
          </button>
        </div>
      ),
      width: "118px",
    },
  ];

  return (
    <div className="view-stack">
      <Section
        title="Providers"
        subtitle="Grouped by service, host name, account, and provider instance"
        error={queries.providers.error}
        actions={
          <>
            <button
              className={cx("button secondary", allVisibleDeletableSelected && "selected")}
              type="button"
              disabled={deletableRows.length === 0}
              title={deletableRows.length === 0 ? "No disconnected providers visible" : `${allVisibleDeletableSelected ? "Clear" : "Select"} ${n(deletableRows.length)} disconnected providers`}
              aria-pressed={allVisibleDeletableSelected}
              onClick={() => toggleVisibleDeleteSelected(!allVisibleDeletableSelected)}
            >
              <Unplug aria-hidden="true" size={15} />
              {allVisibleDeletableSelected ? "Clear disconnected" : "Select disconnected"}
            </button>
            <button className="button danger" type="button" disabled={selectedDeleteCount === 0} onClick={deleteSelectedProviders}>
              <Trash2 aria-hidden="true" size={15} />
              Delete {selectedDeleteCount ? n(selectedDeleteCount) : ""}
            </button>
            <button className="button secondary" type="button" onClick={refresh}>
              <RefreshCw aria-hidden="true" size={15} />
              Refresh
            </button>
          </>
        }
      >
        <DataTable rows={rows} columns={columns} empty="No providers registered" getRowId={(row) => providerInstanceID(row)} onRowClick={(row) => setSelectedProviderID(providerInstanceID(row))} compact />
      </Section>

      <Drawer
        open={!!selected}
        onClose={() => setSelectedProviderID(null)}
        title={selected ? middleEllipsis(providerInstanceID(selected), 18, 12) : "Provider"}
        subtitle={selected ? serviceHostAccount(selected) : undefined}
      >
        {selected ? (
          <ProviderDetail
            provider={selected}
            controlConnected={selectedControlConnected}
            dataConnected={selectedDataConnected}
            usage={selectedUsage}
            changed={detailChanged}
            onDrain={() => providerAction(selected, "drain")}
            onResume={() => providerAction(selected, "resume")}
            onRefreshAuth={() => providerAction(selected, "refresh")}
            onOpenChat={(endpoint) => setChatTarget({ provider: selected, endpoint })}
            onOpenModels={() => setDataTarget({ kind: "models", provider: selected })}
            onOpenUsage={() => setDataTarget({ kind: "usage", provider: selected, usage: usage.get(providerInstanceID(selected)) })}
          />
        ) : null}
      </Drawer>
      <ChatWorkbench target={chatTarget} token={token} onClose={() => setChatTarget(null)} />
      <EndpointDataWorkbench target={dataTarget} token={token} onClose={() => setDataTarget(null)} />
    </div>
  );
}

function providerDetailChangeValues(provider: ProviderRegistration, usage: ProviderUsageSnapshot | undefined, controlConnected: boolean, dataConnected: boolean) {
  const subscription = providerSubscription(provider, usage);
  return {
    "summary:providerType": provider.identity.provider_type,
    "summary:targetVersion": provider.identity.target_version ?? "",
    "summary:instance": provider.identity.provider_instance_id,
    "summary:node": provider.identity.node_id,
    "summary:host": `${provider.identity.host_name}|${containerSummary(provider.identity)}`,
    "summary:account": providerAccountLabel(provider),
    "summary:subscription": subscription ? JSON.stringify(subscription) : "",
    "summary:container": containerSummary(provider.identity),
    "summary:registered": fmtTime(provider.registered_at),
    "state:badges": [
      provider.health?.status ?? "",
      provider.health?.reason ?? "",
      provider.auth?.status ?? "",
      provider.auth?.last_refresh_error ?? "",
      controlConnected ? "control" : "no-control",
      dataConnected ? "data" : "no-data",
    ].join("|"),
    "state:healthCheck": provider.health?.checked_at ?? "",
    "state:authExpires": provider.auth?.expires_at ?? "",
    "state:lastRefresh": provider.auth?.last_refresh_at ?? "",
    "state:refreshable": provider.auth?.refreshable ? "yes" : "no",
    "models:list": (provider.models ?? []).map((model) => [
      model.id,
      model.kind ?? "",
      model.aliases?.join(",") ?? "",
      model.capabilities?.join(",") ?? "",
      model.context_tokens ?? "",
      model.max_context_tokens ?? "",
      model.max_output_tokens ?? "",
    ].join(":")).join("|"),
    "load:queue": String(provider.limits?.queue_depth ?? 0),
    "load:streams": String(provider.limits?.active_streams ?? 0),
    "load:maxConcurrency": String(provider.limits?.max_concurrency ?? 0),
    "load:requests": String(usage?.usage?.requests ?? 0),
    "load:tokens": String(usage?.usage?.total_tokens ?? 0),
    "load:usageAge": usage?.updated_at || usage?.usage?.observed_at || "",
    "capabilities:list": (provider.capabilities ?? []).join("|"),
  };
}

function providerHealthTitle(provider: ProviderRegistration) {
  return compactTooltipLines([
    `Health: ${provider.health?.status || "unknown"}`,
    provider.health?.reason ? `Reason: ${provider.health.reason}` : "",
    provider.health?.checked_at ? `Checked: ${fmtTime(provider.health.checked_at)}` : "",
  ]);
}

function providerAuthTitle(provider: ProviderRegistration) {
  return compactTooltipLines([
    `Auth: ${provider.auth?.status || "unknown"}`,
    provider.auth?.last_refresh_error ? `Error: ${provider.auth.last_refresh_error}` : "",
    provider.auth?.expires_at ? `Expires: ${fmtTime(provider.auth.expires_at)}` : "",
    provider.auth?.last_refresh_at ? `Last refresh: ${fmtTime(provider.auth.last_refresh_at)}` : "",
    provider.auth?.selected_source ? `Source: ${provider.auth.selected_source}` : "",
    typeof provider.auth?.refreshable === "boolean" ? `Refreshable: ${provider.auth.refreshable ? "yes" : "no"}` : "",
  ]);
}

function sessionTitle(kind: "Control" | "Data", connected: boolean, provider: ProviderRegistration) {
  return compactTooltipLines([
    `${kind} session: ${connected ? "connected" : "missing"}`,
    `Provider: ${providerInstanceID(provider)}`,
    `Node: ${provider.identity.node_id}`,
    provider.identity.host_name ? `Host: ${provider.identity.host_name}` : "",
  ]);
}

function compactTooltipLines(lines: Array<string | undefined>) {
  return lines.map((line) => line?.trim()).filter(Boolean).join("\n");
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

type ServiceTooltipPosition = {
  top: number;
  left: number;
};

function ServiceLogoCell({ service }: { service: string }) {
  const [position, setPosition] = useState<ServiceTooltipPosition | null>(null);
  const label = serviceLabel(service);

  function showTooltip(target: HTMLElement) {
    const rect = target.getBoundingClientRect();
    const left = Math.min(Math.max(rect.left + rect.width / 2, 92), window.innerWidth - 92);
    setPosition({ left, top: rect.bottom + 9 });
  }

  return (
    <span
      className="provider-service-logo"
      tabIndex={0}
      role="img"
      aria-label={label}
      title={label}
      onMouseEnter={(event) => showTooltip(event.currentTarget)}
      onMouseLeave={() => setPosition(null)}
      onFocus={(event) => showTooltip(event.currentTarget)}
      onBlur={() => setPosition(null)}
    >
      <ServiceIcon service={service} size={22} label={label} />
      {position
        ? createPortal(
            <div className="service-logo-popover" style={{ left: position.left, top: position.top }} aria-hidden="true">
              <ServiceIcon service={service} size={42} label={label} />
              <strong>{label}</strong>
            </div>,
            document.body,
          )
        : null}
    </span>
  );
}

function ServiceBadgeRow({ provider }: { provider: ProviderRegistration }) {
  const endpoints = providerServiceEndpoints(provider);
  if (!endpoints.length) {
    return <span className="muted">none</span>;
  }
  return (
    <div className="service-icon-row">
      {endpoints.map((endpoint) => (
        <ServiceBadge endpoint={endpoint} compact key={endpoint.id} />
      ))}
    </div>
  );
}

function HostCell({ identity }: { identity: ProviderIdentity }) {
  const [position, setPosition] = useState<ServiceTooltipPosition | null>(null);
  const meta = containerMeta(identity);
  const runtime = meta ? containerRuntime(meta.kind) : null;

  function showTooltip(target: HTMLElement) {
    if (!meta) return;
    const rect = target.getBoundingClientRect();
    const left = Math.min(Math.max(rect.left + rect.width / 2, 128), window.innerWidth - 128);
    setPosition({ left, top: rect.bottom + 9 });
  }

  return (
    <span className="host-cell">
      <span className="host-name">{identity.host_name}</span>
      {meta ? (
        <span
          className="container-indicator"
          tabIndex={0}
          role="img"
          aria-label={`Container ${meta.name || meta.id || meta.kind}`}
          onMouseEnter={(event) => showTooltip(event.currentTarget)}
          onMouseLeave={() => setPosition(null)}
          onFocus={(event) => showTooltip(event.currentTarget)}
          onBlur={() => setPosition(null)}
        >
          <ContainerGlyph runtime={runtime} size={14} />
          {position
            ? createPortal(
                <div className="container-popover" style={{ left: position.left, top: position.top }} aria-hidden="true">
                  <ContainerGlyph runtime={runtime} size={30} />
                  <div>
                    <strong>{runtime?.label || meta.kind || "container"}</strong>
                    <span>Name: {meta.name || "-"}</span>
                    <span>ID: {middleEllipsis(meta.id || "-", 12, 10)}</span>
                  </div>
                </div>,
                document.body,
              )
            : null}
        </span>
      ) : null}
    </span>
  );
}

function containerMeta(identity: ProviderIdentity) {
  const kind = identity.container_kind || "";
  const name = identity.container_name || "";
  const id = identity.container_id || "";
  if (!kind && !name && !id) return null;
  return { kind, name, id };
}

function containerSummary(identity: ProviderIdentity) {
  const meta = containerMeta(identity);
  if (!meta) return "";
  return [meta.kind, meta.name, meta.id].filter(Boolean).join(" / ");
}

function containerRuntime(kind: string) {
  const normalized = kind.trim().toLowerCase();
  if (normalized === "docker" || normalized === "containerd") {
    return { icon: dockerIcon, label: normalized === "containerd" ? "containerd" : "Docker" };
  }
  if (normalized === "kubernetes" || normalized === "k8s") {
    return { icon: kubernetesIcon, label: "Kubernetes" };
  }
  return null;
}

function ContainerGlyph({ runtime, size }: { runtime: ReturnType<typeof containerRuntime>; size: number }) {
  if (runtime) {
    return <img className="container-runtime-icon" src={runtime.icon} alt="" aria-hidden="true" style={{ width: size, height: size }} />;
  }
  return <Box aria-hidden="true" size={size} />;
}

type ProviderDetailProps = {
  provider: ProviderRegistration;
  controlConnected: boolean;
  dataConnected: boolean;
  usage?: ProviderUsageSnapshot;
  changed: Set<string>;
  onDrain: () => void;
  onResume: () => void;
  onRefreshAuth: () => void;
  onOpenChat: (endpoint: ServiceEndpoint) => void;
  onOpenModels: () => void;
  onOpenUsage: () => void;
};

function ProviderDetail({ provider, controlConnected, dataConnected, usage, changed, onDrain, onResume, onRefreshAuth, onOpenChat, onOpenModels, onOpenUsage }: ProviderDetailProps) {
  return (
    <div className="detail-stack">
      <div className="drawer-action-row">
        <button className="button danger" type="button" onClick={onDrain}>
          <PauseCircle aria-hidden="true" size={15} />
          Drain
        </button>
        <button className="button secondary" type="button" onClick={onResume}>
          <PlayCircle aria-hidden="true" size={15} />
          Resume
        </button>
        <button className="button secondary" type="button" onClick={onRefreshAuth}>
          <KeyRound aria-hidden="true" size={15} />
          Refresh auth
        </button>
      </div>

      <div className="detail-section">
        <h3>Summary</h3>
        <div className="kv-list">
          <div className="kv-key">Provider Type</div><DetailValue changed={changed} changeKey="summary:providerType" className="mono">{provider.identity.provider_type}</DetailValue>
          <div className="kv-key">Target Version</div><DetailValue changed={changed} changeKey="summary:targetVersion" className="mono">{provider.identity.target_version || "Not reported"}</DetailValue>
          <div className="kv-key">Instance</div><DetailValue changed={changed} changeKey="summary:instance" className="mono">{provider.identity.provider_instance_id}</DetailValue>
          <div className="kv-key">Node</div><DetailValue changed={changed} changeKey="summary:node" className="mono">{provider.identity.node_id}</DetailValue>
          <div className="kv-key">Host</div><DetailValue changed={changed} changeKey="summary:host"><HostCell identity={provider.identity} /></DetailValue>
          <div className="kv-key">Account</div><DetailValue changed={changed} changeKey="summary:account">{providerAccountLabel(provider)}</DetailValue>
          <div className="kv-key">Your Plan</div><DetailValue changed={changed} changeKey="summary:subscription"><SubscriptionValue provider={provider} usage={usage} /></DetailValue>
          <div className="kv-key">Container</div><DetailValue changed={changed} changeKey="summary:container" className="mono">{containerSummary(provider.identity)}</DetailValue>
          <div className="kv-key">Registered</div><DetailValue changed={changed} changeKey="summary:registered">{fmtTime(provider.registered_at)}</DetailValue>
        </div>
      </div>

      <div className="detail-section">
        <h3>State</h3>
        <div className={cx("badge-row", changed.has("state:badges") && "provider-value-changed")}>
          <StatusBadge value={provider.health?.status} title={provider.health?.reason} />
          <StatusBadge value={provider.auth?.status} title={provider.auth?.last_refresh_error} />
          <StatusBadge value={controlConnected ? "control connected" : "control missing"} tone={controlConnected ? "ok" : "warn"} />
          <StatusBadge value={dataConnected ? "data connected" : "data missing"} tone={dataConnected ? "ok" : "danger"} />
        </div>
        <div className="kv-list">
          <div className="kv-key">Health check</div><DetailValue changed={changed} changeKey="state:healthCheck">{age(provider.health?.checked_at)}</DetailValue>
          <div className="kv-key">Auth expires</div><DetailValue changed={changed} changeKey="state:authExpires">{fmtTime(provider.auth?.expires_at)}</DetailValue>
          <div className="kv-key">Last refresh</div><DetailValue changed={changed} changeKey="state:lastRefresh">{fmtTime(provider.auth?.last_refresh_at)}</DetailValue>
          <div className="kv-key">Refreshable</div><DetailValue changed={changed} changeKey="state:refreshable">{provider.auth?.refreshable ? "yes" : "no"}</DetailValue>
        </div>
      </div>

      <ProviderEndpointTable provider={provider} onOpenChat={onOpenChat} />

      <div className="detail-section">
        <div className="detail-section-heading">
          <h3>Models</h3>
          <button className="button secondary compact" type="button" onClick={onOpenModels}>
            <ListChecks aria-hidden="true" size={14} />
            Open
          </button>
        </div>
        <div className={cx("tag-list", changed.has("models:list") && "provider-value-changed")}>
          {(provider.models ?? []).map((model) => (
            <span className={isGroupProviderModel(provider.identity.service, model) || isAliasProviderModel(model) ? "tag mono model-tag-group" : "tag mono"} key={model.id}>
              {isGroupProviderModel(provider.identity.service, model) ? <span className="model-group-badge mini" title="Group model">G</span> : null}
              {isAliasProviderModel(model) ? <span className="model-alias-badge mini" title="Alias model">A</span> : null}
              {model.id}
            </span>
          ))}
          {!provider.models?.length ? <span className="muted">No model report</span> : null}
        </div>
      </div>

      <div className="detail-section">
        <div className="detail-section-heading">
          <h3>Usage And Load</h3>
          <button className="button secondary compact" type="button" onClick={onOpenUsage}>
            <Activity aria-hidden="true" size={14} />
            Open
          </button>
        </div>
        <div className="kv-list">
          <div className="kv-key">Queue depth</div><DetailValue changed={changed} changeKey="load:queue">{n(provider.limits?.queue_depth ?? 0)}</DetailValue>
          <div className="kv-key">Active streams</div><DetailValue changed={changed} changeKey="load:streams">{n(provider.limits?.active_streams ?? 0)}</DetailValue>
          <div className="kv-key">Max concurrency</div><DetailValue changed={changed} changeKey="load:maxConcurrency">{n(provider.limits?.max_concurrency ?? 0)}</DetailValue>
          <div className="kv-key">Requests</div><DetailValue changed={changed} changeKey="load:requests">{n(usage?.usage?.requests ?? 0)}</DetailValue>
          <div className="kv-key">Tokens</div><DetailValue changed={changed} changeKey="load:tokens">{n(usage?.usage?.total_tokens ?? 0)}</DetailValue>
          <div className="kv-key">Usage age</div><DetailValue changed={changed} changeKey="load:usageAge">{age(usage?.updated_at || usage?.usage?.observed_at)}</DetailValue>
        </div>
      </div>

      <div className="detail-section">
        <h3>Capabilities</h3>
        <div className={cx("tag-list", changed.has("capabilities:list") && "provider-value-changed")}>
          {(provider.capabilities ?? []).map((capability) => (
            <span className="tag mono" key={capability}>{capability}</span>
          ))}
        </div>
      </div>
    </div>
  );
}

function DetailValue({ children, changed, changeKey, className }: { children: ReactNode; changed: Set<string>; changeKey: string; className?: string }) {
  return (
    <div className={cx("kv-value", className, changed.has(changeKey) && "provider-value-changed")}>
      {children}
    </div>
  );
}

function SubscriptionValue({ provider, usage }: { provider: ProviderRegistration; usage?: ProviderUsageSnapshot }) {
  const subscription = providerSubscription(provider, usage);
  if (!subscription) {
    return <span className="muted">Not reported</span>;
  }
  const display = subscription.name || humanSubscriptionTier(subscription.tier) || subscription.paidTier || subscription.rateLimitTier || "Reported";
  return (
    <div className="subscription-value">
      <strong>{display}</strong>
      {subscription.tier && subscription.tier !== display ? <span className="tag mono">{subscription.tier}</span> : null}
      {subscription.paidTier ? <span className="tag mono">paid {subscription.paidTier}</span> : null}
      {subscription.rateLimitTier ? <span className="tag mono">rate {subscription.rateLimitTier}</span> : null}
      {subscription.status ? <span className="subscription-status">{subscription.status}</span> : null}
      {subscription.source ? <span className="muted mono">{subscription.source}</span> : null}
    </div>
  );
}

function providerSubscription(provider: ProviderRegistration, usage?: ProviderUsageSnapshot) {
  const native = asRecord(usage?.usage?.native_summary);
  const nativeNotes = asStringArray(native?.notes);
  const tier =
    trimOrUndefined(usage?.usage?.subscription?.tier) ||
    trimOrUndefined(usage?.usage?.plan_tier) ||
    trimOrUndefined(provider.auth?.subscription?.tier) ||
    trimOrUndefined(native?.plan_tier);
  const name =
    trimOrUndefined(usage?.usage?.subscription?.name) ||
    trimOrUndefined(provider.auth?.subscription?.name) ||
    noteValue(nativeNotes, "tier");
  const paidTier =
    trimOrUndefined(usage?.usage?.subscription?.paid_tier) ||
    trimOrUndefined(provider.auth?.subscription?.paid_tier) ||
    noteValue(nativeNotes, "paid-tier");
  const rateLimitTier =
    trimOrUndefined(usage?.usage?.subscription?.rate_limit_tier) ||
    trimOrUndefined(provider.auth?.subscription?.rate_limit_tier) ||
    noteValue(nativeNotes, "rate-limit-tier");
  const status = trimOrUndefined(usage?.usage?.subscription?.status) || trimOrUndefined(provider.auth?.subscription?.status) || noteValue(nativeNotes, "status");
  const source = trimOrUndefined(usage?.usage?.subscription?.source) || trimOrUndefined(provider.auth?.subscription?.source);
  if (!tier && !name && !paidTier && !rateLimitTier && !status) {
    return null;
  }
  return { tier, name, paidTier, rateLimitTier, status, source };
}

function noteValue(notes: string[], key: string) {
  const prefix = `${key.toLowerCase()}:`;
  for (const note of notes) {
    const trimmed = note.trim();
    if (trimmed.toLowerCase().startsWith(prefix)) {
      return trimOrUndefined(trimmed.slice(prefix.length));
    }
  }
  return undefined;
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  return value as Record<string, unknown>;
}

function asStringArray(value: unknown) {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string");
}

function trimOrUndefined(value: unknown) {
  if (typeof value !== "string") return undefined;
  const trimmed = value.trim();
  return trimmed || undefined;
}

function humanSubscriptionTier(value?: string) {
  const raw = value?.trim();
  if (!raw) return "";
  const normalized = raw.toLowerCase().replace(/[_-]+/g, " ");
  const known: Record<string, string> = {
    enterprise: "Enterprise",
    team: "Team",
    pro: "Pro",
    plus: "Plus",
    max: "Max",
    "gpt pro": "GPT Pro",
    "ai pro": "AI Pro",
    "standard tier": "Standard",
  };
  if (known[normalized]) return known[normalized];
  return normalized.replace(/\b\w/g, (char) => char.toUpperCase());
}

function ProviderEndpointTable({ provider, onOpenChat }: { provider: ProviderRegistration; onOpenChat: (endpoint: ServiceEndpoint) => void }) {
  const endpoints = providerServiceEndpoints(provider);

  return (
    <div className="detail-section">
      <h3>Service Endpoints</h3>
      <div className="endpoint-table-frame">
        <table className="endpoint-action-table">
          <thead>
            <tr>
              <th>API</th>
              <th>Model</th>
              <th>Chat</th>
            </tr>
          </thead>
          <tbody>
            {endpoints.map((endpoint) => (
              <tr key={endpoint.id}>
                <td>
                  <ServiceBadge endpoint={endpoint} />
                </td>
                <td>
                  <span className="mono">{endpoint.model || "no model"}</span>
                  <span className="endpoint-path mono">{endpoint.chatPath}</span>
                </td>
                <td>
                  <button className="icon-button small" type="button" title={`${endpoint.label} chat`} disabled={!endpoint.supportsChat || !endpoint.model} onClick={() => onOpenChat(endpoint)}>
                    <MessageSquare aria-hidden="true" size={15} />
                  </button>
                </td>
              </tr>
            ))}
            {!endpoints.length ? (
              <tr>
                <td colSpan={3}>
                  <span className="muted">No service endpoints reported</span>
                </td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>
    </div>
  );
}

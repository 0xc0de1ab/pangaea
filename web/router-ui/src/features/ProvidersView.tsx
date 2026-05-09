import { useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { Activity, Box, Clipboard, KeyRound, ListChecks, MessageSquare, PauseCircle, PlayCircle, RefreshCw } from "lucide-react";
import type { DashboardViewProps } from "../app/dashboard";
import { DataTable, type DashboardColumn } from "../components/DataTable";
import { Drawer } from "../components/Drawer";
import { Section } from "../components/Section";
import { ServiceBadge, ServiceIcon } from "../components/ServiceIcon";
import { StatusBadge } from "../components/StatusBadge";
import { ChatWorkbench, type ChatWorkbenchTarget } from "./ChatWorkbench";
import { EndpointDataWorkbench, type EndpointDataWorkbenchTarget } from "./EndpointDataWorkbench";
import { api } from "../lib/api";
import { providerAccountLabel, providerID, providerUsageMap, sessionSet, serviceHostAccount } from "../lib/derive";
import { age, copyText, fmtTime, hasText, middleEllipsis, n } from "../lib/format";
import { providerServiceEndpoints, serviceLabel, type ServiceEndpoint } from "../lib/service-endpoints";
import type { ProviderIdentity, ProviderRegistration, ProviderUsageSnapshot } from "../lib/types";
import dockerIcon from "../assets/icons/docker-mark-ocean-blue.svg";
import kubernetesIcon from "../assets/icons/kubernetes.svg";

export function ProvidersView({ data, queries, search, token, onAction, refresh }: DashboardViewProps) {
  const [selected, setSelected] = useState<ProviderRegistration | null>(null);
  const [chatTarget, setChatTarget] = useState<ChatWorkbenchTarget | null>(null);
  const [dataTarget, setDataTarget] = useState<EndpointDataWorkbenchTarget | null>(null);
  const rows = useMemo(() => {
    return [...data.providers]
      .sort((a, b) => `${a.identity.service}:${a.identity.host_name}:${providerAccountLabel(a)}:${providerID(a)}`.localeCompare(`${b.identity.service}:${b.identity.host_name}:${providerAccountLabel(b)}:${providerID(b)}`))
      .filter((provider) => hasText(provider, search));
  }, [data.providers, search]);
  const control = sessionSet(data.controlSessions);
  const dataSession = sessionSet(data.dataSessions);
  const usage = providerUsageMap(data.usage);

  function providerAction(provider: ProviderRegistration, kind: "drain" | "resume" | "refresh") {
    const id = providerID(provider);
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
      id: "provider",
      header: "Provider Instance",
      sortValue: (row) => providerID(row),
      cell: (row) => (
        <span className="id-cell">
          <span className="mono">{middleEllipsis(providerID(row), 12, 9)}</span>
          <button className="mini-icon" type="button" aria-label="Copy provider id" onClick={(event) => { event.stopPropagation(); copyText(providerID(row)); }}>
            <Clipboard aria-hidden="true" size={13} />
          </button>
        </span>
      ),
      width: "210px",
    },
    { id: "service", header: "Svc", sortValue: (row) => row.identity.service, cell: (row) => <ServiceLogoCell service={row.identity.service} />, align: "center", width: "56px" },
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
    { id: "health", header: "Health", sortValue: (row) => row.health?.status, cell: (row) => <StatusBadge value={row.health?.status} title={row.health?.reason} />, width: "128px" },
    { id: "auth", header: "Auth", sortValue: (row) => row.auth?.status, cell: (row) => <StatusBadge value={row.auth?.status} title={row.auth?.last_refresh_error} />, width: "132px" },
    {
      id: "sessions",
      header: "Sessions",
      sortValue: (row) => Number(control.has(providerID(row))) + Number(dataSession.has(providerID(row))),
      cell: (row) => (
        <div className="session-pair">
          <StatusBadge value={control.has(providerID(row)) ? "control" : "no control"} tone={control.has(providerID(row)) ? "ok" : "warn"} />
          <StatusBadge value={dataSession.has(providerID(row)) ? "data" : "no data"} tone={dataSession.has(providerID(row)) ? "ok" : "danger"} />
        </div>
      ),
      width: "210px",
    },
    { id: "models", header: "Models", sortValue: (row) => row.models?.length ?? 0, cell: (row) => n(row.models?.length ?? 0), align: "right", width: "80px" },
    { id: "queue", header: "Queue", sortValue: (row) => row.limits?.queue_depth ?? 0, cell: (row) => n(row.limits?.queue_depth ?? 0), align: "right", width: "76px" },
    { id: "streams", header: "Streams", sortValue: (row) => row.limits?.active_streams ?? 0, cell: (row) => n(row.limits?.active_streams ?? 0), align: "right", width: "92px" },
    {
      id: "usage",
      header: "Requests",
      sortValue: (row) => usage.get(providerID(row))?.usage?.requests ?? 0,
      cell: (row) => n(usage.get(providerID(row))?.usage?.requests ?? 0),
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
          <button className="button secondary" type="button" onClick={refresh}>
            <RefreshCw aria-hidden="true" size={15} />
            Refresh
          </button>
        }
      >
        <DataTable rows={rows} columns={columns} empty="No providers registered" getRowId={(row) => providerID(row)} onRowClick={setSelected} compact />
      </Section>

      <Drawer
        open={!!selected}
        onClose={() => setSelected(null)}
        title={selected ? middleEllipsis(providerID(selected), 18, 12) : "Provider"}
        subtitle={selected ? serviceHostAccount(selected) : undefined}
      >
        {selected ? (
          <ProviderDetail
            provider={selected}
            controlConnected={control.has(providerID(selected))}
            dataConnected={dataSession.has(providerID(selected))}
            usage={usage.get(providerID(selected))}
            onDrain={() => providerAction(selected, "drain")}
            onResume={() => providerAction(selected, "resume")}
            onRefreshAuth={() => providerAction(selected, "refresh")}
            onOpenChat={(endpoint) => setChatTarget({ provider: selected, endpoint })}
            onOpenModels={() => setDataTarget({ kind: "models", provider: selected })}
            onOpenUsage={() => setDataTarget({ kind: "usage", provider: selected, usage: usage.get(providerID(selected)) })}
          />
        ) : null}
      </Drawer>
      <ChatWorkbench target={chatTarget} token={token} onClose={() => setChatTarget(null)} />
      <EndpointDataWorkbench target={dataTarget} token={token} onClose={() => setDataTarget(null)} />
    </div>
  );
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
  onDrain: () => void;
  onResume: () => void;
  onRefreshAuth: () => void;
  onOpenChat: (endpoint: ServiceEndpoint) => void;
  onOpenModels: () => void;
  onOpenUsage: () => void;
};

function ProviderDetail({ provider, controlConnected, dataConnected, usage, onDrain, onResume, onRefreshAuth, onOpenChat, onOpenModels, onOpenUsage }: ProviderDetailProps) {
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
          <div className="kv-key">Provider ID</div><div className="kv-value mono">{provider.identity.provider_id}</div>
          <div className="kv-key">Instance</div><div className="kv-value mono">{provider.identity.provider_instance_id}</div>
          <div className="kv-key">Node</div><div className="kv-value mono">{provider.identity.node_id}</div>
          <div className="kv-key">Host</div><div className="kv-value"><HostCell identity={provider.identity} /></div>
          <div className="kv-key">Account</div><div className="kv-value">{providerAccountLabel(provider)}</div>
          <div className="kv-key">Container</div><div className="kv-value mono">{containerSummary(provider.identity)}</div>
          <div className="kv-key">Registered</div><div className="kv-value">{fmtTime(provider.registered_at)}</div>
        </div>
      </div>

      <div className="detail-section">
        <h3>State</h3>
        <div className="badge-row">
          <StatusBadge value={provider.health?.status} title={provider.health?.reason} />
          <StatusBadge value={provider.auth?.status} title={provider.auth?.last_refresh_error} />
          <StatusBadge value={controlConnected ? "control connected" : "control missing"} tone={controlConnected ? "ok" : "warn"} />
          <StatusBadge value={dataConnected ? "data connected" : "data missing"} tone={dataConnected ? "ok" : "danger"} />
        </div>
        <div className="kv-list">
          <div className="kv-key">Health check</div><div className="kv-value">{age(provider.health?.checked_at)}</div>
          <div className="kv-key">Auth expires</div><div className="kv-value">{fmtTime(provider.auth?.expires_at)}</div>
          <div className="kv-key">Last refresh</div><div className="kv-value">{fmtTime(provider.auth?.last_refresh_at)}</div>
          <div className="kv-key">Refreshable</div><div className="kv-value">{provider.auth?.refreshable ? "yes" : "no"}</div>
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
        <div className="tag-list">
          {(provider.models ?? []).map((model) => (
            <span className={model.kind === "group" || model.group_members?.length ? "tag mono model-tag-group" : "tag mono"} key={model.id}>
              {model.kind === "group" || model.group_members?.length ? <span className="model-group-badge mini" title="Group model">G</span> : null}
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
          <div className="kv-key">Queue depth</div><div className="kv-value">{n(provider.limits?.queue_depth ?? 0)}</div>
          <div className="kv-key">Active streams</div><div className="kv-value">{n(provider.limits?.active_streams ?? 0)}</div>
          <div className="kv-key">Max concurrency</div><div className="kv-value">{n(provider.limits?.max_concurrency ?? 0)}</div>
          <div className="kv-key">Requests</div><div className="kv-value">{n(usage?.usage?.requests ?? 0)}</div>
          <div className="kv-key">Tokens</div><div className="kv-value">{n(usage?.usage?.total_tokens ?? 0)}</div>
          <div className="kv-key">Usage age</div><div className="kv-value">{age(usage?.updated_at || usage?.usage?.observed_at)}</div>
        </div>
      </div>

      <div className="detail-section">
        <h3>Capabilities</h3>
        <div className="tag-list">
          {(provider.capabilities ?? []).map((capability) => (
            <span className="tag mono" key={capability}>{capability}</span>
          ))}
        </div>
      </div>
    </div>
  );
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

import { useMemo, useState } from "react";
import { NavLink, Navigate, Route, Routes, useNavigate } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  Boxes,
  Command,
  KeyRound,
  Loader2,
  Network,
  RefreshCw,
  RouteIcon,
  Search,
  Shield,
  Signal,
} from "lucide-react";
import { ActionModal, type ConfirmAction } from "../components/ActionModal";
import { StatusBadge } from "../components/StatusBadge";
import { api } from "../lib/api";
import type { DashboardData } from "../lib/types";
import { age, cx } from "../lib/format";
import type { DashboardQueries } from "./dashboard";
import { Overview } from "../features/Overview";
import { RoutesView } from "../features/RoutesView";
import { ProvidersView } from "../features/ProvidersView";
import { RequestsView } from "../features/RequestsView";
import { AdminView } from "../features/AdminView";

const navItems = [
  { to: "/", label: "Overview", icon: Activity },
  { to: "/routes", label: "Routes", icon: RouteIcon },
  { to: "/providers", label: "Providers", icon: Boxes },
  { to: "/requests", label: "Requests", icon: Signal },
  { to: "/admin", label: "Admin", icon: Shield },
];

function useDashboardQueries(token: string | undefined, authVersion: number): DashboardQueries {
  const authedKey = authVersion;
  const common = {
    refetchInterval: 15_000,
    refetchIntervalInBackground: false,
  };

  return {
    health: useQuery({
      queryKey: ["health"],
      queryFn: api.health,
      refetchInterval: 10_000,
    }),
    providers: useQuery({
      queryKey: ["providers", authedKey],
      queryFn: () => api.providers(token),
      ...common,
    }),
    nodes: useQuery({
      queryKey: ["nodes", authedKey],
      queryFn: () => api.nodes(token),
      ...common,
    }),
    containers: useQuery({
      queryKey: ["containers", authedKey],
      queryFn: () => api.containers(token),
      ...common,
    }),
    usage: useQuery({
      queryKey: ["usage", authedKey],
      queryFn: () => api.usage(token),
      ...common,
    }),
    controlSessions: useQuery({
      queryKey: ["control-sessions", authedKey],
      queryFn: () => api.controlSessions(token),
      ...common,
    }),
    dataSessions: useQuery({
      queryKey: ["data-sessions", authedKey],
      queryFn: () => api.dataSessions(token),
      ...common,
    }),
    traces: useQuery({
      queryKey: ["traces", authedKey],
      queryFn: () => api.traces(token, 100),
      ...common,
    }),
    audit: useQuery({
      queryKey: ["audit", authedKey],
      queryFn: () => api.audit(token, 40),
      ...common,
    }),
    quotas: useQuery({
      queryKey: ["quotas", authedKey],
      queryFn: () => api.quotas(token),
      ...common,
    }),
    apiKeys: useQuery({
      queryKey: ["api-keys", authedKey],
      queryFn: () => api.apiKeys(token),
      ...common,
    }),
    models: useQuery({
      queryKey: ["models", authedKey],
      queryFn: () => api.models(token),
      retry: false,
      refetchInterval: 60_000,
    }),
  };
}

function dataFromQueries(queries: DashboardQueries): DashboardData {
  return {
    healthText: queries.health.data,
    providers: queries.providers.data ?? [],
    nodes: queries.nodes.data ?? [],
    containers: queries.containers.data ?? [],
    usage: queries.usage.data ?? [],
    controlSessions: queries.controlSessions.data ?? [],
    dataSessions: queries.dataSessions.data ?? [],
    traces: queries.traces.data ?? [],
    audit: queries.audit.data ?? [],
    quotas: queries.quotas.data ?? [],
    apiKeys: queries.apiKeys.data ?? [],
    models: queries.models.data ?? [],
  };
}

function queryAge(queries: DashboardQueries) {
  const timestamps = Object.values(queries)
    .map((query) => query.dataUpdatedAt)
    .filter((value) => value > 0);
  if (timestamps.length === 0) {
    return "never";
  }
  return age(Math.min(...timestamps));
}

function queryErrorCount(queries: DashboardQueries) {
  return Object.values(queries).filter((query) => query.isError).length;
}

export default function App() {
  const [tokenDraft, setTokenDraft] = useState("");
  const [adminToken, setAdminToken] = useState("");
  const [authVersion, setAuthVersion] = useState(0);
  const [search, setSearch] = useState("");
  const [commandOpen, setCommandOpen] = useState(false);
  const [action, setAction] = useState<ConfirmAction | null>(null);
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const queries = useDashboardQueries(adminToken || undefined, authVersion);
  const data = useMemo(() => dataFromQueries(queries), [queries]);
  const errorCount = queryErrorCount(queries);
  const isFetching = Object.values(queries).some((query) => query.isFetching);

  function applyToken() {
    setAdminToken(tokenDraft.trim());
    setAuthVersion((value) => value + 1);
    queryClient.clear();
  }

  function clearToken() {
    setTokenDraft("");
    setAdminToken("");
    setAuthVersion((value) => value + 1);
    queryClient.clear();
  }

  function refresh() {
    void queryClient.invalidateQueries();
  }

  const viewProps = {
    data,
    queries,
    search,
    token: adminToken || undefined,
    onAction: setAction,
    refresh,
  };

  return (
    <div className="app-shell">
      <aside className="left-rail" aria-label="Primary">
        <div className="brand">
          <div className="brand-mark">
            <Network aria-hidden="true" size={18} />
          </div>
          <div className="brand-text">
            <strong>Pangaea</strong>
            <span>Router Console</span>
          </div>
        </div>
        <nav className="rail-nav">
          {navItems.map((item) => {
            const Icon = item.icon;
            return (
              <NavLink key={item.to} to={item.to} end={item.to === "/"}>
                <Icon aria-hidden="true" size={17} />
                <span>{item.label}</span>
              </NavLink>
            );
          })}
        </nav>
      </aside>

      <div className="workspace">
        <header className="topbar">
          <div className="topbar-left">
            <span className="env-badge">v2</span>
            <StatusBadge value={queries.health.isError ? "healthz failed" : data.healthText || "checking"} tone={queries.health.isError ? "danger" : data.healthText ? "ok" : "unknown"} />
            <span className="topbar-meta">role {adminToken ? "bearer" : "dev/anonymous"}</span>
          </div>
          <label className="global-search">
            <Search aria-hidden="true" size={16} />
            <input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search provider, host, model, key prefix, request id"
              autoComplete="off"
            />
          </label>
          <div className="topbar-actions">
            <label className="token-field">
              <KeyRound aria-hidden="true" size={15} />
              <input
                value={tokenDraft}
                onChange={(event) => setTokenDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    applyToken();
                  }
                }}
                type="password"
                placeholder="Admin bearer"
                autoComplete="off"
                spellCheck={false}
              />
            </label>
            <button className="button secondary" type="button" onClick={applyToken}>
              Use
            </button>
            <button className="button ghost" type="button" onClick={clearToken}>
              Clear
            </button>
            <div className="command-wrap">
              <button className="icon-button" type="button" aria-label="Open command menu" onClick={() => setCommandOpen((value) => !value)}>
                <Command aria-hidden="true" size={17} />
              </button>
              {commandOpen ? (
                <div className="command-menu">
                  <button type="button" onClick={() => { navigate("/providers"); setCommandOpen(false); }}>
                    Open provider actions
                  </button>
                  <button type="button" onClick={() => { navigate("/routes"); setCommandOpen(false); }}>
                    Dry run route
                  </button>
                  <button type="button" onClick={() => { navigate("/requests"); setCommandOpen(false); }}>
                    Find request trace
                  </button>
                  <button type="button" onClick={() => { navigate("/admin"); setCommandOpen(false); }}>
                    Open audit and keys
                  </button>
                </div>
              ) : null}
            </div>
            <button className={cx("icon-button", isFetching && "is-active")} type="button" aria-label="Refresh" onClick={refresh}>
              {isFetching ? <Loader2 aria-hidden="true" className="spin" size={17} /> : <RefreshCw aria-hidden="true" size={17} />}
            </button>
            <div className="freshness" title={errorCount ? `${errorCount} sections have errors` : "All loaded sections are healthy"}>
              <span>age {queryAge(queries)}</span>
              {errorCount ? <b>{errorCount} err</b> : <b>ok</b>}
            </div>
          </div>
        </header>

        <main className="view-area">
          <Routes>
            <Route path="/" element={<Overview {...viewProps} />} />
            <Route path="/routes" element={<RoutesView {...viewProps} />} />
            <Route path="/providers" element={<ProvidersView {...viewProps} />} />
            <Route path="/requests" element={<RequestsView {...viewProps} />} />
            <Route path="/admin" element={<AdminView {...viewProps} />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </div>

      <ActionModal action={action} onClose={() => setAction(null)} />
    </div>
  );
}

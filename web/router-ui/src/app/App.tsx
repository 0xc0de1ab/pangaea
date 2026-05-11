import { useMemo, useState } from "react";
import { NavLink, Navigate, Route, Routes, useNavigate } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  Boxes,
  Command,
  KeyRound,
  LogIn,
  LogOut,
  Loader2,
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
import { AuthView } from "../features/AuthView";
import pangaeaIcon from "../assets/images/pangaea.128x128.png";

const navItems = [
  { to: "/", label: "Overview", icon: Activity },
  { to: "/routes", label: "Routes", icon: RouteIcon },
  { to: "/providers", label: "Providers", icon: Boxes },
  { to: "/auth", label: "Auth", icon: KeyRound },
  { to: "/requests", label: "Requests", icon: Signal },
  { to: "/admin", label: "Admin", icon: Shield },
];

function useDashboardQueries(token: string | undefined, authVersion: number, enabled: boolean): DashboardQueries {
  const authedKey = authVersion;
  const common = {
    enabled,
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
    auth: useQuery({
      queryKey: ["auth", authedKey],
      queryFn: () => api.auth(token),
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
    notifiers: useQuery({
      queryKey: ["notifiers", authedKey],
      queryFn: () => api.notifiers(token),
      ...common,
    }),
    notificationHistory: useQuery({
      queryKey: ["notification-history", authedKey],
      queryFn: () => api.notificationHistory(token, 80),
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
      enabled,
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
    auth: queries.auth.data ?? [],
    controlSessions: queries.controlSessions.data ?? [],
    dataSessions: queries.dataSessions.data ?? [],
    traces: queries.traces.data ?? [],
    audit: queries.audit.data ?? [],
    notifiers: queries.notifiers.data ?? [],
    notificationHistory: queries.notificationHistory.data ?? [],
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
  const session = useQuery({
    queryKey: ["router-session", authVersion],
    queryFn: api.session,
    refetchInterval: 60_000,
    retry: false,
  });
  const oauthEnabled = session.data?.google_oauth?.enabled ?? false;
  const oauthUser = session.data?.authenticated ? session.data.user : undefined;
  const sessionKnown = session.data !== undefined || session.isError;
  const oauthLoginRequired = sessionKnown && oauthEnabled && !oauthUser;

  const adminQueriesEnabled = !oauthLoginRequired && (Boolean(adminToken) || (sessionKnown && (!oauthEnabled || Boolean(oauthUser))));
  const queries = useDashboardQueries(adminToken || undefined, authVersion, adminQueriesEnabled);
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

  function startGoogleLogin() {
    const next = `${window.location.pathname}${window.location.search}${window.location.hash}`;
    window.location.assign(`/router/v1/auth/google/login?next=${encodeURIComponent(next)}`);
  }

  async function logoutGoogle() {
    await api.logout();
    setAuthVersion((value) => value + 1);
    queryClient.clear();
  }

  const viewProps = {
    data,
    queries,
    search,
    token: adminToken || undefined,
    onAction: setAction,
    refresh,
  };

  if (!sessionKnown) {
    return <DashboardAuthGate state="loading" />;
  }
  if (session.isError && session.data === undefined) {
    return <DashboardAuthGate state="error" message={session.error instanceof Error ? session.error.message : "Unable to check router session"} />;
  }
  if (oauthLoginRequired) {
    return <DashboardAuthGate state="login" onLogin={startGoogleLogin} />;
  }

  return (
    <div className="app-shell">
      <aside className="left-rail" aria-label="Primary">
        <div className="brand">
          <div className="brand-mark">
            <img src={pangaeaIcon} alt="" aria-hidden="true" />
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
            <span className="topbar-meta">role {adminToken ? "bearer" : oauthUser?.email ? "google" : oauthEnabled ? "signed-out" : "dev/anonymous"}</span>
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
            {oauthEnabled ? (
              oauthUser?.email ? (
                <div className="oauth-user" title={oauthUser.name || oauthUser.email}>
                  {oauthUser.picture ? <img src={oauthUser.picture} alt="" referrerPolicy="no-referrer" /> : null}
                  <span>{oauthUser.email}</span>
                  <button className="icon-button small" type="button" aria-label="Logout Google session" onClick={() => void logoutGoogle()}>
                    <LogOut aria-hidden="true" size={15} />
                  </button>
                </div>
              ) : (
                <button className="button secondary" type="button" onClick={startGoogleLogin}>
                  <LogIn aria-hidden="true" size={15} />
                  Google
                </button>
              )
            ) : null}
            <div className="command-wrap">
              <button className="icon-button" type="button" aria-label="Open command menu" onClick={() => setCommandOpen((value) => !value)}>
                <Command aria-hidden="true" size={17} />
              </button>
              {commandOpen ? (
                <div className="command-menu">
                  <button type="button" onClick={() => { navigate("/providers"); setCommandOpen(false); }}>
                    Open provider actions
                  </button>
                  <button type="button" onClick={() => { navigate("/auth"); setCommandOpen(false); }}>
                    Open auth inventory
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
            <Route path="/auth" element={<AuthView {...viewProps} />} />
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

function DashboardAuthGate({ state, message, onLogin }: { state: "loading" | "login" | "error"; message?: string; onLogin?: () => void }) {
  return (
    <main className="auth-gate" aria-live="polite">
      <section className="auth-gate-panel">
        <div className="auth-gate-brand">
          <div className="brand-mark">
            <img src={pangaeaIcon} alt="" aria-hidden="true" />
          </div>
          <div className="brand-text">
            <strong>Pangaea</strong>
            <span>Router Console</span>
          </div>
        </div>
        {state === "loading" ? (
          <div className="auth-gate-status">
            <Loader2 aria-hidden="true" className="spin" size={18} />
            <span>Checking session</span>
          </div>
        ) : state === "error" ? (
          <>
            <h1>Session unavailable</h1>
            <p>{message || "The router session endpoint did not respond."}</p>
          </>
        ) : (
          <>
            <h1>Sign in required</h1>
            <p>Dashboard data is only shown after an allowed Google account is authenticated.</p>
            <button className="button primary" type="button" onClick={onLogin}>
              <LogIn aria-hidden="true" size={16} />
              Continue with Google
            </button>
          </>
        )}
      </section>
    </main>
  );
}

import type {
  AuditEvent,
  ContainerSnapshot,
  DashboardData,
  Incident,
  ProviderRegistration,
  ProviderUsageSnapshot,
  QuotaSnapshot,
  RequestTrace,
  SessionSnapshot,
} from "./types";
import { accountLabel, pct } from "./format";

export function providerID(provider: ProviderRegistration) {
  return provider.identity.provider_instance_id;
}

export function providerAccount(provider: ProviderRegistration) {
  return provider.identity.account?.display || provider.identity.account?.id ? provider.identity.account : provider.auth?.account;
}

export function providerAccountLabel(provider: ProviderRegistration) {
  return accountLabel(providerAccount(provider));
}

export function serviceHostAccount(provider: ProviderRegistration) {
  const identity = provider.identity;
  return [identity.service, identity.host_name, providerAccountLabel(provider)].filter(Boolean).join(" / ");
}

export function sessionSet(sessions: SessionSnapshot[]) {
  return new Set(sessions.map((session) => session.provider_instance_id));
}

export function quotaPressure(snapshot: QuotaSnapshot) {
  const committedTokens = snapshot.committed?.tokens ?? 0;
  const reservedTokens = snapshot.reserved?.tokens ?? 0;
  const tokenLimit = snapshot.limit?.max_tokens ?? 0;
  const committedRequests = snapshot.committed?.requests ?? 0;
  const reservedRequests = snapshot.reserved?.requests ?? 0;
  const requestLimit = snapshot.limit?.max_requests ?? 0;
  const tokenPressure = tokenLimit > 0 ? (committedTokens + reservedTokens) / tokenLimit : 0;
  const requestPressure = requestLimit > 0 ? (committedRequests + reservedRequests) / requestLimit : 0;
  return Math.max(tokenPressure, requestPressure);
}

export function quotaPressureLabel(snapshot: QuotaSnapshot) {
  return pct(Math.round(quotaPressure(snapshot) * 100), 100);
}

export function deriveIncidents(data: DashboardData): Incident[] {
  const incidents: Incident[] = [];
  const control = sessionSet(data.controlSessions);
  const dataSessions = sessionSet(data.dataSessions);

  for (const provider of data.providers) {
    const identity = provider.identity;
    const id = providerID(provider);
    const scope = serviceHostAccount(provider) || id;
    const health = provider.health?.status || "unknown";
    const auth = provider.auth?.status || "unknown";
    if (!["ready", "unknown"].includes(health)) {
      incidents.push({
        id: `provider-health-${id}`,
        severity: health === "down" ? "critical" : "warning",
        scope,
        title: `Provider ${health}`,
        detail: provider.health?.reason || id,
        at: provider.health?.checked_at,
        providerInstanceID: id,
      });
    }
    if (["expired", "revoked", "conflict", "unavailable"].includes(auth)) {
      incidents.push({
        id: `provider-auth-${id}`,
        severity: "critical",
        scope,
        title: `Auth ${auth}`,
        detail: provider.auth?.last_refresh_error || "Provider auth is not routable.",
        at: provider.auth?.last_refresh_at || provider.auth?.expires_at,
        providerInstanceID: id,
      });
    } else if (auth === "refresh_soon") {
      incidents.push({
        id: `provider-auth-soon-${id}`,
        severity: "warning",
        scope,
        title: "Auth refresh soon",
        detail: provider.auth?.expires_at ? `Expires at ${provider.auth.expires_at}` : "Provider credentials are nearing expiry.",
        at: provider.auth?.expires_at,
        providerInstanceID: id,
      });
    }
    if (!control.has(id)) {
      incidents.push({
        id: `control-missing-${id}`,
        severity: "warning",
        scope,
        title: "Control session disconnected",
        detail: "Drain, refresh, and runtime actions will not execute until control reconnects.",
        providerInstanceID: id,
      });
    }
    if (!dataSessions.has(id)) {
      incidents.push({
        id: `data-missing-${id}`,
        severity: health === "ready" ? "critical" : "warning",
        scope,
        title: "Data session disconnected",
        detail: "Routable provider has no data websocket session.",
        providerInstanceID: id,
      });
    }
  }

  for (const trace of data.traces.slice(0, 20)) {
    if (trace.status && trace.status !== "completed") {
      incidents.push({
        id: `trace-${trace.request_id}`,
        severity: trace.status === "rejected" ? "warning" : "critical",
        scope: [trace.route_request?.model, trace.provider?.host_name].filter(Boolean).join(" / ") || trace.request_id,
        title: `Request ${trace.status}`,
        detail: trace.error || trace.decision?.reason || trace.error_code || "Request did not complete.",
        at: trace.completed_at || trace.started_at,
        traceID: trace.request_id,
      });
    }
  }

  for (const quota of data.quotas) {
    const pressure = quotaPressure(quota);
    if (pressure >= 0.8) {
      incidents.push({
        id: `quota-${JSON.stringify(quota.scope)}`,
        severity: pressure >= 1 ? "critical" : "warning",
        scope: [quota.scope.tenant_id, quota.scope.user_id, quota.scope.api_key_id, quota.scope.model].filter(Boolean).join(" / "),
        title: `Quota pressure ${quotaPressureLabel(quota)}`,
        detail: "Committed plus reserved usage is close to the configured limit.",
      });
    }
  }

  return incidents.slice(0, 60);
}

export function capacityRows(providers: ProviderRegistration[]) {
  const groups = new Map<
    string,
    {
      key: string;
      service: string;
      model: string;
      providers: number;
      ready: number;
      degraded: number;
      down: number;
      activeStreams: number;
      queueDepth: number;
      hosts: Set<string>;
    }
  >();
  for (const provider of providers) {
    const models = provider.models?.length ? provider.models : [{ id: "(no model report)" }];
    for (const model of models) {
      const key = `${provider.identity.service}:${model.id}`;
      const row =
        groups.get(key) ??
        {
          key,
          service: provider.identity.service,
          model: model.id,
          providers: 0,
          ready: 0,
          degraded: 0,
          down: 0,
          activeStreams: 0,
          queueDepth: 0,
          hosts: new Set<string>(),
        };
      row.providers += 1;
      row.hosts.add(provider.identity.host_name);
      const status = provider.health?.status || "unknown";
      if (status === "ready") row.ready += 1;
      if (status === "degraded" || status === "draining" || status === "auth-updating") row.degraded += 1;
      if (status === "down") row.down += 1;
      row.activeStreams += provider.limits?.active_streams ?? 0;
      row.queueDepth += provider.limits?.queue_depth ?? 0;
      groups.set(key, row);
    }
  }
  return Array.from(groups.values()).sort((a, b) => `${a.service}:${a.model}`.localeCompare(`${b.service}:${b.model}`));
}

export function providerUsageMap(usage: ProviderUsageSnapshot[]) {
  return new Map(usage.map((snapshot) => [snapshot.provider_instance_id, snapshot]));
}

export function failedTraceRate(traces: RequestTrace[]) {
  if (traces.length === 0) {
    return "0%";
  }
  const failed = traces.filter((trace) => trace.status !== "completed").length;
  return pct(failed, traces.length);
}

export function auditTarget(event: AuditEvent) {
  return (
    event.target?.provider_instance_id ||
    event.target?.api_key_id ||
    event.target?.model ||
    event.target?.user_id ||
    event.target?.tenant_id ||
    ""
  );
}

export function containerProvider(container: ContainerSnapshot) {
  return container.provider_instance_id || container.provider_id || "";
}

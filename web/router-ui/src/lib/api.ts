import type {
  APIKeyCreateResponse,
  APIKeyPrincipal,
  AuditEvent,
  ContainerSnapshot,
  NodeSnapshot,
  ProviderRegistration,
  ProviderUsageSnapshot,
  PublicModel,
  QuotaLimit,
  QuotaScope,
  QuotaSnapshot,
  RequestTrace,
  RouteDecision,
  RouteRequest,
  SessionSnapshot,
} from "./types";

type RequestOptions = {
  token?: string;
  method?: string;
  body?: unknown;
  okStatuses?: number[];
};

export class APIError extends Error {
  status: number;
  endpoint: string;

  constructor(message: string, status: number, endpoint: string) {
    super(message);
    this.name = status === 401 ? "UnauthorizedError" : "APIError";
    this.status = status;
    this.endpoint = endpoint;
  }
}

const jsonHeaders = {
  Accept: "application/json",
  "Content-Type": "application/json",
};

async function request<T>(endpoint: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { ...jsonHeaders };
  if (options.token) {
    headers.Authorization = `Bearer ${options.token}`;
  }
  const response = await fetch(endpoint, {
    method: options.method ?? "GET",
    cache: "no-store",
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
  const okStatuses = options.okStatuses ?? [200, 201, 202, 204];
  if (!okStatuses.includes(response.status)) {
    let detail = response.statusText || `HTTP ${response.status}`;
    try {
      const payload = (await response.json()) as { error?: string };
      if (payload.error) {
        detail = payload.error;
      }
    } catch {
      detail = response.statusText || detail;
    }
    throw new APIError(detail, response.status, endpoint);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

async function requestText(endpoint: string): Promise<string> {
  const response = await fetch(endpoint, { cache: "no-store" });
  if (!response.ok) {
    throw new APIError(response.statusText || `HTTP ${response.status}`, response.status, endpoint);
  }
  return response.text();
}

export const api = {
  health: () => requestText("/healthz"),
  providers: async (token?: string) => {
    const payload = await request<{ providers?: ProviderRegistration[] }>("/router/v1/providers", { token });
    return payload.providers ?? [];
  },
  nodes: async (token?: string) => {
    const payload = await request<{ nodes?: NodeSnapshot[] }>("/router/v1/nodes", { token });
    return payload.nodes ?? [];
  },
  containers: async (token?: string) => {
    const payload = await request<{ containers?: ContainerSnapshot[] }>("/router/v1/containers", { token });
    return payload.containers ?? [];
  },
  usage: async (token?: string) => {
    const payload = await request<{ usage?: ProviderUsageSnapshot[] }>("/router/v1/usage/providers", { token });
    return payload.usage ?? [];
  },
  controlSessions: async (token?: string) => {
    const payload = await request<{ sessions?: SessionSnapshot[] }>("/router/v1/control/sessions", { token });
    return payload.sessions ?? [];
  },
  dataSessions: async (token?: string) => {
    const payload = await request<{ sessions?: SessionSnapshot[] }>("/router/v1/data/sessions", { token });
    return payload.sessions ?? [];
  },
  audit: async (token?: string, limit = 40) => {
    const payload = await request<{ events?: AuditEvent[] }>(`/router/v1/audit/events?limit=${limit}`, { token });
    return payload.events ?? [];
  },
  traces: async (token?: string, limit = 100) => {
    const payload = await request<{ traces?: RequestTrace[] }>(`/router/v1/traces?limit=${limit}`, { token });
    return payload.traces ?? [];
  },
  trace: (requestID: string, token?: string) => request<RequestTrace>(`/router/v1/traces/${encodeURIComponent(requestID)}`, { token }),
  quotas: async (token?: string) => {
    const payload = await request<{ quotas?: QuotaSnapshot[] }>("/router/v1/quotas", { token });
    return payload.quotas ?? [];
  },
  apiKeys: async (token?: string) => {
    const payload = await request<{ api_keys?: APIKeyPrincipal[] }>("/router/v1/api-keys", { token });
    return payload.api_keys ?? [];
  },
  models: async (token?: string) => {
    const payload = await request<{ data?: Array<{ id: string }>; models?: Array<{ name: string; displayName?: string; version?: string }> }>("/v1/models", { token });
    const openAI = (payload.data ?? []).map((model) => ({ id: model.id, display: model.id, protocol: "openai" }));
    const gemini = (payload.models ?? []).map((model) => ({
      id: model.name.replace(/^models\//, ""),
      display: model.displayName || model.name,
      protocol: "gemini",
      canonical_model: model.version,
    }));
    return [...openAI, ...gemini] satisfies PublicModel[];
  },
  dryRun: (route: RouteRequest, token?: string) =>
    request<RouteDecision>("/router/v1/routes/dry-run", {
      token,
      method: "POST",
      body: route,
      okStatuses: [200, 409],
    }),
  providerDrain: (providerInstanceID: string, drain: boolean, reason: string, token?: string) =>
    request(`/router/v1/providers/${encodeURIComponent(providerInstanceID)}/drain`, {
      token,
      method: "POST",
      body: {
        drain,
        reason,
        confirm: true,
        timeout_seconds: 5,
      },
      okStatuses: [202],
    }),
  providerAuthRefresh: (providerInstanceID: string, reason: string, token?: string) =>
    request(`/router/v1/providers/${encodeURIComponent(providerInstanceID)}/auth/refresh`, {
      token,
      method: "POST",
      body: {
        reason,
        confirm: true,
        timeout_seconds: 30,
      },
    }),
  setQuotaLimit: (scope: QuotaScope, limit: QuotaLimit, token?: string) =>
    request<QuotaSnapshot>("/router/v1/quotas/limits", {
      token,
      method: "PUT",
      body: { scope, limit },
    }),
  createAPIKey: (payload: { tenant_id?: string; user_id?: string; disabled?: boolean; expires_at?: string }, token?: string) =>
    request<APIKeyCreateResponse>("/router/v1/api-keys", {
      token,
      method: "POST",
      body: payload,
      okStatuses: [201],
    }),
  deleteAPIKey: (id: string, token?: string) =>
    request<void>(`/router/v1/api-keys/${encodeURIComponent(id)}`, {
      token,
      method: "DELETE",
      okStatuses: [204],
    }),
};

import type {
  APIKeyCreateResponse,
  APIKeyPrincipal,
  AuthEvent,
  AuthRecord,
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
  RequestTracePage,
  RouteDecision,
  RouteRequest,
  SessionSnapshot,
} from "./types";

type RequestOptions = {
  token?: string;
  method?: string;
  body?: unknown;
  okStatuses?: number[];
  headers?: Record<string, string>;
};

export type DashboardChatMessage = {
  role: "user" | "assistant";
  content: DashboardChatContent;
};

export type DashboardChatContent = string | DashboardChatContentPart[];

export type DashboardChatContentPart =
  | { type: "text"; text: string }
  | { type: "image_url"; image_url: { url: string } };

export type DashboardChatProtocol = "openai" | "anthropic" | "gemini";

export type DashboardChatResult = {
  content: string;
  raw?: unknown;
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
  const headers = requestHeaders(options);
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

async function requestWithFetchFallback<T>(endpoint: string, fallbackEndpoint: string, options: RequestOptions = {}): Promise<T> {
  try {
    return await request<T>(endpoint, options);
  } catch (err) {
    if (!isFetchFailure(err)) {
      throw err;
    }
    return request<T>(fallbackEndpoint, options);
  }
}

function isFetchFailure(err: unknown) {
  return err instanceof TypeError && /fetch/i.test(err.message);
}

async function requestText(endpoint: string): Promise<string> {
  const response = await fetch(endpoint, { cache: "no-store" });
  if (!response.ok) {
    throw new APIError(response.statusText || `HTTP ${response.status}`, response.status, endpoint);
  }
  return response.text();
}

async function requestBlob(endpoint: string, options: RequestOptions = {}): Promise<Blob> {
  const response = await fetch(endpoint, {
    method: options.method ?? "GET",
    cache: "no-store",
    headers: requestHeaders(options),
  });
  const okStatuses = options.okStatuses ?? [200];
  if (!okStatuses.includes(response.status)) {
    let detail = response.statusText || `HTTP ${response.status}`;
    try {
      const payload = (await response.json()) as { error?: string };
      detail = payload.error || detail;
    } catch {
      detail = response.statusText || detail;
    }
    throw new APIError(detail, response.status, endpoint);
  }
  return response.blob();
}

function requestHeaders(options: RequestOptions = {}) {
  const headers: Record<string, string> = { ...jsonHeaders, ...(options.headers ?? {}) };
  if (options.token) {
    headers.Authorization = `Bearer ${options.token}`;
  }
  return headers;
}

async function requestStream(endpoint: string, options: RequestOptions, onPayload: (payload: unknown) => void): Promise<void> {
  const response = await fetch(endpoint, {
    method: options.method ?? "POST",
    cache: "no-store",
    headers: requestHeaders(options),
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
  if (!response.ok) {
    let detail = response.statusText || `HTTP ${response.status}`;
    try {
      const payload = (await response.json()) as { error?: string | { message?: string } };
      if (typeof payload.error === "string") {
        detail = payload.error;
      } else if (payload.error?.message) {
        detail = payload.error.message;
      }
    } catch {
      detail = response.statusText || detail;
    }
    throw new APIError(detail, response.status, endpoint);
  }
  if (!response.body) {
    throw new APIError("stream response body is empty", response.status, endpoint);
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    buffer = consumeSSEBuffer(buffer, endpoint, onPayload);
  }
  buffer += decoder.decode();
  consumeSSEBuffer(buffer, endpoint, onPayload, true);
}

function consumeSSEBuffer(buffer: string, endpoint: string, onPayload: (payload: unknown) => void, flush = false) {
  let cursor = 0;
  for (;;) {
    const match = /\r?\n\r?\n/g.exec(buffer.slice(cursor));
    if (!match) break;
    const frameEnd = cursor + match.index;
    emitSSEFrame(buffer.slice(cursor, frameEnd), endpoint, onPayload);
    cursor = frameEnd + match[0].length;
  }
  if (flush && cursor < buffer.length) {
    emitSSEFrame(buffer.slice(cursor), endpoint, onPayload);
    return "";
  }
  return buffer.slice(cursor);
}

function emitSSEFrame(frame: string, endpoint: string, onPayload: (payload: unknown) => void) {
  const data = frame
    .split(/\r?\n/)
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice(5).trimStart())
    .join("\n")
    .trim();
  if (!data || data === "[DONE]") {
    return;
  }
  try {
    const payload = JSON.parse(data) as unknown;
    const error = streamPayloadError(payload);
    if (error) {
      throw new APIError(error, 200, endpoint);
    }
    onPayload(payload);
  } catch (err) {
    if (err instanceof APIError) {
      throw err;
    }
    if (data.includes('"error"')) {
      throw new APIError(data, 200, endpoint);
    }
    onPayload(data);
  }
}

function streamPayloadError(payload: unknown) {
  if (!payload || typeof payload !== "object") {
    return "";
  }
  const error = (payload as { error?: unknown }).error;
  if (!error) {
    return "";
  }
  if (typeof error === "string") {
    return error;
  }
  if (typeof error === "object") {
    const message = (error as { message?: unknown; error?: unknown; detail?: unknown }).message
      ?? (error as { message?: unknown; error?: unknown; detail?: unknown }).error
      ?? (error as { message?: unknown; error?: unknown; detail?: unknown }).detail;
    return typeof message === "string" ? message : JSON.stringify(error);
  }
  return String(error);
}

async function bufferedChat(protocol: DashboardChatProtocol, model: string, messages: DashboardChatMessage[], token?: string, reasoningEffort?: string): Promise<DashboardChatResult> {
  switch (protocol) {
    case "openai": {
      const response = await request<{ choices?: Array<{ message?: { content?: string } }> }>("/v1/chat/completions", {
        token,
        method: "POST",
        body: compactBody({ model, messages: openAIMessages(messages), max_tokens: 2048, stream: false, reasoning_effort: reasoningEffort }),
      });
      return { content: response.choices?.[0]?.message?.content ?? "", raw: response };
    }
    case "anthropic": {
      const response = await request<{ content?: Array<{ type?: string; text?: string }> }>("/v1/messages", {
        token,
        method: "POST",
        headers: { "anthropic-version": "2023-06-01" },
        body: compactBody({ model, max_tokens: 2048, messages: anthropicMessages(messages), stream: false, reasoning_effort: reasoningEffort }),
      });
      return { content: extractAnthropicText(response), raw: response };
    }
    case "gemini": {
      const modelPath = encodeURIComponent(model);
      const response = await requestWithFetchFallback<{ candidates?: Array<{ content?: { parts?: Array<{ text?: string }> } }> }>(`/v1beta/models/${modelPath}:generateContent`, `/router/v1/compat/v1beta/models/${modelPath}:generateContent`, {
        token,
        method: "POST",
        body: compactBody({ contents: geminiContents(messages), reasoning_effort: reasoningEffort, generationConfig: reasoningEffort ? { reasoningEffort: reasoningEffort } : undefined }),
      });
      return { content: extractGeminiText(response), raw: response };
    }
  }
}

async function streamingChat(protocol: DashboardChatProtocol, model: string, messages: DashboardChatMessage[], token: string | undefined, onDelta: (delta: string) => void, reasoningEffort?: string): Promise<DashboardChatResult> {
  let content = "";
  const append = (delta: string) => {
    if (!delta) return;
    content += delta;
    onDelta(delta);
  };
  switch (protocol) {
    case "openai":
      await requestStream("/v1/chat/completions", {
        token,
        method: "POST",
        body: compactBody({ model, messages: openAIMessages(messages), max_tokens: 2048, stream: true, reasoning_effort: reasoningEffort }),
      }, (payload) => append(extractOpenAIStreamDelta(payload)));
      return { content };
    case "anthropic":
      await requestStream("/v1/messages", {
        token,
        method: "POST",
        headers: { "anthropic-version": "2023-06-01" },
        body: compactBody({ model, max_tokens: 2048, messages: anthropicMessages(messages), stream: true, reasoning_effort: reasoningEffort }),
      }, (payload) => append(extractAnthropicStreamDelta(payload)));
      return { content };
    case "gemini":
      await requestGeminiStreamWithFallback(model, {
        token,
        method: "POST",
        body: compactBody({ contents: geminiContents(messages), reasoning_effort: reasoningEffort, generationConfig: reasoningEffort ? { reasoningEffort: reasoningEffort } : undefined }),
      }, (payload) => append(extractGeminiText(payload)));
      return { content };
  }
}

async function requestGeminiStreamWithFallback(model: string, options: RequestOptions, onPayload: (payload: unknown) => void) {
  const modelPath = encodeURIComponent(model);
  try {
    await requestStream(`/v1beta/models/${modelPath}:streamGenerateContent?alt=sse`, options, onPayload);
  } catch (err) {
    if (!isFetchFailure(err)) {
      throw err;
    }
    await requestStream(`/router/v1/compat/v1beta/models/${modelPath}:streamGenerateContent?alt=sse`, options, onPayload);
  }
}

function extractAnthropicText(payload: { content?: Array<{ type?: string; text?: string }> }) {
  return (payload.content ?? []).map((part) => part.text ?? "").join("");
}

function extractGeminiText(payload: unknown) {
  const response = payload as { candidates?: Array<{ content?: { parts?: Array<{ text?: string }> } }> };
  return (response.candidates ?? []).flatMap((candidate) => candidate.content?.parts ?? []).map((part) => part.text ?? "").join("");
}

function extractOpenAIStreamDelta(payload: unknown) {
  const response = payload as { choices?: Array<{ delta?: { content?: string } }> };
  return response.choices?.[0]?.delta?.content ?? "";
}

function extractAnthropicStreamDelta(payload: unknown) {
  const response = payload as { type?: string; delta?: { type?: string; text?: string } };
  return response.type === "content_block_delta" ? response.delta?.text ?? "" : "";
}

function openAIMessages(messages: DashboardChatMessage[]) {
  return messages.map((message) => ({ ...message, content: normalizeChatContent(message.content) }));
}

function anthropicMessages(messages: DashboardChatMessage[]) {
  return messages.map((message) => ({
    role: message.role,
    content: normalizeChatContent(message.content).map((part) => {
      if (part.type === "text") {
        return part;
      }
      const source = dataURLSource(part.image_url.url);
      return {
        type: "image",
        source: source ?? { type: "url", url: part.image_url.url },
      };
    }),
  }));
}

function geminiContents(messages: DashboardChatMessage[]) {
  return messages.map((message) => ({
    role: message.role === "assistant" ? "model" : "user",
    parts: normalizeChatContent(message.content).map((part) => {
      if (part.type === "text") {
        return { text: part.text };
      }
      const source = dataURLSource(part.image_url.url);
      if (!source || source.type !== "base64") {
        return { text: `[Image: ${part.image_url.url}]` };
      }
      return { inlineData: { mimeType: source.media_type, data: source.data } };
    }),
  }));
}

function normalizeChatContent(content: DashboardChatContent): DashboardChatContentPart[] {
  if (typeof content === "string") {
    return content ? [{ type: "text", text: content }] : [];
  }
  return content;
}

function dataURLSource(url: string) {
  const match = /^data:([^;,]+);base64,(.*)$/i.exec(url);
  if (!match) return null;
  return { type: "base64", media_type: match[1], data: match[2] };
}

function compactBody<T extends Record<string, unknown>>(body: T) {
  return Object.fromEntries(Object.entries(body).filter(([, value]) => value !== undefined && value !== "")) as Partial<T>;
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
  auth: async (token?: string) => {
    const payload = await request<{ auth?: AuthRecord[] }>("/router/v1/auth", { token });
    return payload.auth ?? [];
  },
  authEvents: async (authID: string, token?: string) => {
    const payload = await request<{ events?: AuthEvent[] }>(`/router/v1/auth/${encodeURIComponent(authID)}/events`, { token });
    return payload.events ?? [];
  },
  authDownload: (authID: string, token?: string) => requestBlob(`/router/v1/auth/${encodeURIComponent(authID)}/download`, { token }),
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
  tracePage: (token: string | undefined, limit: number, offset: number) =>
    request<RequestTracePage>(`/router/v1/traces?limit=${limit}&offset=${offset}`, { token }),
  trace: (requestID: string, token?: string) => request<RequestTrace>(`/router/v1/traces/${encodeURIComponent(requestID)}`, { token }),
  deleteTraces: (requestIDs: string[], token?: string) =>
    request<{ deleted: number }>("/router/v1/traces", {
      token,
      method: "DELETE",
      body: { request_ids: requestIDs },
    }),
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
  openAIModels: (token?: string) => request<{ object?: string; data?: Array<{ id: string; object?: string; created?: number; owned_by?: string }> }>("/v1/models", { token }),
  anthropicModels: (token?: string) =>
    request<{ data?: Array<{ id: string; type?: string; display_name?: string }>; first_id?: string; last_id?: string; has_more?: boolean }>("/v1/models", {
      token,
      headers: { "anthropic-version": "2023-06-01", "x-api-dialect": "anthropic" },
    }),
  geminiModels: (token?: string) =>
    request<{ models?: Array<{ name: string; displayName?: string; version?: string; supportedGenerationMethods?: string[] }> }>("/v1beta/models", { token }),
  openAIChat: (model: string, token?: string) =>
    request<unknown>("/v1/chat/completions", {
      token,
      method: "POST",
      body: {
        model,
        messages: [{ role: "user", content: "Reply with exactly OK." }],
        max_tokens: 8,
      },
    }),
  anthropicMessage: (model: string, token?: string) =>
    request<unknown>("/v1/messages", {
      token,
      method: "POST",
      headers: { "anthropic-version": "2023-06-01" },
      body: {
        model,
        max_tokens: 8,
        messages: [{ role: "user", content: "Reply with exactly OK." }],
      },
    }),
  geminiGenerateContent: (model: string, token?: string) => {
    const modelPath = encodeURIComponent(model);
    return requestWithFetchFallback<unknown>(`/v1beta/models/${modelPath}:generateContent`, `/router/v1/compat/v1beta/models/${modelPath}:generateContent`, {
      token,
      method: "POST",
      body: {
        contents: [{ role: "user", parts: [{ text: "Reply with exactly OK." }] }],
      },
    });
  },
  bufferedChat,
  streamingChat,
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

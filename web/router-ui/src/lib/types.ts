export type HealthStatus = "unknown" | "ready" | "degraded" | "draining" | "down" | string;
export type AuthStatus =
  | "unknown"
  | "healthy"
  | "refresh_soon"
  | "refreshing"
  | "expired"
  | "revoked"
  | "conflict"
  | "unavailable"
  | "no_login"
  | string;

export type Account = {
  id?: string;
  display?: string;
};

export type ProviderIdentity = {
  provider_id: string;
  provider_instance_id: string;
  node_id: string;
  host_name: string;
  container_id?: string;
  container_kind?: string;
  container_name?: string;
  service: string;
  kind: string;
  account?: Account;
};

export type ProviderModel = {
  id: string;
  aliases?: string[];
  capabilities?: string[];
  context_tokens?: number;
  max_context_tokens?: number;
  kind?: string;
  group_members?: string[];
  quota?: {
    remaining_pct?: number;
    reset_at?: string;
    source?: string;
  };
};

export type ProviderRegistration = {
  identity: ProviderIdentity;
  capabilities?: string[];
  models?: ProviderModel[];
  health?: {
    status?: HealthStatus;
    reason?: string;
    checked_at?: string;
  };
  auth?: {
    status?: AuthStatus;
    account?: Account;
    expires_at?: string;
    refreshable?: boolean;
    last_refresh_at?: string;
    last_refresh_error?: string;
    selected_source?: string;
    replica_count?: number;
    bootstrap_source?: string;
  };
  limits?: {
    max_concurrency?: number;
    queue_depth?: number;
    active_streams?: number;
  };
  registered_at?: string;
};

export type NodeSnapshot = {
  node_id: string;
  host_name?: string;
  agent_version?: string;
  os?: string;
  arch?: string;
  runtime?: Record<string, unknown>;
  capabilities?: string[];
  health?: {
    status?: string;
    reason?: string;
    observed_at?: string;
  };
  resources?: Record<string, unknown>;
  last_hello_at?: string;
  last_heartbeat_at?: string;
  last_inventory_at?: string;
  updated_at?: string;
};

export type ContainerSnapshot = {
  node_id?: string;
  host_name?: string;
  container_id: string;
  container_kind?: string;
  container_name?: string;
  provider_id?: string;
  provider_instance_id?: string;
  image?: string;
  state?: string;
  health?: {
    status?: string;
    reason?: string;
  };
  resources?: Record<string, unknown>;
  labels?: Record<string, string>;
  started_at?: string;
  reported_at?: string;
  updated_at?: string;
};

export type SessionSnapshot = {
  provider_instance_id: string;
  provider_id?: string;
  node_id?: string;
  host_name?: string;
  service?: string;
  account?: Account;
  connected_at: string;
  pending_requests?: number;
};

export type ProviderUsageSnapshot = {
  provider_instance_id: string;
  provider_id: string;
  node_id: string;
  host_name: string;
  container_id?: string;
  container_kind?: string;
  container_name?: string;
  service: string;
  kind: string;
  account?: Account;
  usage?: {
    observed_at?: string;
    source?: string;
    requests?: number;
    input_tokens?: number;
    output_tokens?: number;
    total_tokens?: number;
    native_summary?: unknown;
  };
  reported_at?: string;
  updated_at?: string;
};

export type AuthReplica = {
  provider_id?: string;
  provider_instance_id: string;
  node_id?: string;
  host_name?: string;
  service?: string;
  account?: Account;
  status?: AuthStatus;
  fingerprint?: string;
  source?: string;
  observed_at?: string;
  updated_at?: string;
  has_download?: boolean;
};

export type AuthRecord = {
  id: string;
  service: string;
  account?: Account;
  status?: AuthStatus;
  expires_at?: string;
  refreshable?: boolean;
  last_refresh_at?: string;
  last_refresh_error?: string;
  selected_source?: string;
  bootstrap_source?: string;
  fingerprint?: string;
  source?: string;
  filename: string;
  format?: string;
  latest_provider_id?: string;
  provider_instance_id?: string;
  node_id?: string;
  host_name?: string;
  observed_at?: string;
  reported_at?: string;
  updated_at?: string;
  has_download?: boolean;
  download_url?: string;
  replicas?: AuthReplica[];
};

export type AuthEvent = {
  id: string;
  auth_id: string;
  type: string;
  service?: string;
  account?: Account;
  provider_id?: string;
  provider_instance_id?: string;
  node_id?: string;
  host_name?: string;
  status?: AuthStatus;
  fingerprint?: string;
  source?: string;
  message?: string;
  at?: string;
};

export type QuotaScope = {
  tenant_id?: string;
  user_id?: string;
  api_key_id?: string;
  model?: string;
};

export type QuotaUsage = {
  tokens?: number;
  requests?: number;
};

export type QuotaLimit = {
  max_tokens?: number;
  max_requests?: number;
};

export type QuotaSnapshot = {
  scope: QuotaScope;
  limit?: QuotaLimit;
  committed?: QuotaUsage;
  reserved?: QuotaUsage;
};

export type RouteRequest = {
  tenant_id?: string;
  user_id?: string;
  api_key_id?: string;
  model: string;
  api_dialect: "openai" | "anthropic" | "gemini" | string;
  stream?: boolean;
  features?: string[];
};

export type RouteDecision = {
  allowed: boolean;
  route_id?: string;
  model_alias?: string;
  canonical_model?: string;
  selected?: string;
  selected_provider?: ProviderRegistration;
  required_capabilities?: string[];
  fallback_chain?: string[];
  scores?: Array<{
    provider_instance_id?: string;
    provider_id?: string;
    score: number;
    weight?: number;
    reason?: string;
  }>;
  rejections?: Array<{
    provider_instance_id?: string;
    provider_id?: string;
    reason: string;
  }>;
  reason?: string;
};

export type RequestTrace = {
  request_id: string;
  route_request?: RouteRequest;
  decision?: RouteDecision;
  reservation?: {
    request_id?: string;
    scope?: QuotaScope;
    estimate?: QuotaUsage;
    actual?: QuotaUsage;
    status?: string;
    created_at?: string;
    closed_at?: string;
  };
  provider?: ProviderIdentity;
  http?: RequestTraceHTTP;
  status: string;
  error?: string;
  error_code?: string;
  error_status?: number;
  retry_after?: string;
  estimated_usage?: QuotaUsage;
  actual_usage?: QuotaUsage;
  started_at?: string;
  completed_at?: string;
  duration_ms?: number;
};

export type RequestTraceHTTP = {
  request: {
    method: string;
    path: string;
    query?: string;
    headers?: Record<string, string[]>;
    body?: RequestTraceHTTPBody;
  };
  response: {
    status: number;
    headers?: Record<string, string[]>;
    body?: RequestTraceHTTPBody;
  };
};

export type RequestTraceHTTPBody = {
  content_type?: string;
  json?: unknown;
  jsonl?: unknown[];
  text?: string;
  truncated?: boolean;
};

export type RequestTracePage = {
  traces: RequestTrace[];
  total: number;
  limit: number;
  offset: number;
  has_more: boolean;
};

export type AuditEvent = {
  id: string;
  type: string;
  actor?: {
    tenant_id?: string;
    user_id?: string;
    api_key_id?: string;
    source?: string;
    remote_addr?: string;
    request_id?: string;
  };
  target?: {
    provider_instance_id?: string;
    provider_id?: string;
    node_id?: string;
    host_name?: string;
    container_id?: string;
    service?: string;
    api_key_id?: string;
    tenant_id?: string;
    user_id?: string;
    model?: string;
    request_id?: string;
  };
  reason?: string;
  outcome: string;
  error?: string;
  metadata?: Record<string, string>;
  created_at?: string;
};

export type APIKeyPrincipal = {
  id: string;
  prefix: string;
  tenant_id?: string;
  user_id?: string;
  created_at?: string;
  expires_at?: string;
  disabled?: boolean;
  last_used_at?: string;
};

export type APIKeyCreateResponse = {
  api_key: APIKeyPrincipal;
  raw_key?: string;
};

export type PublicModel = {
  id: string;
  display?: string;
  protocol?: string;
  canonical_model?: string;
};

export type DashboardData = {
  healthText?: string;
  providers: ProviderRegistration[];
  nodes: NodeSnapshot[];
  containers: ContainerSnapshot[];
  usage: ProviderUsageSnapshot[];
  auth: AuthRecord[];
  controlSessions: SessionSnapshot[];
  dataSessions: SessionSnapshot[];
  traces: RequestTrace[];
  audit: AuditEvent[];
  quotas: QuotaSnapshot[];
  apiKeys: APIKeyPrincipal[];
  models: PublicModel[];
};

export type Incident = {
  id: string;
  severity: "critical" | "warning" | "info";
  scope: string;
  title: string;
  detail: string;
  at?: string;
  providerInstanceID?: string;
  traceID?: string;
};

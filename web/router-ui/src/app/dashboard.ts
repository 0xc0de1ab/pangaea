import type { UseQueryResult } from "@tanstack/react-query";
import type { ConfirmAction } from "../components/ActionModal";
import type {
  APIKeyPrincipal,
  AuditEvent,
  ContainerSnapshot,
  DashboardData,
  NodeSnapshot,
  ProviderRegistration,
  ProviderUsageSnapshot,
  PublicModel,
  QuotaSnapshot,
  RequestTrace,
  SessionSnapshot,
} from "../lib/types";

export type DashboardQueries = {
  health: UseQueryResult<string, Error>;
  providers: UseQueryResult<ProviderRegistration[], Error>;
  nodes: UseQueryResult<NodeSnapshot[], Error>;
  containers: UseQueryResult<ContainerSnapshot[], Error>;
  usage: UseQueryResult<ProviderUsageSnapshot[], Error>;
  controlSessions: UseQueryResult<SessionSnapshot[], Error>;
  dataSessions: UseQueryResult<SessionSnapshot[], Error>;
  traces: UseQueryResult<RequestTrace[], Error>;
  audit: UseQueryResult<AuditEvent[], Error>;
  quotas: UseQueryResult<QuotaSnapshot[], Error>;
  apiKeys: UseQueryResult<APIKeyPrincipal[], Error>;
  models: UseQueryResult<PublicModel[], Error>;
};

export type DashboardViewProps = {
  data: DashboardData;
  queries: DashboardQueries;
  search: string;
  token?: string;
  onAction: (action: ConfirmAction) => void;
  refresh: () => void;
};

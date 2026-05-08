import chatgptIcon from "../assets/icons/chatgpt.svg";
import claudeIcon from "../assets/icons/claude.svg";
import geminiIcon from "../assets/icons/gemini.svg";
import { serviceLabel, type ServiceEndpoint } from "../lib/service-endpoints";
import { protocolLabel, type ProviderProtocol } from "../lib/protocols";

type ServiceIconProps = {
  service: string;
  size?: number;
  label?: string;
};

export function ServiceIcon({ service, size = 22, label }: ServiceIconProps) {
  const normalized = service.trim().toLowerCase();
  const icon = brandIcon(normalized);
  const displayLabel = label ?? icon?.label ?? serviceLabel(service);
  if (icon) {
    return (
      <span className={`service-icon service-${normalized}`} style={{ width: size, height: size }} role="img" aria-label={displayLabel} title={displayLabel}>
        <img src={icon.src} alt="" aria-hidden="true" />
      </span>
    );
  }
  return (
    <svg className={`service-svg service-${normalized}`} width={size} height={size} viewBox="0 0 24 24" role="img" aria-label={displayLabel}>
      <title>{displayLabel}</title>
      {normalized.includes("claude") ? <ClaudeMark /> : normalized.includes("gemini") ? <GeminiMark /> : normalized.includes("codex") ? <CodexMark /> : <ProviderMark />}
    </svg>
  );
}

export function ServiceBadge({ endpoint, compact = false }: { endpoint: ServiceEndpoint; compact?: boolean }) {
  return (
    <span className="service-badge" title={`${endpoint.protocolLabel} via ${endpoint.label}`}>
      <ServiceIcon service={endpoint.protocol} size={compact ? 19 : 22} label={endpoint.protocolLabel} />
      {compact ? null : (
        <span>
          <strong>{endpoint.protocolLabel}</strong>
          <em>{endpoint.label} backend</em>
        </span>
      )}
    </span>
  );
}

function brandIcon(value: string): { src: string; label: string } | undefined {
  switch (value as ProviderProtocol | string) {
    case "openai":
    case "chatgpt":
      return { src: chatgptIcon, label: protocolLabel("openai") };
    case "anthropic":
    case "claude":
      return { src: claudeIcon, label: protocolLabel("anthropic") };
    case "gemini":
      return { src: geminiIcon, label: protocolLabel("gemini") };
    default:
      return undefined;
  }
}

function CodexMark() {
  return (
    <>
      <rect x="2.5" y="2.5" width="19" height="19" rx="5" fill="#0f766e" />
      <path d="M15.8 7.2 9.1 10.9v2.2l6.7 3.7" fill="none" stroke="#f8fafc" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M8.4 7.1 5.6 12l2.8 4.9" fill="none" stroke="#ccfbf1" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
    </>
  );
}

function ClaudeMark() {
  return (
    <>
      <rect x="2.5" y="2.5" width="19" height="19" rx="5" fill="#191919" />
      <path d="M6.8 17.4 11.2 6.6h1.6l4.4 10.8h-2.1l-.9-2.4h-4.4l-.9 2.4H6.8Zm3.7-4.1h3l-1.5-4.1-1.5 4.1Z" fill="#f8fafc" />
    </>
  );
}

function GeminiMark() {
  return (
    <>
      <rect x="2.5" y="2.5" width="19" height="19" rx="5" fill="#2f63d8" />
      <path d="M12 5.2c.7 3.4 2.5 5.2 5.8 5.8-3.3.7-5.1 2.5-5.8 5.8-.7-3.3-2.5-5.1-5.8-5.8 3.3-.6 5.1-2.4 5.8-5.8Z" fill="#fff" />
      <path d="M17.3 15.4c.3 1.2 1 1.9 2.2 2.2-1.2.3-1.9 1-2.2 2.2-.3-1.2-1-1.9-2.2-2.2 1.2-.3 1.9-1 2.2-2.2Z" fill="#c9d7ff" />
    </>
  );
}

function ProviderMark() {
  return (
    <>
      <rect x="2.5" y="2.5" width="19" height="19" rx="5" fill="#475467" />
      <path d="M7 8h10M7 12h10M7 16h10" stroke="#f8fafc" strokeWidth="1.7" strokeLinecap="round" />
    </>
  );
}

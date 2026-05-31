import antigravityIcon from "../assets/icons/antigravity-color.svg";
import claudeIcon from "../assets/icons/claude.svg";
import codexIcon from "../assets/icons/codex-color.svg";
import cursorAiIcon from "../assets/icons/cursor-ai-code-icon.svg";
import geminiIcon from "../assets/icons/gemini.svg";
import githubCopilotIcon from "../assets/icons/githubcopilot.svg";
import grokIcon from "../assets/icons/grok-icon.svg";
import minimaxIcon from "../assets/icons/minimax-color.svg";
import openaiIcon from "../assets/icons/openai.svg";
import { normalizeService, serviceLabel, type ServiceEndpoint } from "../lib/service-endpoints";
import { protocolLabel, type ProviderProtocol } from "../lib/protocols";

type ServiceIconProps = {
  service: string;
  size?: number;
  label?: string;
};

type ProtocolIconProps = {
  protocol: ProviderProtocol;
  size?: number;
  label?: string;
};

export function ServiceIcon({ service, size = 22, label }: ServiceIconProps) {
  const normalized = normalizeService(service);
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
      {normalized.includes("claude") ? (
        <ClaudeMark />
      ) : normalized.includes("gemini") ? (
        <GeminiMark />
      ) : normalized.includes("codex") ? (
        <CodexMark />
      ) : normalized.includes("cursor") ? (
        <CursorMark />
      ) : (
        <ProviderMark />
      )}
    </svg>
  );
}

export function ProtocolIcon({ protocol, size = 22, label }: ProtocolIconProps) {
  const icon = protocolBrandIcon(protocol);
  const displayLabel = label ?? icon.label;
  return (
    <span className={`service-icon protocol-icon protocol-${protocol}`} style={{ width: size, height: size }} role="img" aria-label={displayLabel} title={displayLabel}>
      <img src={icon.src} alt="" aria-hidden="true" />
    </span>
  );
}

export function ServiceBadge({ endpoint, compact = false }: { endpoint: ServiceEndpoint; compact?: boolean }) {
  return (
    <span className="service-badge" title={`${endpoint.protocolLabel} API via ${endpoint.label}`}>
      <ProtocolIcon protocol={endpoint.protocol} size={compact ? 19 : 22} label={`${endpoint.protocolLabel} API via ${endpoint.label}`} />
      {compact ? null : (
        <span>
          <strong>{endpoint.protocolLabel}</strong>
          <em>{endpoint.label} provider</em>
        </span>
      )}
    </span>
  );
}

function brandIcon(value: string): { src: string; label: string } | undefined {
  switch (value as ProviderProtocol | string) {
    case "openai":
    case "chatgpt":
      return { src: openaiIcon, label: protocolLabel("openai") };
    case "codex":
    case "codex-cli":
    case "codex-appserver":
      return { src: codexIcon, label: serviceLabel("codex") };
    case "anthropic":
      return { src: claudeIcon, label: protocolLabel("anthropic") };
    case "claude":
      return { src: claudeIcon, label: serviceLabel("claude") };
    case "gemini":
      return { src: geminiIcon, label: protocolLabel("gemini") };
    case "antigravity":
    case "antigravity-sidecar":
      return { src: antigravityIcon, label: serviceLabel("antigravity") };
    case "cursor":
    case "cursor-cli":
      return { src: cursorAiIcon, label: serviceLabel("cursor") };
    case "grok":
    case "grok-build":
    case "grok-build-cli":
    case "xai":
    case "x-ai":
      return { src: grokIcon, label: serviceLabel("grok-build") };
    case "github-copilot":
    case "github-copilot-sidecar":
    case "github-copilot-acp":
    case "github-copilot-sdk":
    case "githubcopilot":
    case "copilot":
    case "copilot-sidecar":
    case "copilot-acp":
    case "copilot-sdk":
      return { src: githubCopilotIcon, label: serviceLabel("github-copilot") };
    case "minimax":
    case "minimax-api":
    case "minimax-api-provider":
      return { src: minimaxIcon, label: serviceLabel("minimax") };
    default:
      if (value.includes("minimax")) {
        return { src: minimaxIcon, label: serviceLabel("minimax") };
      }
      return undefined;
  }
}

function protocolBrandIcon(protocol: ProviderProtocol): { src: string; label: string } {
  switch (protocol) {
    case "openai":
      return { src: openaiIcon, label: protocolLabel("openai") };
    case "anthropic":
      return { src: claudeIcon, label: protocolLabel("anthropic") };
    case "gemini":
      return { src: geminiIcon, label: protocolLabel("gemini") };
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

function CursorMark() {
  return (
    <>
      <rect x="2.5" y="2.5" width="19" height="19" rx="5" fill="#0b0b0b" />
      <path d="M12 7.2 16.2 9.6 12 12l-4.2-2.4L12 7.2Zm4.2 5.3V14L12 16.4 7.8 14v-2.5L12 14l4.2-2.4Zm-8.4 0L12 9.6l4.2 2.4L12 14.4 7.8 12Z" fill="#e4e4e4" />
      <path d="m16.2 9.6-.45 4.35L12 16.4V12l4.2-2.4Z" fill="#fff" opacity=".85" />
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

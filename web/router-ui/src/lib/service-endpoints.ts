import type { ProviderModel, ProviderRegistration } from "./types";
import { preferredModelForProtocol, providerModelsForProtocol, providerProtocols, providerSupportsCapability, protocolLabel, type ProviderProtocol } from "./protocols";

export type ServiceEndpoint = {
  id: string;
  service: string;
  label: string;
  protocol: ProviderProtocol;
  protocolLabel: string;
  model: string;
  models: ProviderModel[];
  chatPath: string;
  streamPath: string;
  modelsPath: string;
  supportsChat: boolean;
  supportsStream: boolean;
};

export function providerServiceEndpoints(provider: ProviderRegistration): ServiceEndpoint[] {
  const service = normalizeService(provider.identity.service);
  return providerProtocols(provider).map((protocol) => {
    const model = preferredModelForProtocol(provider, protocol);
    const models = providerModelsForProtocol(provider, protocol);
    const supportsChat = chatCapability(provider, protocol);
    const supportsStream = providerSupportsCapability(provider, "stream.sse") || models.some((candidate) => (candidate.capabilities ?? []).includes("stream.sse"));
    return {
      id: `${service}:${protocol}`,
      service,
      label: serviceLabel(service),
      protocol,
      protocolLabel: protocolLabel(protocol),
      model,
      models,
      chatPath: chatPath(protocol, model, false),
      streamPath: chatPath(protocol, model, true),
      modelsPath: protocol === "gemini" ? "/v1beta/models" : "/v1/models",
      supportsChat,
      supportsStream,
    };
  });
}

export function normalizeService(service: string) {
  return service.trim().toLowerCase().replace(/[_\s]+/g, "-");
}

export function serviceLabel(service: string) {
  switch (normalizeService(service)) {
    case "codex":
    case "codex-cli":
      return "Codex";
    case "claude":
    case "claude-cli":
      return "Claude";
    case "gemini":
    case "gemini-cli":
      return "Gemini";
    case "cursor":
    case "cursor-cli":
      return "Cursor";
    case "grok":
    case "grok-build":
    case "grok-build-cli":
    case "xai":
    case "x-ai":
      return "Grok Build";
    case "github-copilot":
    case "github-copilot-sidecar":
    case "github-copilot-acp":
    case "github-copilot-sdk":
    case "githubcopilot":
    case "copilot":
    case "copilot-sidecar":
    case "copilot-acp":
    case "copilot-sdk":
      return "GitHub Copilot";
    case "minimax":
    case "minimax-api":
    case "minimax-api-provider":
      return "MiniMax";
    case "antigravity":
      return "Antigravity";
    default:
      return service
        .split(/[-_\s]+/)
        .filter(Boolean)
        .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
        .join(" ") || "Provider";
  }
}

export function chatPath(protocol: ProviderProtocol, model: string, stream: boolean) {
  switch (protocol) {
    case "openai":
      return "/router/v1/compat/v1/chat/completions";
    case "anthropic":
      return "/router/v1/compat/v1/messages";
    case "gemini":
      return `/router/v1/compat/v1beta/models/${model}:${stream ? "streamGenerateContent" : "generateContent"}`;
  }
}

function chatCapability(provider: ProviderRegistration, protocol: ProviderProtocol) {
  switch (protocol) {
    case "openai":
      return providerSupportsCapability(provider, "api.openai.chat");
    case "anthropic":
      return providerSupportsCapability(provider, "api.anthropic.messages");
    case "gemini":
      return providerSupportsCapability(provider, "api.gemini.generateContent");
  }
}

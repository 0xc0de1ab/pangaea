import type { ProviderModel, ProviderRegistration } from "./types";

export type ProviderProtocol = "openai" | "anthropic" | "gemini";

const protocolOrder: ProviderProtocol[] = ["openai", "anthropic", "gemini"];

const protocolCapabilities: Record<ProviderProtocol, string[]> = {
  openai: ["api.openai.chat", "api.openai.responses"],
  anthropic: ["api.anthropic.messages"],
  gemini: ["api.gemini.generateContent"],
};

export function protocolLabel(protocol: ProviderProtocol) {
  switch (protocol) {
    case "openai":
      return "ChatGPT";
    case "anthropic":
      return "Claude";
    case "gemini":
      return "Gemini";
  }
}

export function providerProtocols(provider: ProviderRegistration): ProviderProtocol[] {
  const capabilities = providerCapabilitySet(provider);
  return protocolOrder.filter((protocol) => protocolCapabilities[protocol].some((capability) => capabilities.has(capability)));
}

export function providerSupportsCapability(provider: ProviderRegistration, capability: string) {
  return providerCapabilitySet(provider).has(capability);
}

export function modelSupportsProtocol(model: ProviderModel, protocol: ProviderProtocol) {
  const capabilities = new Set(model.capabilities ?? []);
  return protocolCapabilities[protocol].some((capability) => capabilities.has(capability));
}

export function providerModelsForProtocol(provider: ProviderRegistration, protocol: ProviderProtocol) {
  const models = provider.models ?? [];
  const matched = models.filter((model) => modelSupportsProtocol(model, protocol));
  return matched.length ? matched : models;
}

export function preferredModelForProtocol(provider: ProviderRegistration, protocol: ProviderProtocol) {
  const model = providerModelsForProtocol(provider, protocol)[0];
  return model?.aliases?.[0] || model?.id || "";
}

function providerCapabilitySet(provider: ProviderRegistration) {
  const capabilities = new Set(provider.capabilities ?? []);
  for (const model of provider.models ?? []) {
    for (const capability of model.capabilities ?? []) {
      capabilities.add(capability);
    }
  }
  return capabilities;
}

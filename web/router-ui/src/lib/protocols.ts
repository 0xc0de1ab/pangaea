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
      return "OpenAI";
    case "anthropic":
      return "Anthropic";
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
  const model = preferredModelsForService(provider.identity.service, providerModelsForProtocol(provider, protocol))[0];
  return model?.id || model?.aliases?.[0] || "";
}

function preferredModelsForService(service: string, models: ProviderModel[]) {
  const key = service.trim().toLowerCase().replace(/[_\s]+/g, "-");
  if (key !== "gemini" && !key.startsWith("gemini-")) {
    return models;
  }
  const hasConcrete = models.some((model) => model.kind !== "group");
  if (!hasConcrete) {
    return models;
  }
  return [...models].sort((a, b) => {
    const groupRank = Number(a.kind === "group") - Number(b.kind === "group");
    if (groupRank !== 0) {
      return groupRank;
    }
    const familyRank = geminiModelFamilyRank(a) - geminiModelFamilyRank(b);
    if (familyRank !== 0) {
      return familyRank;
    }
    const quotaRank = quotaRemaining(b) - quotaRemaining(a);
    if (quotaRank !== 0) {
      return quotaRank;
    }
    return models.indexOf(a) - models.indexOf(b);
  });
}

function quotaRemaining(model: ProviderModel) {
  return model.quota?.remaining_pct ?? -1;
}

function geminiModelFamilyRank(model: ProviderModel) {
  const id = model.id.toLowerCase();
  if (id.includes("flash-lite")) {
    return 0;
  }
  if (id.includes("flash")) {
    return 1;
  }
  if (id.includes("pro")) {
    return 3;
  }
  return 2;
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

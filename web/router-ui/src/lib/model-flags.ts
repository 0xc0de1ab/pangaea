import type { ProviderModel } from "./types";

export function isGroupProviderModel(service: string | undefined, model: Pick<ProviderModel, "id" | "kind" | "group_members">) {
  return model.kind === "group" || Boolean(model.group_members?.length) || isProviderAutoGroupModel(service, model.id);
}

export function isAliasProviderModel(model: Pick<ProviderModel, "id" | "kind">) {
  return model.kind === "alias" || model.id === "antigravity-default";
}

function isProviderAutoGroupModel(service: string | undefined, modelID: string | undefined) {
  if ((modelID ?? "").trim().toLowerCase() !== "auto") {
    return false;
  }
  const key = (service ?? "").trim().toLowerCase().replace(/[_\s]+/g, "-");
  return [
    "github-copilot",
    "github-copilot-sidecar",
    "github-copilot-acp",
    "github-copilot-sdk",
    "githubcopilot",
    "copilot",
    "copilot-sidecar",
    "copilot-acp",
    "copilot-sdk",
  ].includes(key);
}

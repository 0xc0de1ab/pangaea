import type { ProviderModel } from "./types";

export function isGroupProviderModel(model: Pick<ProviderModel, "kind" | "group_members">) {
  return model.kind === "group" || Boolean(model.group_members?.length);
}

export function isAliasProviderModel(model: Pick<ProviderModel, "kind">) {
  return model.kind === "alias";
}

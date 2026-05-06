import type { Account, QuotaScope } from "./types";

export function cx(...values: Array<string | false | null | undefined>) {
  return values.filter(Boolean).join(" ");
}

export function n(value: number | null | undefined) {
  if (value === null || value === undefined || Number.isNaN(value)) {
    return "0";
  }
  return new Intl.NumberFormat().format(value);
}

export function compactNumber(value: number | null | undefined) {
  if (value === null || value === undefined || Number.isNaN(value)) {
    return "0";
  }
  return new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(value);
}

export function pct(numerator: number, denominator: number) {
  if (!denominator) {
    return "0%";
  }
  return `${Math.round((numerator / denominator) * 100)}%`;
}

export function fmtTime(value?: string) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return new Intl.DateTimeFormat(undefined, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(date);
}

export function age(value?: string | number) {
  if (!value) {
    return "never";
  }
  const millis = typeof value === "number" ? value : new Date(value).getTime();
  if (!millis || Number.isNaN(millis)) {
    return "never";
  }
  const delta = Math.max(0, Date.now() - millis);
  if (delta < 5_000) {
    return "now";
  }
  if (delta < 60_000) {
    return `${Math.floor(delta / 1000)}s`;
  }
  if (delta < 3_600_000) {
    return `${Math.floor(delta / 60_000)}m`;
  }
  return `${Math.floor(delta / 3_600_000)}h`;
}

export function middleEllipsis(value?: string, left = 9, right = 7) {
  if (!value) {
    return "";
  }
  if (value.length <= left + right + 1) {
    return value;
  }
  return `${value.slice(0, left)}...${value.slice(-right)}`;
}

export function accountLabel(account?: Account) {
  return account?.display || account?.id || "";
}

export function scopeLabel(scope?: QuotaScope) {
  if (!scope) {
    return "";
  }
  const parts = [
    scope.tenant_id && `tenant:${scope.tenant_id}`,
    scope.user_id && `user:${scope.user_id}`,
    scope.api_key_id && `key:${scope.api_key_id}`,
    scope.model && `model:${scope.model}`,
  ].filter(Boolean);
  return parts.join(" / ");
}

export function hasText(row: unknown, query: string) {
  if (!query.trim()) {
    return true;
  }
  return JSON.stringify(row).toLowerCase().includes(query.trim().toLowerCase());
}

export function copyText(value: string) {
  if (!value || !navigator.clipboard) {
    return;
  }
  void navigator.clipboard.writeText(value);
}

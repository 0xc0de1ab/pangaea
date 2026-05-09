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
  const sign = value < 0 ? "-" : "";
  const absolute = Math.abs(value);
  const units = [
    { suffix: "T", value: 1_000_000_000_000 },
    { suffix: "B", value: 1_000_000_000 },
    { suffix: "M", value: 1_000_000 },
    { suffix: "K", value: 1_000 },
  ];
  for (const unit of units) {
    if (absolute >= unit.value) {
      const scaled = absolute / unit.value;
      const digits = scaled >= 100 ? 0 : scaled >= 10 ? 1 : 1;
      return `${sign}${scaled.toFixed(digits).replace(/\.0$/, "")}${unit.suffix}`;
    }
  }
  return `${sign}${new Intl.NumberFormat("en-US").format(absolute)}`;
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
  if (!validDisplayDate(date)) {
    return "";
  }
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  const hour = String(date.getHours()).padStart(2, "0");
  const minute = String(date.getMinutes()).padStart(2, "0");
  const second = String(date.getSeconds()).padStart(2, "0");
  return `${month}-${day} ${hour}:${minute}:${second}`;
}

export function age(value?: string | number) {
  if (!value) {
    return "never";
  }
  const millis = typeof value === "number" ? value : new Date(value).getTime();
  if (!millis || Number.isNaN(millis)) {
    return "never";
  }
  if (typeof value === "string" && !validDisplayDate(new Date(value))) {
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

function validDisplayDate(date: Date) {
  return !Number.isNaN(date.getTime()) && date.getUTCFullYear() > 1;
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

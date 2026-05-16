import { AlertTriangle, CheckCircle2, CircleDot, HelpCircle, XCircle, type LucideIcon } from "lucide-react";
import { cx } from "../lib/format";

type StatusBadgeProps = {
  value?: string;
  tone?: "ok" | "warn" | "danger" | "unknown";
  title?: string;
  icon?: LucideIcon;
  iconOnly?: boolean;
};

export function inferStatusTone(value?: string): NonNullable<StatusBadgeProps["tone"]> {
  if (!value) {
    return "unknown";
  }
  if (["ready", "healthy", "succeeded", "completed", "reserved", "committed", "running", "ok"].includes(value)) {
    return "ok";
  }
  if (["degraded", "draining", "auth-updating", "refresh_soon", "refreshing", "warning", "released"].includes(value)) {
    return "warn";
  }
  if (["down", "expired", "revoked", "conflict", "unavailable", "no_login", "failed", "rejected", "provider_error", "critical", "disabled"].includes(value)) {
    return "danger";
  }
  return "unknown";
}

export function StatusBadge({ value, tone, title, icon, iconOnly }: StatusBadgeProps) {
  const resolved = tone ?? inferStatusTone(value);
  const Icon = icon ?? (resolved === "ok" ? CheckCircle2 : resolved === "warn" ? AlertTriangle : resolved === "danger" ? XCircle : value ? CircleDot : HelpCircle);
  const label = value || "unknown";
  return (
    <span className={cx("status-badge", iconOnly && "status-icon-only", `status-${resolved}`)} title={title || label} aria-label={iconOnly ? label : undefined}>
      <Icon aria-hidden="true" size={14} />
      {iconOnly ? null : <span>{label}</span>}
    </span>
  );
}

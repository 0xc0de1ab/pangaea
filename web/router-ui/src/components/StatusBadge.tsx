import { AlertTriangle, CheckCircle2, CircleDot, HelpCircle, XCircle } from "lucide-react";
import { cx } from "../lib/format";

type StatusBadgeProps = {
  value?: string;
  tone?: "ok" | "warn" | "danger" | "unknown";
  title?: string;
};

function inferTone(value?: string): NonNullable<StatusBadgeProps["tone"]> {
  if (!value) {
    return "unknown";
  }
  if (["ready", "healthy", "succeeded", "completed", "reserved", "committed", "running", "ok"].includes(value)) {
    return "ok";
  }
  if (["degraded", "draining", "auth-updating", "refresh_soon", "refreshing", "warning", "released"].includes(value)) {
    return "warn";
  }
  if (["down", "expired", "revoked", "conflict", "unavailable", "failed", "rejected", "provider_error", "critical", "disabled"].includes(value)) {
    return "danger";
  }
  return "unknown";
}

export function StatusBadge({ value, tone, title }: StatusBadgeProps) {
  const resolved = tone ?? inferTone(value);
  const Icon = resolved === "ok" ? CheckCircle2 : resolved === "warn" ? AlertTriangle : resolved === "danger" ? XCircle : value ? CircleDot : HelpCircle;
  return (
    <span className={cx("status-badge", `status-${resolved}`)} title={title}>
      <Icon aria-hidden="true" size={14} />
      <span>{value || "unknown"}</span>
    </span>
  );
}

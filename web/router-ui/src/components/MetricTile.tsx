import type { ReactNode } from "react";
import { cx } from "../lib/format";

type MetricTileProps = {
  label: string;
  value: ReactNode;
  subvalue?: ReactNode;
  tone?: "ok" | "warn" | "danger" | "neutral";
};

export function MetricTile({ label, value, subvalue, tone = "neutral" }: MetricTileProps) {
  return (
    <div className={cx("metric-tile", `metric-${tone}`)}>
      <div className="metric-label">{label}</div>
      <div className="metric-value">{value}</div>
      {subvalue ? <div className="metric-subvalue">{subvalue}</div> : null}
    </div>
  );
}

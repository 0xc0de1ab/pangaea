import type { ReactNode } from "react";
import { AlertTriangle } from "lucide-react";

type SectionProps = {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  error?: unknown;
  children: ReactNode;
};

export function Section({ title, subtitle, actions, error, children }: SectionProps) {
  return (
    <section className="section">
      <div className="section-header">
        <div>
          <h2>{title}</h2>
          {subtitle ? <p>{subtitle}</p> : null}
        </div>
        {actions ? <div className="section-actions">{actions}</div> : null}
      </div>
      {error ? (
        <div className="section-error">
          <AlertTriangle aria-hidden="true" size={15} />
          <span>{error instanceof Error ? error.message : "Section failed to load"}</span>
        </div>
      ) : null}
      <div className="section-body">{children}</div>
    </section>
  );
}

export function EmptyState({ children = "No records", className }: { children?: ReactNode; className?: string }) {
  return <div className={["empty-state", className].filter(Boolean).join(" ")}>{children}</div>;
}

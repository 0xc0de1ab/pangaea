import type { ReactNode } from "react";
import { X } from "lucide-react";

type DrawerProps = {
  open: boolean;
  title: string;
  subtitle?: string;
  onClose: () => void;
  children: ReactNode;
};

export function Drawer({ open, title, subtitle, onClose, children }: DrawerProps) {
  if (!open) {
    return null;
  }
  return (
    <div className="drawer-layer" role="presentation">
      <button className="drawer-scrim" type="button" aria-label="Close drawer" onClick={onClose} />
      <aside className="drawer" aria-label={title}>
        <div className="drawer-header">
          <div>
            <h2>{title}</h2>
            {subtitle ? <p>{subtitle}</p> : null}
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close drawer">
            <X aria-hidden="true" size={18} />
          </button>
        </div>
        <div className="drawer-body">{children}</div>
      </aside>
    </div>
  );
}

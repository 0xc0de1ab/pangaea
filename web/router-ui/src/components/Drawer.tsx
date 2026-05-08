import { useEffect, useRef, useState, type ReactNode } from "react";
import { X } from "lucide-react";

type DrawerProps = {
  open: boolean;
  title: string;
  subtitle?: string;
  onClose: () => void;
  children: ReactNode;
};

const panelExitMs = 260;

export function Drawer({ open, title, subtitle, onClose, children }: DrawerProps) {
  const [mounted, setMounted] = useState(open);
  const [exiting, setExiting] = useState(false);
  const lastContent = useRef({ title, subtitle, children });

  useEffect(() => {
    if (open) {
      lastContent.current = { title, subtitle, children };
      setMounted(true);
      setExiting(false);
      return;
    }
    if (!mounted) return;
    setExiting(true);
    const timeout = window.setTimeout(() => {
      setMounted(false);
      setExiting(false);
    }, panelExitMs);
    return () => window.clearTimeout(timeout);
  }, [children, mounted, open, subtitle, title]);

  if (!mounted) {
    return null;
  }

  const content = open ? { title, subtitle, children } : lastContent.current;

  return (
    <div className={`drawer-layer${exiting ? " is-exiting" : ""}`} role="presentation">
      <button className="drawer-scrim" type="button" aria-label="Close drawer" onClick={onClose} />
      <aside className="drawer" aria-label={content.title}>
        <div className="drawer-header">
          <div>
            <h2>{content.title}</h2>
            {content.subtitle ? <p>{content.subtitle}</p> : null}
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close drawer">
            <X aria-hidden="true" size={18} />
          </button>
        </div>
        <div className="drawer-body">{content.children}</div>
      </aside>
    </div>
  );
}

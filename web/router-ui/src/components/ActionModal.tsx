import { useEffect, useState } from "react";
import { AlertTriangle, Loader2, X } from "lucide-react";

export type ConfirmAction = {
  title: string;
  target: string;
  detail: string;
  requireReason?: boolean;
  confirmLabel?: string;
  danger?: boolean;
  execute: (reason: string) => Promise<void>;
};

type ActionModalProps = {
  action: ConfirmAction | null;
  onClose: () => void;
};

export function ActionModal({ action, onClose }: ActionModalProps) {
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [error, setError] = useState("");
  const [running, setRunning] = useState(false);

  useEffect(() => {
    setReason("");
    setConfirmed(false);
    setError("");
    setRunning(false);
  }, [action]);

  if (!action) {
    return null;
  }

  const canSubmit = confirmed && (!action.requireReason || reason.trim().length > 0) && !running;

  async function submit() {
    if (!canSubmit || !action) {
      return;
    }
    setRunning(true);
    setError("");
    try {
      await action.execute(reason.trim());
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Action failed");
    } finally {
      setRunning(false);
    }
  }

  return (
    <div className="modal-layer" role="presentation">
      <button className="modal-scrim" type="button" aria-label="Close dialog" onClick={onClose} />
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby="action-title">
        <div className="modal-header">
          <div className="modal-title-row">
            <AlertTriangle aria-hidden="true" size={18} />
            <h2 id="action-title">{action.title}</h2>
          </div>
          <button className="icon-button" type="button" aria-label="Close dialog" onClick={onClose}>
            <X aria-hidden="true" size={17} />
          </button>
        </div>
        <div className="modal-body">
          <div className="kv-list">
            <div className="kv-key">Target</div>
            <div className="kv-value mono">{action.target}</div>
            <div className="kv-key">Impact</div>
            <div className="kv-value">{action.detail}</div>
          </div>
          {action.requireReason ? (
            <label className="field">
              <span>Reason</span>
              <input value={reason} onChange={(event) => setReason(event.target.value)} autoComplete="off" />
            </label>
          ) : null}
          <label className="check-row">
            <input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} />
            <span>I understand this operation will be audited.</span>
          </label>
          {error ? <div className="inline-error">{error}</div> : null}
        </div>
        <div className="modal-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            Cancel
          </button>
          <button type="button" className={action.danger ? "button danger" : "button primary"} disabled={!canSubmit} onClick={submit}>
            {running ? <Loader2 aria-hidden="true" className="spin" size={15} /> : null}
            {action.confirmLabel || "Execute"}
          </button>
        </div>
      </div>
    </div>
  );
}

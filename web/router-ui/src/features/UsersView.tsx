import type { FormEvent } from "react";
import { useMemo, useState } from "react";
import { Edit3, Plus, RefreshCw, Trash2 } from "lucide-react";
import type { DashboardViewProps } from "../app/dashboard";
import { DataTable, type DashboardColumn } from "../components/DataTable";
import { Section } from "../components/Section";
import { StatusBadge } from "../components/StatusBadge";
import { api } from "../lib/api";
import { fmtTime, hasText } from "../lib/format";
import type { RouterUser } from "../lib/types";

type UserFormState = {
  email: string;
  name: string;
  role: string;
  enabled: boolean;
};

const emptyForm: UserFormState = {
  email: "",
  name: "",
  role: "user",
  enabled: true,
};

export function UsersView({ data, queries, search, token, refresh }: DashboardViewProps) {
  const [form, setForm] = useState<UserFormState>(emptyForm);
  const [editingEmail, setEditingEmail] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const rows = useMemo(() => data.users.filter((user) => hasText(user, search)), [data.users, search]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      if (editingEmail) {
        await api.updateUser(editingEmail, { name: form.name, role: form.role, enabled: form.enabled }, token);
      } else {
        await api.createUser({ email: form.email, name: form.name, role: form.role, enabled: form.enabled }, token);
      }
      setForm(emptyForm);
      setEditingEmail("");
      refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save user");
    } finally {
      setSaving(false);
    }
  }

  function edit(user: RouterUser) {
    setEditingEmail(user.email);
    setForm({
      email: user.email,
      name: user.name || "",
      role: user.role || "user",
      enabled: user.enabled,
    });
  }

  async function remove(user: RouterUser) {
    if (!window.confirm(`Delete ${user.email}?`)) {
      return;
    }
    setError("");
    try {
      await api.deleteUser(user.email, token);
      if (editingEmail === user.email) {
        setEditingEmail("");
        setForm(emptyForm);
      }
      refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete user");
    }
  }

  const columns: DashboardColumn<RouterUser>[] = [
    {
      id: "email",
      header: "Email",
      sortValue: (row) => row.email,
      cell: (row) => (
        <div className="stacked-cell">
          <strong>{row.email}</strong>
          <span>{row.name || "no display name"}</span>
        </div>
      ),
    },
    {
      id: "role",
      header: "Role",
      sortValue: (row) => row.role,
      cell: (row) => <StatusBadge value={row.role} tone={row.role === "admin" ? "warn" : "unknown"} />,
      width: "120px",
    },
    {
      id: "enabled",
      header: "State",
      sortValue: (row) => row.enabled,
      cell: (row) => <StatusBadge value={row.enabled ? "enabled" : "disabled"} tone={row.enabled ? "ok" : "danger"} />,
      width: "120px",
    },
    {
      id: "updated",
      header: "Updated",
      sortValue: (row) => row.updated_at || "",
      cell: (row) => fmtTime(row.updated_at),
      width: "140px",
    },
    {
      id: "actions",
      header: "Actions",
      cell: (row) => (
        <div className="row-actions">
          <button className="icon-button small" type="button" aria-label={`Edit ${row.email}`} onClick={() => edit(row)}>
            <Edit3 aria-hidden="true" size={14} />
          </button>
          <button className="icon-button small danger-text" type="button" aria-label={`Delete ${row.email}`} onClick={() => void remove(row)}>
            <Trash2 aria-hidden="true" size={14} />
          </button>
        </div>
      ),
      width: "110px",
    },
  ];

  return (
    <div className="view-stack">
      <Section
        title="Users"
        subtitle="Google OAuth accounts allowed to enter the dashboard and manage their own routing rules"
        error={queries.users.error}
        actions={
          <button className="button secondary" type="button" onClick={refresh}>
            <RefreshCw aria-hidden="true" size={15} />
            Refresh
          </button>
        }
      >
        <form className="user-form" onSubmit={submit}>
          <label className="field">
            <span>Email</span>
            <input value={form.email} onChange={(event) => setForm((value) => ({ ...value, email: event.target.value }))} disabled={Boolean(editingEmail)} placeholder="user@example.com" />
          </label>
          <label className="field">
            <span>Name</span>
            <input value={form.name} onChange={(event) => setForm((value) => ({ ...value, name: event.target.value }))} placeholder="Display name" />
          </label>
          <label className="field">
            <span>Role</span>
            <select value={form.role} onChange={(event) => setForm((value) => ({ ...value, role: event.target.value }))}>
              <option value="user">User</option>
              <option value="admin">Admin</option>
            </select>
          </label>
          <label className="check-row inline">
            <input type="checkbox" checked={form.enabled} onChange={(event) => setForm((value) => ({ ...value, enabled: event.target.checked }))} />
            <span>enabled</span>
          </label>
          <button className="button primary" type="submit" disabled={saving || !form.email.trim()}>
            <Plus aria-hidden="true" size={15} />
            {editingEmail ? "Update" : "Add"}
          </button>
          {editingEmail ? (
            <button className="button ghost" type="button" onClick={() => { setEditingEmail(""); setForm(emptyForm); }}>
              Cancel
            </button>
          ) : null}
        </form>
        {error ? <div className="inline-error">{error}</div> : null}
        <DataTable rows={rows} columns={columns} empty="No users are registered" getRowId={(row) => row.email} compact />
      </Section>
    </div>
  );
}

import { useState } from "react";
import { ActionDropdown } from "../ActionDropdown";
import { Action, Field, Form, Table } from "../ui";

export function Users({ data: d, post }) {
  return <div className="workspace-page">
    <h1 className="workspace-heading">Users</h1>
    <section className="workspace-panel">
      <h2>Tambah user</h2>
      <Form className="workspace-form user-create-form" onSave={v => post("/users/create", v)}>
        <Field name="username" label="Username" required />
        <Field name="email" label="Email" type="email" />
        <Field name="password" label="Password" type="password" minLength="8" required />
        <Field name="is_admin" label="Role"
          choices={[["false", "User"], ["true", "Platform Admin"]]} />
      </Form>
    </section>
    <section className="workspace-panel users-list">
      <h2>Daftar user</h2>
      <Table rows={d.users} columns={[
        ["Username", "Username"], ["Email", "Email"],
        ["Groups", u => u.Groups?.join(", ") || "—"],
        ["Status", u => u.IsActive ? "Aktif" : "Nonaktif"],
        ["Role", u => u.IsAdmin ? "Admin" : "User"],
        ["Aksi", user => <UserActions user={user} post={post} />],
      ]} />
    </section>
  </div>;
}

function UserActions({ user, post }) {
  const [showReset, setShowReset] = useState(false);

  return (
    <ActionDropdown label="Kelola" accessibleLabel={`Kelola ${user.Username}`}
      className="user-action-options" onClose={() => setShowReset(false)}>
      <Action onClick={() => post("/users/toggle-active",
        { user_id: user.ID, is_active: String(!user.IsActive) })}>
        {user.IsActive ? "Nonaktifkan akun" : "Aktifkan akun"}
      </Action>
      <Action confirmText={`Ubah role ${user.Username}?`}
        onClick={() => post("/users/toggle-admin",
          { user_id: user.ID, is_admin: String(!user.IsAdmin) })}>
        {user.IsAdmin ? "Cabut hak admin" : "Jadikan admin"}
      </Action>
      <button type="button" aria-expanded={showReset}
        onClick={() => setShowReset(open => !open)}>
        Reset password
      </button>
      {showReset && (
        <Form button="Simpan password"
          onSave={values => post("/users/reset-password", { ...values, user_id: user.ID })}>
          <Field name="new_password" label="Password baru"
            type="password" minLength="8" autoComplete="new-password" required />
        </Form>
      )}
    </ActionDropdown>
  );
}

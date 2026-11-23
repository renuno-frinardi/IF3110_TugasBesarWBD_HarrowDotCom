import React from "react";
import { api } from "./api";
import { Action, Field, Form, Table, link } from "./ui";

const options = (rows, label) =>
  (rows || []).map(row => [row.ID, row[label]]);

function pgOnly(rows) {
  return (rows || []).filter(row => !["mariadb", "mysql"].includes(row.DBType));
}

function dbLink(path, id, extra = {}) {
  return link(path, { id, source: "postgresql", ...extra });
}

export function Workspace({ path, params, data: d, session, run, post }) {
  const admin = session.is_admin;
  const c = d.connection || {};
  const id = params.get("id");
  const connection = { id, source: "postgresql" };

  if (path === "/" || path === "/connections") {
    const dashboard = path === "/";
    return <>
      <h1>{dashboard ? "Dashboard" : "Koneksi PostgreSQL"}</h1>
      {dashboard && <p>
        Koneksi yang dapat diakses: {d.stats?.Connections}.
        {admin && ` Users: ${d.stats?.Users}. Groups: ${d.stats?.Groups}.`}
      </p>}
      {admin && <a href={link("/connections/new")}>Tambah koneksi</a>}
      {!dashboard && (
        <Form button="Terapkan filter" onSave={values => {
          window.location.hash = link("/connections", values).slice(1);
        }}>
          <Field name="search" label="Cari koneksi"
            defaultValue={params.get("search") || ""} />
          <Field name="status" label="Status" defaultValue={params.get("status") || ""}
            choices={[["", "Semua"], ["active", "Aktif"], ["inactive", "Nonaktif"]]} />
        </Form>
      )}
      <Table rows={pgOnly(dashboard ? d.connections : d.postgres)} columns={[
        ["Nama", row => <a href={dbLink("/connections/view", row.ID)}>{row.Name}</a>],
        ["Database", "DatabaseName"], ["Host", "Host"], ["Port", "Port"],
        ["Status", row => row.IsActive ? "Aktif" : "Nonaktif"],
      ]} />
      {dashboard && <a href={link("/connections")}>Lihat semua koneksi</a>}
    </>;
  }

  if (path === "/connections/view") {
    return <>
      <h1>{c.Name}</h1>
      <p>{c.DBType} · {c.Host}:{c.Port} · {c.DatabaseName}</p>
      <p>User: {c.Username} · SSL: {c.SSLMode} · {c.IsActive ? "Aktif" : "Nonaktif"}</p>
      <div className="actions">
        {c.IsActive && <>
          <a href={dbLink("/connections/explore", id)}>Explorer</a>
          <a href={link("/migrations", { db: id })}>Migrations</a>
        </>}
        {admin && <>
          <a href={dbLink("/connections/edit", id)}>Edit koneksi</a>
          <Action onClick={() => post("/connections/toggle-active",
            { ...connection, is_active: String(!c.IsActive) })}>
            {c.IsActive ? "Nonaktifkan" : "Aktifkan"}
          </Action>
          <Action confirmText={`Hapus koneksi ${c.Name}?`}
            onClick={() => post("/connections/delete", connection)}>Hapus</Action>
        </>}
      </div>
    </>;
  }

  if (path === "/connections/new" || path === "/connections/edit") {
    const editing = path.endsWith("/edit");
    return <>
      <h1>{editing ? "Edit koneksi" : "Tambah koneksi PostgreSQL"}</h1>
      <Form onSave={(values, action) => {
        const body = { ...values, db_type: "postgresql" };
        if (action === "test") {
          run(async () => {
            const result = await api("/connections/test", { method: "POST", form: body });
            return { notice: result.success };
          });
        } else {
          post(editing ? `${path}?${new URLSearchParams(connection)}` : path, body);
        }
      }}>
        <Field name="name" label="Nama koneksi" defaultValue={c.Name} required />
        <Field name="host" defaultValue={c.Host} required />
        <Field name="port" type="number" min="1" max="65535"
          defaultValue={c.Port || 5432} required />
        <Field name="database_name" label="Database" defaultValue={c.DatabaseName} required />
        <Field name="username" defaultValue={c.Username} required />
        <Field name="password" type="password" required={!editing}
          label={editing ? "Password baru (opsional)" : "Password"} />
        <Field name="ssl_mode" label="SSL mode" defaultValue={c.SSLMode || "prefer"}
          choices={["prefer", "require", "verify-full", "disable"].map(s => [s, s])} />
        {!editing && <button type="submit" value="test">Test koneksi</button>}
      </Form>
    </>;
  }

  if (path === "/connections/explore") {
    return <Explorer {...{ d, id, post }} />;
  }

  if (path === "/users") {
    return <>
      <h1>Users</h1>
      <Form title="Tambah user" onSave={v => post("/users/create", v)}>
        <Field name="username" required />
        <Field name="email" type="email" />
        <Field name="password" type="password" minLength="8" required />
        <Field name="is_admin" label="Role"
          choices={[["false", "User"], ["true", "Platform Admin"]]} />
      </Form>
      <Table rows={d.users} columns={[
        ["Username", "Username"], ["Email", "Email"],
        ["Groups", u => u.Groups?.join(", ") || "—"],
        ["Status", u => u.IsActive ? "Aktif" : "Nonaktif"],
        ["Role", u => u.IsAdmin ? "Admin" : "User"],
        ["Aksi", u => <>
          <Action onClick={() => post("/users/toggle-active",
            { user_id: u.ID, is_active: String(!u.IsActive) })}>
            {u.IsActive ? "Nonaktifkan" : "Aktifkan"}
          </Action>
          <Action confirmText={`Ubah role ${u.Username}?`}
            onClick={() => post("/users/toggle-admin",
              { user_id: u.ID, is_admin: String(!u.IsAdmin) })}>
            {u.IsAdmin ? "Cabut admin" : "Jadikan admin"}
          </Action>
          <Form button="Reset password"
            onSave={v => post("/users/reset-password", { ...v, user_id: u.ID })}>
            <Field name="new_password" label="Password baru"
              type="password" minLength="8" required />
          </Form>
        </>],
      ]} />
    </>;
  }

  if (path === "/groups") {
    return <>
      <h1>Groups</h1>
      <Form title="Buat group" onSave={v => post("/groups/create", v)}>
        <Field name="name" required />
        <Field name="description" />
      </Form>
      {(d.groups || []).map(g => (
        <section key={g.ID}>
          <h2>{g.Name}</h2>
          <Form onSave={v => post("/groups/update", { ...v, group_id: g.ID })}>
            <Field name="name" defaultValue={g.Name} required />
            <Field name="description" defaultValue={g.Description} />
          </Form>
          <Form button="Tambah anggota" onSave={v =>
            post("/groups/members/add", { ...v, group_id: g.ID })}>
            <Field name="user_id" label="User" required choices={options(
              (d.users || []).filter(u => !g.Members?.some(m => m.UserID === u.ID)),
              "Username"
            )} />
          </Form>
          <ul>{(g.Members || []).map(m => (
            <li key={m.UserID}>{m.Username}{" "}
              {g.LeaderID === m.UserID && <strong>Leader </strong>}
              <Action onClick={() => post("/groups/members/remove",
                { group_id: g.ID, user_id: m.UserID })}>Keluarkan</Action>
            </li>
          ))}</ul>
          {!!g.Members?.length && (
            <Form button="Tetapkan leader" onSave={v => run(() =>
              api(`/v2/groups/${g.ID}/leader`, {
                method: "PUT", json: { user_id: Number(v.user_id) },
              }))}>
              <Field name="user_id" label="Leader" defaultValue={g.LeaderID || ""}
                choices={g.Members.map(m => [m.UserID, m.Username])} required />
            </Form>
          )}
          <Action confirmText={`Hapus group ${g.Name}?`}
            onClick={() => post("/groups/delete", { group_id: g.ID })}>Hapus group</Action>
        </section>
      ))}
    </>;
  }

  if (path === "/access") {
    return <>
      <h1>Akses database</h1>
      <Form button="Berikan akses" onSave={v => post("/access/grant", v)}>
        <Field name="group_id" label="Group" choices={options(d.groups, "Name")} required />
        <Field name="database_connection_id" label="Database"
          choices={options(pgOnly(d.connections), "Name")} required />
        <Field name="access_level" label="Hak akses" required
          choices={[["view_only", "View"], ["view_edit", "View + edit"]]} />
      </Form>
      <Table rows={pgOnly(d.grants)} columns={[
        ["Group", "GroupName"], ["Koneksi", "ConnectionName"], ["Akses", "AccessLevel"],
        ["Aksi", row => <Action onClick={() => post("/access/revoke", {
          group_id: row.GroupID, database_connection_id: row.DatabaseConnectionID,
        })}>Cabut akses</Action>],
      ]} />
    </>;
  }

  if (path === "/audit-logs") {
    return <>
      <h1>Audit log M1</h1>
      <Table rows={d.audit_logs} columns={[
        ["Waktu", "CreatedAt"], ["Actor", "ActorUsername"], ["Aksi", "Action"],
        ["Resource", "ResourceType"], ["ID", "ResourceID"], ["Metadata", "Metadata"],
      ]} />
    </>;
  }

  return <p>Halaman tidak ditemukan.</p>;
}

function Explorer({ d, id, post }) {
  const e = d.explorer;
  const columns = e.Columns || [];
  const scope = {
    id, source: "postgresql", schema: e.SelectedSchema,
    table: e.SelectedTable, page: e.Page,
  };

  function rowIdentity(row, index) {
    const values = { ...scope, row_ctid: e.RowIDs?.[index] || "" };
    row.forEach((value, i) => { values[`old_${i}`] = value; });
    return values;
  }

  return <>
    <h1>Explorer · {d.connection.Name}</h1>
    <nav>{(e.Schemas || []).map(schema => (
      <a key={schema} href={dbLink("/connections/explore", id, { schema })}>{schema}</a>
    ))}</nav>
    <nav>{(e.Tables || []).map(table => (
      <a key={table} href={dbLink("/connections/explore", id,
        { schema: e.SelectedSchema, table })}>{table}</a>
    ))}</nav>

    {e.SelectedTable && <>
      <h2>{e.SelectedSchema}.{e.SelectedTable}</h2>
      {["Constraints", "Indexes"].map(key => <section key={key}>
        <h3>{key}</h3>
        <Table rows={e[key]} columns={[
          ["Nama", "Name"], ["Tipe", "Type"], ["Definisi", "Definition"],
        ]} />
      </section>)}

      {d.can_edit && <Form title="Tambah baris" button="Insert"
        onSave={v => post("/connections/explore/insert", { ...scope, ...v })}>
        {columns.map(col => <Field key={col} name={`col_${col}`} label={col} />)}
      </Form>}

      <Table rows={e.Rows} columns={[
        ...columns.map((name, i) => [name, row => row[i]]),
        ...(d.can_edit ? [["Ubah baris", (row, index) => (
          <details>
            <summary>Edit atau hapus</summary>
            <Form button="Simpan baris" onSave={v =>
              post("/connections/explore/update", { ...rowIdentity(row, index), ...v })}>
              {columns.map((col, i) => (
                <Field key={col} name={`new_${i}`} label={col} defaultValue={row[i]} />
              ))}
            </Form>
            <Action confirmText="Hapus baris ini?" onClick={() =>
              post("/connections/explore/delete", rowIdentity(row, index))}>Hapus</Action>
          </details>
        )]] : []),
      ]} />
      <nav>
        {e.Page > 1 && <a href={link("/connections/explore",
          { ...scope, page: e.Page - 1 })}>Sebelumnya</a>}
        <span>Halaman {e.Page}</span>
        {e.HasNextPage && <a href={link("/connections/explore",
          { ...scope, page: e.Page + 1 })}>Berikutnya</a>}
      </nav>
    </>}
  </>;
}
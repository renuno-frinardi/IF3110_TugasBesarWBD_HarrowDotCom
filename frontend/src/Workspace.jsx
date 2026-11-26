import { useId, useRef, useState } from "react";
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
      <h1>{dashboard ? <span>Dashboard - Welcome, {session.username}</span> : "Koneksi PostgreSQL"}</h1>
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
      <Form className="connection-form" onSave={(values, action) => {
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
    return <Explorer {...{ d, id, post, run }} />;
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
        ["Aksi", user => <UserActions user={user} post={post} />],
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
          <ul className="group-members">{(g.Members || []).map(m => (
            <li key={m.UserID}>
              <span>{m.Username}{" "}
                {g.LeaderUserID === m.UserID && <strong>Leader</strong>}
              </span>
              <button type="button" className="remove-member"
                aria-label={`Keluarkan ${m.Username}`}
                onClick={() => post("/groups/members/remove", { group_id: g.ID, user_id: m.UserID })}>
                Keluarkan
              </button>
            </li>
          ))}</ul>
          {!!g.Members?.length && (
            <Form button="Tetapkan leader" onSave={v => run(() =>
              api(`/v2/groups/${g.ID}/leader`, {
                method: "PUT", json: { user_id: Number(v.user_id) },
              }))}>
              <Field name="user_id" label="Leader" defaultValue={g.LeaderUserID || ""}
                choices={[["", "Pilih anggota"], ...g.Members.map(m => [m.UserID, m.Username])]}
                required />
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
      <h1>Audit Logs</h1>
      <Table rows={d.audit_logs} columns={[
        ["Waktu", "CreatedAt"], ["Actor", "ActorUsername"], ["Aksi", "Action"],
        ["Resource", "ResourceType"], ["ID", "ResourceID"], ["Metadata", "Metadata"],
      ]} />
    </>;
  }

  return <p>Halaman tidak ditemukan.</p>;
}

function ActionDropdown({ label, accessibleLabel, className, onClose, children }) {
  const menuId = useId();
  const triggerRef = useRef(null);

  function positionMenu(menu) {
    const trigger = triggerRef.current;
    const viewportHeight = document.documentElement.clientHeight;
    const gap = 8;
    if (viewportHeight - trigger.getBoundingClientRect().bottom < menu.offsetHeight + gap) {
      menu.dataset.needsRoom = "true";
      trigger.scrollIntoView({ block: "center", inline: "nearest" });
    }

    const { bottom, right } = trigger.getBoundingClientRect();
    menu.style.setProperty("--trigger-right", `${right}px`);
    menu.style.top = `${window.scrollY + bottom + gap}px`;
    menu.style.maxHeight = `${Math.max(44, viewportHeight - bottom - gap * 2)}px`;
    menu.dataset.positioned = "true";
  }

  return (
    <>
      <button ref={triggerRef} type="button" className="action-dropdown-toggle"
        popoverTarget={menuId} aria-label={accessibleLabel}>
        {label}
      </button>
      <div id={menuId} popover="auto" className={`action-dropdown ${className}`}
        onToggle={event => {
          const menu = event.currentTarget;
          if (event.nativeEvent.newState === "open") {
            positionMenu(menu);
          } else {
            delete menu.dataset.needsRoom;
            delete menu.dataset.positioned;
            menu.style.maxHeight = "";
            onClose?.();
          }
        }}>
        {children}
      </div>
    </>
  );
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

function Explorer({ d, id, post, run }) {
  const [insertError, setInsertError] = useState("");
  const e = d.explorer;
  const columns = e.Columns || [];
  const scope = {
    id, source: "postgresql", schema: e.SelectedSchema,
    table: e.SelectedTable, page: e.Page,
  };

  function insertRow(values) {
    setInsertError("");
    run(async () => {
      const result = await api("/connections/explore/insert", {
        method: "POST", form: { ...scope, ...values },
      });
      const message = result.navigate && new URLSearchParams(result.navigate.split("?")[1]).get("error");
      if (message) throw new Error(message);
      return result;
    }, setInsertError);
  }

  function rowIdentity(row, index) {
    const values = { ...scope, row_ctid: e.RowIDs?.[index] || "" };
    row.forEach((value, i) => { values[`old_${i}`] = value; });
    return values;
  }

  return <>
    <h1>Explorer · {d.connection.Name}</h1>
    {d.success && !insertError && <p role="status">{d.success}</p>}
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

      {d.can_edit && <Form title="Tambah baris" button="Insert" onSave={insertRow}>
        {columns.map(col => <Field key={col} name={`col_${col}`} label={col} />)}
      </Form>}
      {insertError && <p role="alert">{insertError}</p>}

      <Table rows={e.Rows} columns={[
        ...columns.map((name, i) => [name, row => row[i]]),
        ...(d.can_edit ? [["Ubah baris", (row, index) => (
          <ActionDropdown label="Edit atau hapus" accessibleLabel={`Edit atau hapus baris ${index + 1}`}
            className="explorer-row-actions">
            <Form button="Simpan baris" onSave={v =>
              post("/connections/explore/update", { ...rowIdentity(row, index), ...v })}>
              {columns.map((col, i) => (
                <Field key={col} name={`new_${i}`} label={col} defaultValue={row[i]} />
              ))}
            </Form>
            <Action className="delete-row" confirmText="Hapus baris ini?" onClick={() =>
              post("/connections/explore/delete", rowIdentity(row, index))}>Hapus</Action>
          </ActionDropdown>
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

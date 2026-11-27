import { useId, useRef, useState } from "react";
import { api } from "./api";
import { Action, Field, Form, Table, link } from "./ui";

const options = (rows, label) =>
  (rows || []).map(row => [row.ID, row[label]]);

function pgOnly(rows) {
  return (rows || []).filter(row => !["mariadb", "mysql"].includes(row.DBType));
}

function accessLabel(level) {
  if (level === "view_edit") return "View + Edit";
  if (level === "view_only") return "View";
  return level || "Tidak diketahui";
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
      {admin && <div className="actions">
        <a className="button-link" href={link("/connections/new")}>Tambah koneksi</a>
      </div>}
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
      {dashboard && <div className="actions">
        <a className="button-link" href={link("/connections")}>Lihat semua koneksi</a>
      </div>}
    </>;
  }

  if (path === "/connections/view") {
    return <>
      <section className="connection-detail">
        <div className="connection-detail-heading">
          <div>
            <p className="connection-eyebrow">Koneksi database</p>
            <div className="connection-title-row">
              <h1>{c.Name}</h1>
              <span className="connection-type">
                {c.DBType === "postgresql" ? "PostgreSQL" : c.DBType}
              </span>
            </div>
          </div>
          <span className={`connection-status ${c.IsActive
            ? "connection-status--active" : "connection-status--inactive"}`}>
            <span aria-hidden="true" />
            {c.IsActive ? "Aktif" : "Nonaktif"}
          </span>
        </div>

        <dl className="connection-metadata">
          <div><dt>Database</dt><dd>{c.DatabaseName}</dd></div>
          <div><dt>Host dan port</dt><dd>{c.Host}:{c.Port}</dd></div>
          <div><dt>User</dt><dd>{c.Username}</dd></div>
          <div><dt>SSL mode</dt><dd>{c.SSLMode}</dd></div>
        </dl>

        <div className="connection-actions">
          <div className="connection-action-group">
            {c.IsActive && <>
              <a className="button-link" href={dbLink("/connections/explore", id)}>Explorer</a>
              <a className="button-link" href={link("/migrations", { db: id })}>Migrations</a>
            </>}
          </div>
          {admin && <div className="connection-action-group connection-admin-actions">
            <a className="button-link button-link--secondary"
              href={dbLink("/connections/edit", id)}>Edit koneksi</a>
            <Action className="secondary-button" onClick={() => post("/connections/toggle-active",
              { ...connection, is_active: String(!c.IsActive) })}>
              {c.IsActive ? "Nonaktifkan" : "Aktifkan"}
            </Action>
            <Action className="danger-solid" confirmText={`Hapus koneksi ${c.Name}?`}
              onClick={() => post("/connections/delete", connection)}>Hapus koneksi</Action>
          </div>}
        </div>
      </section>
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
    return <div className="groups-page">
      <h1>Groups</h1>
      <details className="group-create">
        <summary>Buat group</summary>
        <Form button="Buat group" onSave={v => post("/groups/create", v)}>
          <Field name="name" label="Nama group" required />
          <Field name="description" label="Deskripsi" />
        </Form>
      </details>
      {!d.groups?.length && <p>Belum ada group.</p>}
      {(d.groups || []).map(g => {
        const members = g.Members || [];
        const candidates = (d.users || []).filter(u => !members.some(m => m.UserID === u.ID));
        return <section key={g.ID} className="group-card">
          <h2>{g.Name}</h2>
          {g.Description && <p className="group-description">{g.Description}</p>}
          <div className="group-columns">
            <div>
              <h3>Anggota ({members.length})</h3>
              {members.length ? (
                <ul className="group-members">{members.map(m => (
                  <li key={m.UserID}>
                    <span>{m.Username}{" "}
                      {g.LeaderUserID === m.UserID && <strong className="group-leader">Leader</strong>}
                    </span>
                    <button type="button" className="remove-member"
                      aria-label={`Keluarkan ${m.Username}`}
                      onClick={() => post("/groups/members/remove", { group_id: g.ID, user_id: m.UserID })}>
                      Keluarkan
                    </button>
                  </li>
                ))}</ul>
              ) : <p>Belum ada anggota.</p>}
            </div>
            <div className="group-controls">
              <h3>Kelola anggota</h3>
              {candidates.length ? (
                <Form button="Tambah anggota" onSave={v =>
                  post("/groups/members/add", { ...v, group_id: g.ID })}>
                  <Field name="user_id" label="Pengguna" required choices={options(candidates, "Username")} />
                </Form>
              ) : <p>Tidak ada pengguna yang bisa ditambahkan.</p>}
              {!!members.length && (
                <Form button="Tetapkan leader" onSave={v => run(() =>
                  api(`/v2/groups/${g.ID}/leader`, {
                    method: "PUT", json: { user_id: Number(v.user_id) },
                  }))}>
                  <Field name="user_id" label="Leader" defaultValue={g.LeaderUserID || ""}
                    choices={[["", "Pilih anggota"], ...members.map(m => [m.UserID, m.Username])]}
                    required />
                </Form>
              )}
            </div>
          </div>
          <details className="group-settings">
            <summary>Pengaturan group</summary>
            <Form onSave={v => post("/groups/update", { ...v, group_id: g.ID })}>
              <Field name="name" label="Nama group" defaultValue={g.Name} required />
              <Field name="description" label="Deskripsi" defaultValue={g.Description} />
            </Form>
            <Action className="danger-button" confirmText={`Hapus group ${g.Name}?`}
              onClick={() => post("/groups/delete", { group_id: g.ID })}>Hapus group</Action>
          </details>
        </section>;
      })}
    </div>;
  }

  if (path === "/access") {
    const grants = pgOnly(d.grants);
    return <div className="access-page">
      <h1>Akses database</h1>
      <div className="access-layout">
        <section className="access-panel">
          <h2>Berikan akses</h2>
          <Form className="access-form" onSave={v => post("/access/grant", v)} button="Berikan akses">
            <Field name="group_id" label="Group" choices={options(d.groups, "Name")} required />
            <Field name="database_connection_id" label="Database"
              choices={options(pgOnly(d.connections), "Name")} required />
            <Field name="access_level" label="Hak akses" required
              choices={[["view_only", "View"], ["view_edit", "View + Edit"]]} />
          </Form>
        </section>
        <section className="access-panel">
          <div className="access-list-heading">
            <h2>Akses yang diberikan</h2>
            <span className="access-count">{grants.length} akses</span>
          </div>
          {grants.length ? (
            <ul className="grant-list">
              {grants.map(row => (
                <li key={`${row.GroupID}-${row.DatabaseConnectionID}`}>
                  <div className="grant-target">
                    <strong>{row.GroupName}</strong>
                    <span>Database · {row.ConnectionName}</span>
                  </div>
                  <span className={`access-badge ${row.AccessLevel === "view_edit"
                    ? "access-badge--edit" : "access-badge--view"}`}>
                    {accessLabel(row.AccessLevel)}
                  </span>
                  <Action className="danger-button"
                    confirmText={`Cabut akses ${accessLabel(row.AccessLevel)} untuk group ${row.GroupName} ke ${row.ConnectionName}?`}
                    onClick={() => post("/access/revoke", {
                      group_id: row.GroupID, database_connection_id: row.DatabaseConnectionID,
                    })}>Cabut akses</Action>
                </li>
              ))}
            </ul>
          ) : (
            <p className="access-empty">Belum ada akses database yang diberikan.</p>
          )}
        </section>
      </div>
    </div>;
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
    <div className="explorer-page-heading">
      <div>
        <p className="connection-eyebrow">Database Explorer</p>
        <h1>{d.connection.Name}</h1>
        <p>Pilih schema dan tabel untuk melihat atau mengelola datanya.</p>
      </div>
      <a className="button-link button-link--secondary"
        href={dbLink("/connections/view", id)}>Detail koneksi</a>
    </div>
    {d.success && !insertError && <p role="status">{d.success}</p>}
    <section className="explorer-selector">
      <div className="explorer-selector-heading">
        <div>
          <h2>1. Pilih schema</h2>
          <p>Schema mengelompokkan tabel dalam database.</p>
        </div>
        <span className="access-count">{(e.Schemas || []).length} schema</span>
      </div>
      {(e.Schemas || []).length ? (
        <nav className="explorer-options" aria-label="Schema database">
          {(e.Schemas || []).map(schema => {
            const selected = schema === e.SelectedSchema;
            return (
              <a key={schema}
                className={`explorer-option${selected ? " is-selected" : ""}`}
                href={dbLink("/connections/explore", id, { schema })}
                aria-current={selected ? "page" : undefined}>
                <span>{schema}</span>
              </a>
            );
          })}
        </nav>
      ) : <p className="access-empty">Tidak ada schema yang dapat ditampilkan.</p>}
    </section>

    {e.SelectedSchema && <section className="explorer-selector">
      <div className="explorer-selector-heading">
        <div>
          <h2>2. Pilih tabel</h2>
          <p>Schema aktif: <strong>{e.SelectedSchema}</strong></p>
        </div>
        <span className="access-count">{(e.Tables || []).length} tabel</span>
      </div>
      {(e.Tables || []).length ? (
        <nav className="explorer-options" aria-label={`Tabel pada schema ${e.SelectedSchema}`}>
          {(e.Tables || []).map(table => {
            const selected = table === e.SelectedTable;
            return (
              <a key={table}
                className={`explorer-option${selected ? " is-selected" : ""}`}
                href={dbLink("/connections/explore", id,
                  { schema: e.SelectedSchema, table })}
                aria-current={selected ? "page" : undefined}>
                <span>{table}</span>
              </a>
            );
          })}
        </nav>
      ) : <p className="access-empty">Belum ada tabel pada schema ini.</p>}
    </section>}

    {e.SelectedTable && <>
      <div className="explorer-table-heading">
        <div>
          <p className="connection-eyebrow">Tabel terpilih</p>
          <h2>{e.SelectedSchema}.{e.SelectedTable}</h2>
        </div>
        <span className="access-count">Halaman {e.Page}</span>
      </div>
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
      <nav className="explorer-pagination" aria-label="Navigasi halaman tabel">
        {e.Page > 1 && <a className="button-link pagination-arrow"
          aria-label="Halaman sebelumnya" title="Halaman sebelumnya"
          href={link("/connections/explore", { ...scope, page: e.Page - 1 })}>
          <span aria-hidden="true">←</span>
        </a>}
        <span>Halaman {e.Page}</span>
        {e.HasNextPage && <a className="button-link pagination-arrow"
          aria-label="Halaman berikutnya" title="Halaman berikutnya"
          href={link("/connections/explore", { ...scope, page: e.Page + 1 })}>
          <span aria-hidden="true">→</span>
        </a>}
      </nav>
    </>}
  </>;
}

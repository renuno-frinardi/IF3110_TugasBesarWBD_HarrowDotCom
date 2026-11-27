import { api } from "../api";
import { Action, Field, Form, link } from "../ui";
import { dbLink, pgOnly } from "../workspaceData";

export function ConnectionList({ rows, emptyMessage }) {
  if (!rows.length) return <p className="connection-list-empty">{emptyMessage}</p>;

  return <ul className="connection-list">
    {rows.map(row => (
      <li key={row.ID}>
        <a href={dbLink("/connections/view", row.ID)}>
          <span className="connection-list-name">
            <strong>{row.Name}</strong>
            <span>{row.DatabaseName} · {row.Host}:{row.Port}</span>
          </span>
          <span className={`connection-status connection-status--${row.IsActive ? "active" : "inactive"}`}>
            <span aria-hidden="true" />
            {row.IsActive ? "Aktif" : "Nonaktif"}
          </span>
        </a>
      </li>
    ))}
  </ul>;
}

export function Connections({ data: d, params, admin }) {
  return <div className="workspace-page">
    <div className="workspace-heading">
      <h1>Koneksi PostgreSQL</h1>
      {admin && <a className="button-link" href={link("/connections/new")}>Tambah koneksi</a>}
    </div>
    <section className="workspace-panel">
      <h2>Cari dan filter</h2>
      <Form className="workspace-form connection-filter" button="Terapkan filter" onSave={values => {
        window.location.hash = link("/connections", values).slice(1);
      }}>
        <Field name="search" label="Cari koneksi"
          defaultValue={params.get("search") || ""} />
        <Field name="status" label="Status" defaultValue={params.get("status") || ""}
          choices={[["", "Semua"], ["active", "Aktif"], ["inactive", "Nonaktif"]]} />
      </Form>
    </section>
    <section className="workspace-panel connections-catalog">
      <h2>Daftar koneksi</h2>
      <ConnectionList rows={pgOnly(d.postgres)} emptyMessage="Tidak ada koneksi yang cocok." />
    </section>
  </div>;
}

export function ConnectionDetail({ connection: c, id, admin, post }) {
  const connection = { id, source: "postgresql" };

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

export function ConnectionEditor({ connection: c, id, path, post, run }) {
  const connection = { id, source: "postgresql" };
  const editing = path.endsWith("/edit");
  return <div className="workspace-page connection-editor">
    <h1 className="workspace-heading">{editing ? "Edit koneksi" : "Tambah koneksi PostgreSQL"}</h1>
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
  </div>;
}

import { Action, Field, Form } from "../ui";
import { options, pgOnly } from "../workspaceData";

export function Access({ data: d, post }) {
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

function accessLabel(level) {
  if (level === "view_edit") return "View + Edit";
  if (level === "view_only") return "View";
  return level || "Tidak diketahui";
}

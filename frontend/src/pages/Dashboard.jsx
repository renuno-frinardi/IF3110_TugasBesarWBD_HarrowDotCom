import { link } from "../ui";
import { pgOnly } from "../workspaceData";
import { ConnectionList } from "./Connections";

const dashboardNumber = new Intl.NumberFormat("id-ID");

export function Dashboard({ data, session }) {
  const stats = data.stats || {};
  const connections = pgOnly(data.connections);
  const cards = [
    ["Koneksi yang bisa diakses", stats.Connections, "/connections"],
  ];
  if (session.is_admin) {
    cards.push(
      ["Users", stats.Users, "/users"],
      ["Groups", stats.Groups, "/groups"],
      ["Audit log", stats.AuditLogs, "/audit-logs"],
    );
  }

  return <div className="workspace-page dashboard">
    <section className="workspace-panel dashboard-hero">
      <div>
        <h1>Dashboard</h1>
        <p>Selamat datang, {session.username}.</p>
      </div>
      {session.is_admin && (
        <a className="button-link button-link--secondary" href={link("/connections/new")}>
          Tambah koneksi
        </a>
      )}
    </section>

    <ul className="dashboard-stats" aria-label="Ringkasan dashboard">
      {cards.map(([label, count, path]) => (
        <li key={path}>
          <a href={link(path)} className="dashboard-stat">
            <span>{label}</span>
            <strong>{count == null ? "—" : dashboardNumber.format(count)}</strong>
            <span className="dashboard-stat-arrow" aria-hidden="true">↗</span>
          </a>
        </li>
      ))}
    </ul>

    <section className="workspace-panel dashboard-connections">
      <h2>Koneksi terbaru</h2>
      <ConnectionList rows={connections} emptyMessage="Belum ada koneksi PostgreSQL yang bisa diakses." />
      <div className="workspace-panel-actions">
        <a className="button-link button-link--secondary" href={link("/connections")}>Lihat semua koneksi</a>
      </div>
    </section>
  </div>;
}

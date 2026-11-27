import { Access } from "./pages/Access";
import { AuditLogs } from "./pages/AuditLogs";
import { ConnectionDetail, ConnectionEditor, Connections } from "./pages/Connections";
import { Dashboard } from "./pages/Dashboard";
import { Explorer } from "./pages/Explorer";
import { Groups } from "./pages/Groups";
import { Users } from "./pages/Users";

export function Workspace({ path, params, data, session, run, post }) {
  const admin = session.is_admin;
  const id = params.get("id");
  const connection = data.connection || {};

  switch (path) {
    case "/":
      return <Dashboard data={data} session={session} />;
    case "/connections":
      return <Connections data={data} params={params} admin={admin} />;
    case "/connections/view":
      return <ConnectionDetail {...{ connection, id, admin, post }} />;
    case "/connections/new":
    case "/connections/edit":
      return <ConnectionEditor {...{ connection, id, path, post, run }} />;
    case "/connections/explore":
      return <Explorer {...{ data, id, post, run }} />;
    case "/users":
      return <Users data={data} post={post} />;
    case "/groups":
      return <Groups {...{ data, post, run }} />;
    case "/access":
      return <Access data={data} post={post} />;
    case "/audit-logs":
      return <AuditLogs data={data} />;
    default:
      return <p>Halaman tidak ditemukan.</p>;
  }
}

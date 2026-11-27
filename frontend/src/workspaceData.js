import { link } from "./ui";

export const options = (rows, label) =>
  (rows || []).map(row => [row.ID, row[label]]);

export function pgOnly(rows) {
  return (rows || []).filter(row => !["mariadb", "mysql"].includes(row.DBType));
}

export function dbLink(path, id, extra = {}) {
  return link(path, { id, source: "postgresql", ...extra });
}

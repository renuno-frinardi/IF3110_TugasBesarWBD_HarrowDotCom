import { Table } from "../ui";

export function AuditLogs({ data: d }) {
  return <>
    <h1>Audit Logs</h1>
    <Table rows={d.audit_logs} columns={[
      ["Waktu", "CreatedAt"], ["Actor", "ActorUsername"], ["Aksi", "Action"],
      ["Resource", "ResourceType"], ["ID", "ResourceID"], ["Metadata", "Metadata"],
    ]} />
  </>;
}

import { useState } from "react";
import { ActionDropdown } from "../ActionDropdown";
import { api } from "../api";
import { Action, Field, Form, Table, link } from "../ui";
import { dbLink } from "../workspaceData";

export function Explorer({ data: d, id, post, run }) {
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

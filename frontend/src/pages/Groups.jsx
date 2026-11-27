import { api } from "../api";
import { Action, Field, Form } from "../ui";
import { options } from "../workspaceData";

export function Groups({ data: d, post, run }) {
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

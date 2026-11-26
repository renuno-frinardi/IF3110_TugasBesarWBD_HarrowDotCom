import React, { useEffect, useState } from "react";
import { api } from "./api";
import { Action, Field, Form, Table, link } from "./ui";

function destination(path, params) {
  return { navigate: link(path, params).slice(1) };
}

const actionLabel = {
  submit: "Ajukan review",
  approve: "Approve",
  execute: "Execute",
  rollback: "Ajukan rollback",
  forward: "Ajukan forward",
};

export function MigrationScreen({ path, params, data, run, expire }) {
  const db = params.get("db") || data.database_id;
  const id = params.get("id");

  if (path === "/jobs") {
    return <Job initial={data} id={id} run={run} expire={expire} />;
  }

  if (path === "/history") {
    const hide = params.get("hide") === "1";
    return <>
      <h1>Migration history</h1>
      <a href={link("/migrations", { db })}>Kembali ke migrations</a>
      <label className="check">
        <input type="checkbox" checked={hide} onChange={e => {
          window.location.hash = link("/history",
            { db, hide: e.target.checked ? "1" : "0" }).slice(1);
        }} />
        Hide rollback
      </label>
      <Table rows={data.items} columns={[
        ["Execution", "execution_id"], ["Migration", "migration_id"],
        ["Revisi", "revision"], ["Aksi", "action"], ["Attempt", "attempt"],
        ["Status", "status"], ["User", "requested_by"], ["Waktu", "executed_at"],
        ["Membatalkan execution", "rollback_of"],
        ["SQL yang dijalankan", row => <pre>{row.executed_sql}</pre>],
        ["Job", row => row.job_id &&
          <a href={link("/jobs", { id: row.job_id })}>{row.job_id}</a>],
      ]} />
    </>;
  }

  if (path === "/migrations" || path === "/reviews") {
    const reviews = path === "/reviews";
    return <>
      <h1>{reviews ? "Menunggu review saya" : "Migrations"}</h1>
      {!reviews && <nav>
        <a href={link("/history", { db })}>Migration history</a>
        {data.can_create && <a href={link("/migration/new", { db })}>Buat migration</a>}
      </nav>}
      <Table rows={data.items} columns={[
        ["Nama", row => <a href={link("/migration", { id: row.id })}>{row.name}</a>],
        ["Revisi", "revision"], ["Aksi", "action"], ["Workflow", "workflow"],
      ]} />
    </>;
  }

  if (path === "/migration/new") {
    return <Draft db={db} run={run} />;
  }

  const allowed = data.allowed_actions || [];

  function act(action) {
    run(async () => {
      const result = await api(`/v2/migrations/${encodeURIComponent(id)}/${action}`, {
        method: "POST",
        json: { revision: data.revision },
      });

      if (action === "execute") return destination("/jobs", { id: result.job_id });
      if (action === "rollback" || action === "forward") {
        return destination("/migration", { id });
      }
      return {};
    });
  }

  return <>
    <nav>
      <a href={link("/migrations", { db })}>Daftar migration</a>
      <a href={link("/history", { db })}>History</a>
    </nav>
    <h1>{data.name}</h1>
    <p>Revisi {data.revision} · {data.action} · {data.workflow} · {data.mode}</p>

    {allowed.includes("edit") ? (
      <Draft db={db} migration={data} run={run} />
    ) : (
      <section>
        <h3>SQL up</h3><pre>{data.sql_up}</pre>
        <h3>{data.irreversible ? "Alasan irreversible" : "SQL down"}</h3>
        <pre>{data.irreversible ? data.irreversible_reason : data.sql_down}</pre>
      </section>
    )}

    <h2>Revisi</h2>
    <Table rows={data.revisions} columns={[
      ["Revisi", "revision"], ["SQL up", r => <pre>{r.sql_up}</pre>],
      ["SQL down", r => <pre>{r.sql_down || r.irreversible_reason}</pre>],
    ]} />

    <h2>Approval per group</h2>
    <Table rows={data.approvals} columns={[
      ["Group", "group_name"], ["Leader", r => r.leader_name || "Belum ditetapkan"],
      ["Status", "status"],
    ]} />

    <div className="actions">
      {Object.entries(actionLabel).filter(([key]) => allowed.includes(key))
        .map(([key, label]) => (
          <Action key={key} onClick={() => act(key)}
            confirmText={key === "execute" ? `Jalankan revisi ${data.revision}?` : undefined}>
            {label}
          </Action>
        ))}
      {data.latest_job_id &&
        <a href={link("/jobs", { id: data.latest_job_id })}>Pantau job terakhir</a>}
    </div>
  </>;
}

function Draft({ db, migration: m, run }) {
  const [irreversible, setIrreversible] = useState(m?.irreversible || false);
  const [mode, setMode] = useState(m?.mode || "normal");
  const [plan, setPlan] = useState(null);

  function save(values, operation) {
    run(async () => {
      if (operation === "explain" || operation === "analyze") {
        const result = await api("/connections/migrations/explain", {
          method: "POST",
          form: {
            source: "postgresql", database_id: db, title: values.name,
            sql: values.sql_up, analyze: String(operation === "analyze"),
          },
        });
        const explain = result.explain_result;
        setPlan(explain?.PlanLines?.join("\n") || explain?.Message || "Tidak ada plan.");
        return { notice: "Pemeriksaan query selesai." };
      }

      const payload = {
        name: values.name.trim(),
        up_sql: values.sql_up,
        sql_down: irreversible ? null : values.sql_down,
        irreversible,
        irreversible_reason: irreversible ? values.irreversible_reason.trim() : null,
        mode,
        batch_config: mode === "batch" ? {
          cursor_column: values.cursor_column.trim(),
          batch_size: Number(values.batch_size),
        } : null,
      };

      if (m) payload.revision = m.revision;
      const endpoint = m
        ? `/v2/migrations/${m.id}`
        : `/v2/databases/${encodeURIComponent(db)}/migrations`;
      const result = await api(endpoint, { method: m ? "PATCH" : "POST", json: payload });
      return destination("/migration", { id: result.id });
    });
  }

  return <>
    <Form title={m ? "Edit draft" : "Buat migration"} onSave={save} button="Simpan draft">
      <Field name="name" label="Nama migration" defaultValue={m?.name} required />
      <label>SQL up
        <textarea name="sql_up" defaultValue={m?.sql_up} required spellCheck="false" />
      </label>
      <label className="check">
        <input type="checkbox" checked={irreversible}
          onChange={e => setIrreversible(e.target.checked)} />
        Irreversible
      </label>
      {irreversible ? (
        <Field name="irreversible_reason" label="Alasan irreversible"
          defaultValue={m?.irreversible_reason} required />
      ) : (
        <label>SQL down
          <textarea name="sql_down" defaultValue={m?.sql_down} required spellCheck="false" />
        </label>
      )}
      <Field name="mode" label="Mode eksekusi" value={mode}
        onChange={e => setMode(e.target.value)}
        choices={[["normal", "Normal"], ["batch", "Per batch"]]} />
      {mode === "batch" && <>
        <Field name="cursor_column" label="Kolom cursor unik dan stabil"
          defaultValue={m?.batch_config?.cursor_column || "id"} required />
        <Field name="batch_size" label="Ukuran batch" type="number" min="1" step="1"
          defaultValue={m?.batch_config?.batch_size || 500} required />
      </>}
      <div className="actions">
        <button type="submit" value="explain" formNoValidate>EXPLAIN SQL up</button>
        <button type="submit" value="analyze" formNoValidate>EXPLAIN ANALYZE SQL up</button>
      </div>
    </Form>
    {plan && <section><h3>Query plan</h3><pre>{plan}</pre></section>}
  </>;
}

function Job({ id, initial, run, expire }) {
  const [job, setJob] = useState(initial);
  const [pollError, setPollError] = useState("");
  const [retry, setRetry] = useState(0);
  const active = job.status === "pending" || job.status === "running";

  useEffect(() => {
    if (!active) return;
    const controller = new AbortController();
    let timer;

    async function poll() {
      try {
        const next = await api(`/v2/jobs/${encodeURIComponent(id)}`, {
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        setJob(next);
        setPollError("");
        if (next.status === "pending" || next.status === "running") {
          timer = setTimeout(poll, 1500);
        }
      } catch (e) {
        if (controller.signal.aborted) return;
        if (e.status === 401) expire();
        else setPollError(e.message);
      }
    }

    timer = setTimeout(poll, 1500);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [id, active, retry]);

  function recover(action) {
    run(async () => {
      const result = await api(`/v2/jobs/${encodeURIComponent(id)}/${action}`, {
        method: "POST", json: {},
      });
      return destination("/jobs", { id: result.job_id });
    });
  }

  return <>
    <h1>Job {id}</h1>
    <p role="status">Status: <strong>{job.status}</strong></p>
    <dl>
      <dt>Batch committed</dt><dd>{job.committed_batches ?? 0}</dd>
      <dt>Row committed</dt><dd>{job.committed_rows ?? 0}</dd>
      <dt>Cursor terakhir</dt><dd>{job.checkpoint?.cursor ?? "—"}</dd>
    </dl>
    {(job.error_message || job.error) && <p role="alert">{job.error_message || job.error}</p>}
    {pollError && <p role="alert">{pollError}{" "}
      <Action onClick={() => setRetry(n => n + 1)}>Coba pantau lagi</Action>
    </p>}
    {job.allowed_actions?.includes("resume") && (
      <Action onClick={() => recover("resume")}>Lanjutkan dari checkpoint</Action>
    )}
    {job.allowed_actions?.includes("cancel-batches") && (
      <Action confirmText="Ajukan pembatalan batch yang sudah committed?"
        onClick={() => recover("cancel-batches")}>Batalkan batch committed</Action>
    )}
    <nav>
      <a href={link("/migration", { id: job.migration_id })}>Detail migration</a>
      <a href={link("/history", { db: job.database_id })}>Lihat history</a>
    </nav>
  </>;
}
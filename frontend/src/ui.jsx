import React from "react"

export function link(path, params = {}) {
  const query = new URLSearchParams(params).toString();
  return `#${path}${query ? `?${query}` : ""}`;
}

export function Field({ name, label = name, choices, ...props }) {
  return (
    <label>
      {label}
      {choices ? (
        <span className="select-field">
          <select name={name} {...props}>
            {choices.map(([value, text]) => (
              <option key={value} value={value}>{text}</option>
            ))}
          </select>
        </span>
      ) : (
        <input name={name} {...props} />
      )}
    </label>
  );
}

export function Form({ className, title, onSave, button = "Simpan", children }) {
  return (
    <form className={className} onSubmit={event => {
      event.preventDefault();
      const body = Object.fromEntries(new FormData(event.currentTarget));
      onSave(body, event.nativeEvent.submitter?.value);
    }}>
      {title && <h3>{title}</h3>}
      {children}
      <button type="submit">{button}</button>
    </form>
  );
}

export function Table({ rows = [], columns }) {
  if (!rows?.length) return <p>Belum ada data.</p>;

  return (
    <div className="table-scroll" tabIndex="0" aria-label="Tabel data">
      <table>
        <thead>
          <tr>{columns.map(([title]) => <th key={title}>{title}</th>)}</tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr key={row.execution_id ?? row.id ?? row.ID ?? index}>
              {columns.map(([title, read]) => (
                <td key={title}>
                  {typeof read === "function" ? read(row, index) : String(row[read] ?? "—")}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function Action({ children, onClick, confirmText }) {
  return (
    <button type="button" onClick={() => {
      if (!confirmText || window.confirm(confirmText)) onClick();
    }}>
      {children}
    </button>
  );
}

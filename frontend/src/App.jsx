import React, { useEffect, useRef, useState } from "react";
import { api, setCSRF } from "./api";
import { Field, Form, link } from "./ui";
import { Workspace } from "./Workspace";
import { MigrationScreen } from "./MigrationScreen";
import "./App.css";

function currentRoute() {
  return window.location.hash.slice(1) || "/";
}

function readRoute(raw) {
  const url = new URL(raw, window.location.origin);
  return { path: url.pathname, params: url.searchParams };
}

const migrationPages = new Set([
  "/migrations", "/migration", "/migration/new", "/reviews", "/history", "/jobs",
]);

export default function App() {
  const [route, setRoute] = useState(currentRoute);
  const [session, setSession] = useState(null);
  const [page, setPage] = useState(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [menuOpen, setMenuOpen] = useState(false);
  const submitting = useRef(false);
  const { path, params } = readRoute(route);
  const publicPage = path === "/login" || path === "/signup";

  useEffect(() => {
    function changed() {
      setMenuOpen(false);
      setRoute(currentRoute());
      setError("");
      setNotice("");
    }
    window.addEventListener("hashchange", changed);
    return () => window.removeEventListener("hashchange", changed);
  }, []);

  function expire() {
    setCSRF("");
    setSession(null);
    setPage(null);
    window.location.hash = "/login";
  }

  useEffect(() => {
    const controller = new AbortController();
    const options = { signal: controller.signal };
    setPage(null);
    setError("");

    async function load() {
      if (publicPage) {
        setPage({ route });
        return;
      }

      const user = await api("/session", options);
      if (controller.signal.aborted) return;
      setSession(user);
      setCSRF(user.csrf_token);

      const { path: screen, params: q } = readRoute(route);
      const id = encodeURIComponent(q.get("id") || "");
      const db = encodeURIComponent(q.get("db") || "");
      const endpoints = {
        "/migrations": `/v2/databases/${db}/migrations`,
        "/migration": `/v2/migrations/${id}`,
        "/reviews": "/v2/reviews",
        "/jobs": `/v2/jobs/${id}`,
        "/history": `/v2/databases/${db}/history?hide_rollback=${q.get("hide") === "1"}`,
      };

      const data = screen === "/migration/new" ? {} : await api(endpoints[screen] || route, options);

      if (!controller.signal.aborted) setPage({ route, data });
    }

    load().catch(e => {
      if (controller.signal.aborted) return;
      if (e.status === 401) expire();
      else setError(e.message);
    });

    return () => controller.abort();
  }, [route, refresh, publicPage]);

  async function run(operation) {
    if (submitting.current) return;
    submitting.current = true;
    setBusy(true);
    setError("");
    setNotice("");
    const start = currentRoute();

    try {
      const result = await operation();
      if (currentRoute() !== start) return;
      if (result?.navigate && result.navigate !== start) {
        window.location.hash = result.navigate;
      } else if (result?.notice) {
        setNotice(result.notice);
      } else {
        setRefresh(n => n + 1);
      }
    } catch (e) {
      if (e.status === 401 && !publicPage) expire();
      else if (currentRoute() === start) setError(e.message);
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  }

  function post(endpoint, body) {
    return run(() => api(endpoint, { method: "POST", form: body }));
  }

  const ready = page?.route === route;

  return (
    <>
      <header>
        <a href={link("/")} className="brand">Harrow</a>

        {!publicPage && session && (
          <>
            <button
              type="button"
              className="menu-toggle"
              aria-controls="main-nav main-logout"
              aria-expanded={menuOpen}
              onClick={() => setMenuOpen(open => !open)}
            >
              Menu
            </button>

            <nav
              id="main-nav"
              aria-label="Navigasi utama"
              data-open={menuOpen}
              onClick={event => {
                if (event.target.closest("a")) setMenuOpen(false);
              }}
            >
              <a href={link("/")}>Dashboard</a>
              <a href={link("/connections")}>Koneksi</a>
              <a href={link("/reviews")}>Review Saya</a>

              {session.is_admin && (
                <>
                  <a href={link("/users")}>Users</a>
                  <a href={link("/groups")}>Groups</a>
                  <a href={link("/access")}>Akses</a>
                  <a href={link("/audit-logs")}>Audit Logs</a>
                </>
              )}
            </nav>
            <button
              type="button"
              id="main-logout"
              className="logout-button"
              disabled={busy}
              onClick={() => run(async () => {
                await api("/logout", { method: "POST", form: {} });
                setSession(null);
                setCSRF("");
                return { navigate: "/login" };
              })}
            >
              Keluar
            </button>
          </>
        )}
      </header>

      <main>
        {error && <p role="alert">{error}</p>}
        {notice && <p role="status">{notice}</p>}
        {!ready && !error && <p role="status">Memuat…</p>}
        {!ready && error && (
          <button onClick={() => setRefresh(n => n + 1)}>Coba lagi</button>
        )}

        {ready && (
          <fieldset disabled={busy} className="screen" key={route}>
            {publicPage ? (
              <>
                <h1>{path === "/login" ? "Masuk" : "Buat akun"}</h1>
                <Form onSave={body => post(path, body)}
                      button={path === "/login" ? "Masuk" : "Daftar"}>
                  <Field name="username" required autoComplete="username" />
                  {path === "/signup" && <Field name="email" type="email" />}
                  <Field name="password" type="password" required
                    autoComplete={path === "/login" ? "current-password" : "new-password"} />
                  {path === "/signup" && (
                    <Field name="confirm_password" label="Konfirmasi password"
                      type="password" required autoComplete="new-password" />
                  )}
                </Form>
                <a href={link(path === "/login" ? "/signup" : "/login")}>
                  {path === "/login" ? "Buat akun" : "Kembali ke login"}
                </a>
              </>
            ) : migrationPages.has(path) ? (
              <MigrationScreen {...{ path, params, run, expire }}
                data={page.data} />
            ) : (
              <Workspace {...{ path, params, session, run, post }}
                data={page.data} />
            )}
          </fieldset>
        )}
      </main>
    </>
  );
}
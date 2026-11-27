import { useCallback, useEffect, useRef, useState } from "react";
import { api, setCSRF } from "./api";
import { Field, Form, link } from "./ui";
import { Workspace } from "./Workspace";
import { MigrationScreen } from "./MigrationScreen";
import logo from "./assets/logo.png";
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

  const expire = useCallback(() => {
    setCSRF("");
    setPage(null);
    setSession(null);
    window.location.hash = "/login";
  }, []);

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
  }, [route, refresh, publicPage, expire]);

  async function run(operation, onError = setError) {
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
      else if (currentRoute() === start) onError(e.message);
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  }

  function post(endpoint, body) {
    return run(() => api(endpoint, { method: "POST", form: body }));
  }

  const ready = page?.route === route && (publicPage || session !== null);

  return (
    <>
      {!publicPage && session && (
        <header>
          <a href={link("/")} className="brand">Harrow</a>
          <button
            type="button"
            className="menu-toggle"
            aria-controls="main-menu"
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen(open => !open)}
          >
            Menu
          </button>

          <div id="main-menu" data-open={menuOpen}>
            <nav
              id="main-nav"
              aria-label="Navigasi utama"
              onClick={event => {
                if (event.target.closest("a")) setMenuOpen(false);
              }}
            >
              <a href={link("/")} aria-current={path === "/" ? "page" : undefined}>
                Dashboard
              </a>
              <a href={link("/connections")}
                aria-current={path.startsWith("/connections") ? "page" : undefined}>
                Koneksi
              </a>
              <a href={link("/reviews")}
                aria-current={path === "/reviews" ? "page" : undefined}>
                Review Saya
              </a>

              {session.is_admin && (
                <>
                  <a href={link("/users")}
                    aria-current={path === "/users" ? "page" : undefined}>Users</a>
                  <a href={link("/groups")}
                    aria-current={path === "/groups" ? "page" : undefined}>Groups</a>
                  <a href={link("/access")}
                    aria-current={path === "/access" ? "page" : undefined}>Akses</a>
                  <a href={link("/audit-logs")}
                    aria-current={path === "/audit-logs" ? "page" : undefined}>
                    Audit Logs
                  </a>
                </>
              )}
            </nav>
            <button
              type="button"
              className="logout-button"
              disabled={busy}
              onClick={() => run(async () => {
                await api("/logout", { method: "POST", form: {} });
                expire();
              })}
            >
              Keluar
            </button>
          </div>
        </header>
      )}

      <main>
        {error && <p role="alert">{error}</p>}
        {notice && <p role="status">{notice}</p>}
        {!ready && !error && (
          <div className="page-loader" role="status" aria-label="Memuat halaman">
            <span /><span /><span />
          </div>
        )}
        {!ready && error && (
          <button onClick={() => setRefresh(n => n + 1)}>Coba lagi</button>
        )}

        {ready && (
          <fieldset disabled={busy} className="screen" key={route}>
            {publicPage ? (
              <div className="auth-page">
                <section className="auth-card">
                  <img className="auth-logo" src={logo} alt="HarrowDotCom" />
                  <p className="auth-eyebrow">DATABASE WORKSPACE</p>
                  <h1>{path === "/login" ? "Selamat datang kembali" : "Buat akun"}</h1>
                  <p className="auth-description">
                    {path === "/login"
                      ? "Masuk untuk mengelola koneksi dan migrasi database Anda."
                      : "Daftar untuk mulai mengelola koneksi dan migrasi database Anda."}
                  </p>
                  <Form className="auth-form" onSave={body => post(path, body)}
                        button={path === "/login" ? "Masuk" : "Buat akun"}>
                    <Field name="username" label="Nama pengguna" required autoComplete="username" />
                    {path === "/signup" && <Field name="email" label="Email" type="email" />}
                    <Field name="password" label="Kata sandi" type="password" required
                      autoComplete={path === "/login" ? "current-password" : "new-password"} />
                    {path === "/signup" && (
                      <Field name="confirm_password" label="Konfirmasi kata sandi"
                        type="password" required autoComplete="new-password" />
                    )}
                  </Form>
                  <div className="auth-switch">
                    <p>
                      {path === "/login" ? "Belum punya akun?" : "Sudah punya akun?"}
                    </p>
                    <a className="button-link button-link--secondary auth-switch-button"
                      href={link(path === "/login" ? "/signup" : "/login")}>
                      {path === "/login" ? "Buat akun" : "Masuk"}
                    </a>
                  </div>
                </section>
              </div>
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

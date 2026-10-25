package auth_test

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
)

func newSession() *auth.Session {
	return &auth.Session{CSRFSecret: []byte("0123456789abcdef0123456789abcdef")}
}

func TestCSRFToken(t *testing.T) {
	s := newSession()
	tok := auth.CSRFToken(s)
	if tok == "" || tok != auth.CSRFToken(s) {
		t.Fatal("token harus tidak kosong dan deterministik")
	}
	if auth.CSRFToken(nil) != "" || auth.CSRFToken(&auth.Session{}) != "" {
		t.Fatal("session nil/tanpa secret harus menghasilkan token kosong")
	}
	other := &auth.Session{CSRFSecret: []byte("another-secret-another-secret-12")}
	if auth.CSRFToken(other) == tok {
		t.Fatal("secret berbeda harus menghasilkan token berbeda")
	}
}

func TestValidateCSRFToken(t *testing.T) {
	s := newSession()
	tok := auth.CSRFToken(s)
	tests := []struct {
		name     string
		sess     *auth.Session
		provided string
		want     bool
	}{
		{"valid", s, tok, true},
		{"valid dengan spasi", s, "  " + tok + " ", true},
		{"salah", s, "deadbeef", false},
		{"kosong", s, "", false},
		{"session nil", nil, tok, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := auth.ValidateCSRFToken(tc.sess, tc.provided); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIsStateChangingMethod(t *testing.T) {
	for m, want := range map[string]bool{
		"GET": false, "HEAD": false, "OPTIONS": false,
		"POST": true, "put": true, "PATCH": true, "DELETE": true,
	} {
		if got := auth.IsStateChangingMethod(m); got != want {
			t.Errorf("%s: got %v want %v", m, got, want)
		}
	}
}

func TestExtractAndValidateRequestCSRF(t *testing.T) {
	s := newSession()
	tok := auth.CSRFToken(s)

	t.Run("GET tidak perlu token", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if err := auth.ValidateRequestCSRF(r, s); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("POST via header", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set(auth.CSRFHeaderName, tok)
		if err := auth.ValidateRequestCSRF(r, s); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("POST via form", func(t *testing.T) {
		form := url.Values{auth.CSRFFormField: {tok}}
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := auth.ValidateRequestCSRF(r, s); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("POST tanpa token ditolak", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if err := auth.ValidateRequestCSRF(r, s); err != auth.ErrInvalidCSRFToken {
			t.Fatalf("got %v", err)
		}
	})
}

func TestRedactString(t *testing.T) {
	tests := []struct {
		name, in string
		absent   string
	}{
		{"password kv", "login password=Rahasia123 ok", "Rahasia123"},
		{"token kv", "token: abcdef", "abcdef"},
		{"argon hash", "hash $argon2id$v=19$m=65536,t=1,p=4$salt$hash end", "$argon2id$"},
		{"hex 64", "x " + strings.Repeat("ab", 32) + " y", strings.Repeat("ab", 32)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := auth.RedactString(tc.in)
			if strings.Contains(out, tc.absent) {
				t.Fatalf("masih bocor: %q", out)
			}
		})
	}
	if got := auth.RedactString("halo dunia"); got != "halo dunia" {
		t.Fatalf("teks aman tidak boleh berubah: %q", got)
	}
}

func TestRedactingWriter(t *testing.T) {
	var buf bytes.Buffer
	w := auth.NewRedactingWriter(&buf)
	msg := []byte("password=hunter2")
	n, err := w.Write(msg)
	if err != nil || n != len(msg) {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if strings.Contains(buf.String(), "hunter2") {
		t.Fatalf("bocor: %q", buf.String())
	}
}

func TestSanitizeURL(t *testing.T) {
	if auth.SanitizeURL(nil) != "" {
		t.Fatal("nil harus kosong")
	}
	u, _ := url.Parse("/x?id=1&password=abc&csrf_token=zzz")
	out := auth.SanitizeURL(u)
	if strings.Contains(out, "abc") || strings.Contains(out, "zzz") || !strings.Contains(out, "id=1") {
		t.Fatalf("hasil: %q", out)
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	var logBuf bytes.Buffer
	called := false
	h := auth.SecurityHeadersMiddleware(log.New(&logBuf, "", 0), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/a?token=secret", nil))

	if !called {
		t.Fatal("handler berikutnya tidak dipanggil")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "style-src 'self' 'unsafe-inline'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP: %q", csp)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("header keamanan kurang")
	}
	if strings.Contains(logBuf.String(), "secret") {
		t.Fatalf("log bocor: %q", logBuf.String())
	}
}

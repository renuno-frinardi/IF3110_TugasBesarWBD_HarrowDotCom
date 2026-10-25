package auth

// Import pkg
import (
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Dffinisi variabel
var (
	kvSecretPattern = regexp.MustCompile(`(?i)(password|passwd|secret|token|session_id|harrow_session|csrf_token)\s*([=:])\s*([^\s&",;]+)`)
	hashPattern     = regexp.MustCompile(`\$(pbkdf2-sha256|argon2id)\$[^\s"']+`)
	hex64Pattern    = regexp.MustCompile(`\b[0-9a-fA-F]{64}\b`)
)

// Redaksi string untuk hal sensitif
func RedactString(input string) string {
	out := kvSecretPattern.ReplaceAllString(input, "$1$2[REDACTED]")
	out = hashPattern.ReplaceAllString(out, "[REDACTED_HASH]")
	out = hex64Pattern.ReplaceAllString(out, "[REDACTED_TOKEN]")
	return out
}

// Wrapper untuk menghilangkan informasi sensitif
type RedactingWriter struct {
	dst io.Writer
}

func NewRedactingWriter(dst io.Writer) *RedactingWriter {
	return &RedactingWriter{dst: dst}
}

func (w *RedactingWriter) Write(p []byte) (int, error) {
	redacted := RedactString(string(p))
	_, err := w.dst.Write([]byte(redacted))
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// Menghilangan parameter query sebelum melakukan logging
func SanitizeURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	cp := *u
	if cp.RawQuery != "" {
		q := cp.Query()
		for k := range q {
			lower := strings.ToLower(k)
			if strings.Contains(lower, "password") ||
				strings.Contains(lower, "token") ||
				strings.Contains(lower, "session") ||
				strings.Contains(lower, "secret") {
				q.Set(k, "[REDACTED]")
			}
		}
		cp.RawQuery = q.Encode()
	}
	return cp.RequestURI()
}

// Setup CSP, X-Content-Type-Options, X-Frame-Options, dan log request yang sudah diredaksi
func SecurityHeadersMiddleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		if logger != nil {
			logger.Printf("%s %s", r.Method, SanitizeURL(r.URL))
		}
		next.ServeHTTP(w, r)
	})
}

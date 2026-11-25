package telemetry

import (
	"io"
	"regexp"
	"strings"
)

var (
	dsnPasswordPattern = regexp.MustCompile(`(?i)\b(postgres(?:ql)?|mysql|mariadb)://([^:/@\s]+):([^@\s]+)@`)
	kvSecretPattern    = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|session_id|harrow_session|csrf_token|encryption_key|api_key)\s*([=:])\s*([^\s&",;']+)`)
	bearerPattern      = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/-]+=*`)
	hashPattern        = regexp.MustCompile(`\$(pbkdf2-sha256|argon2id)\$[^\s"']+`)
	hex64Pattern       = regexp.MustCompile(`\b[0-9a-fA-F]{64}\b`)
	sqlPasswordLiteral = regexp.MustCompile(`(?i)(PASSWORD\s*['"=]\s*)('[^']*'|[^\s;)]+)`)
)

var safeExactKeys = map[string]bool{
	"trace_id":       true,
	"span_id":        true,
	"job_id":         true,
	"correlation_id": true,
	"content_hash":   true,
}

func IsSensitiveKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if safeExactKeys[lower] {
		return false
	}
	for _, marker := range []string{
		"password", "passwd", "pwd", "secret", "token",
		"session", "cookie", "csrf", "encryption_key",
		"authorization", "credential", "ciphertext", "dsn", "database_url",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func RedactString(input string) string {
	if input == "" {
		return ""
	}
	out := dsnPasswordPattern.ReplaceAllString(input, "$1://$2:[REDACTED]@")
	out = kvSecretPattern.ReplaceAllString(out, "$1$2[REDACTED]")
	out = bearerPattern.ReplaceAllString(out, "Bearer [REDACTED]")
	out = sqlPasswordLiteral.ReplaceAllString(out, "${1}'[REDACTED]'")
	out = hashPattern.ReplaceAllString(out, "[REDACTED_HASH]")
	out = hex64Pattern.ReplaceAllString(out, "[REDACTED_TOKEN]")
	return out
}

func RedactParams(params []any) []any {
	if len(params) == 0 {
		return nil
	}
	sanitized := make([]any, len(params))
	for i, p := range params {
		switch v := p.(type) {
		case string:
			sanitized[i] = RedactString(v)
		case []byte:
			sanitized[i] = "[REDACTED_BYTES]"
		default:
			sanitized[i] = p
		}
	}
	return sanitized
}

type RedactingWriter struct {
	dst io.Writer
}

func NewRedactingWriter(dst io.Writer) *RedactingWriter {
	return &RedactingWriter{dst: dst}
}

func (w *RedactingWriter) Write(p []byte) (int, error) {
	scrubbed := RedactString(string(p))
	if _, err := w.dst.Write([]byte(scrubbed)); err != nil {
		return 0, err
	}
	return len(p), nil
}

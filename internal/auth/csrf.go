package auth

// Import pkg
import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// Setup error msg
var ErrInvalidCSRFToken = errors.New("invalid or missing CSRF token")

// Definisii konstanta
const (
	CSRFFormField  = "csrf_token"
	CSRFHeaderName = "X-CSRF-Token"
	csrfContextTag = "harrow-csrf-v1"
)

// Mendapatkan session-bound CSRF token dari session CSRFSecret
func CSRFToken(sess *Session) string {
	if sess == nil || len(sess.CSRFSecret) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, sess.CSRFSecret)
	_, _ = mac.Write([]byte(csrfContextTag))
	return hex.EncodeToString(mac.Sum(nil))
}

// Membandingkan CSRF token terhadap token dari suatu sesi
func ValidateCSRFToken(sess *Session, provided string) bool {
	expected := CSRFToken(sess)
	if expected == "" || strings.TrimSpace(provided) == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(strings.TrimSpace(provided))) == 1
}

// Membaca CSRF token dari header X-CSRF-Token atau kolom csrf_token
func ExtractCSRFToken(r *http.Request) string {
	if hdr := strings.TrimSpace(r.Header.Get(CSRFHeaderName)); hdr != "" {
		return hdr
	}
	_ = r.ParseForm()
	return strings.TrimSpace(r.FormValue(CSRFFormField))
}

// Mengembalikna nilai benar untuk metode HTTP yang memutasi state
func IsStateChangingMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// Validasi token CSRF untuk seluruh metode HTTP yang memutasi state
func ValidateRequestCSRF(r *http.Request, sess *Session) error {
	if !IsStateChangingMethod(r.Method) {
		return nil
	}
	token := ExtractCSRFToken(r)
	if !ValidateCSRFToken(sess, token) {
		return ErrInvalidCSRFToken
	}
	return nil
}

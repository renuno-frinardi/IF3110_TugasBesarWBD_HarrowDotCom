package auth

// Import pkg
import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Setup error msg
var (
	ErrNoSession          = errors.New("session not found")
	ErrSessionRevoked     = errors.New("session has been revoked")
	ErrSessionIdleExpired = errors.New("session idle timeout exceeded")
	ErrSessionAbsExpired  = errors.New("session absolute expiration exceeded")
)

// Konfigurasi sesi dan atribut cookies
type SessionConfig struct {
	CookieName       string
	CookieSecure     bool
	IdleTimeout      time.Duration
	AbsoluteTimeout  time.Duration
	RotationInterval time.Duration
	GracePeriod      time.Duration
}

// Mengembalikan parameter sesi default (idle 30m, absolute 8h, rotation 10m, grace 30s),
// overridable melalui .env
func DefaultSessionConfig() SessionConfig {
	secure := strings.EqualFold(os.Getenv("APP_ENV"), "production") ||
		strings.EqualFold(os.Getenv("COOKIE_SECURE"), "true") ||
		os.Getenv("COOKIE_SECURE") == "1"

	cookieName := strings.TrimSpace(os.Getenv("SESSION_COOKIE_NAME"))
	if cookieName == "" {
		cookieName = "harrow_session"
	}

	return SessionConfig{
		CookieName:       cookieName,
		CookieSecure:     secure,
		IdleTimeout:      envDurationOrDefault("SESSION_IDLE_TIMEOUT", 30*time.Minute),
		AbsoluteTimeout:  envDurationOrDefault("SESSION_ABSOLUTE_TIMEOUT", 8*time.Hour),
		RotationInterval: envDurationOrDefault("SESSION_ROTATION_INTERVAL", 10*time.Minute),
		GracePeriod:      envDurationOrDefault("SESSION_GRACE_PERIOD", 30*time.Second),
	}
}

// Membaca durasi dari .env dan fallbackk jika tidak didefinisikan
func envDurationOrDefault(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// Representasi struct untuk sesi
type Session struct {
	ID             int64      `json:"id"`
	IDHash         []byte     `json:"-"`
	UserID         int64      `json:"user_id"`
	CreatedAt      time.Time  `json:"created_at"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	RotatedAt      time.Time  `json:"rotated_at"`
	PrevIDHash     []byte     `json:"-"`
	PrevValidUntil *time.Time `json:"prev_valid_until,omitempty"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	CSRFSecret     []byte     `json:"-"`
	RawToken       string     `json:"-"`
}

// Fungsi untuk mencegah token leak
func (s Session) String() string {
	return fmt.Sprintf("Session{ID:%d, UserID:%d, CreatedAt:%s}", s.ID, s.UserID, s.CreatedAt.Format(time.RFC3339))
}

// Menyimpan session user
type SessionStore interface {
	Insert(ctx context.Context, s *Session) error
	GetByTokenHash(ctx context.Context, tokenHash []byte) (*Session, bool, error)
	UpdateLastSeen(ctx context.Context, sessionID int64, lastSeen time.Time) error
	Rotate(ctx context.Context, sessionID int64, newHash, prevHash []byte, rotatedAt, prevValidUntil time.Time) error
	RevokeByID(ctx context.Context, sessionID int64, revokedAt time.Time) error
	RevokeByUserID(ctx context.Context, userID int64, revokedAt time.Time) error
}

// Mendifinisikan tipe session manager
type SessionManager struct {
	store   SessionStore
	cfg     SessionConfig
	NowFunc func() time.Time
}

func NewSessionManager(store SessionStore, cfg SessionConfig) *SessionManager {
	return &SessionManager{
		store:   store,
		cfg:     cfg,
		NowFunc: time.Now,
	}
}

func (m *SessionManager) Config() SessionConfig {
	return m.cfg
}

// Melakukan generasi token
func GenerateOpaqueToken() (rawToken string, hash []byte, err error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", nil, fmt.Errorf("generate random session token: %w", err)
	}
	rawToken = hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(rawToken))
	return rawToken, sum[:], nil
}

// Komputasi token SHA256
func HashToken(rawToken string) []byte {
	sum := sha256.Sum256([]byte(rawToken))
	return sum[:]
}

// Membuat sesi baru untuk user
func (m *SessionManager) CreateSession(ctx context.Context, userID int64) (*Session, error) {
	rawToken, idHash, err := GenerateOpaqueToken()
	if err != nil {
		return nil, err
	}

	csrfSecret := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, csrfSecret); err != nil {
		return nil, fmt.Errorf("generate csrf secret: %w", err)
	}

	now := m.NowFunc().UTC()
	sess := &Session{
		IDHash:     idHash,
		UserID:     userID,
		CreatedAt:  now,
		LastSeenAt: now,
		RotatedAt:  now,
		CSRFSecret: csrfSecret,
		RawToken:   rawToken,
	}

	if err := m.store.Insert(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// Validasi sebuah sesi token untuk mengecek revocation, ekspirasi, timeout, etc
func (m *SessionManager) ValidateAndTouch(ctx context.Context, rawToken string) (sess *Session, newCookieToken string, err error) {
	if strings.TrimSpace(rawToken) == "" {
		return nil, "", ErrNoSession
	}

	tokenHash := HashToken(rawToken)
	sess, matchedPrev, err := m.store.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, "", err
	}
	if sess == nil {
		return nil, "", ErrNoSession
	}

	now := m.NowFunc().UTC()

	// Check revocation
	if sess.RevokedAt != nil {
		return nil, "", ErrSessionRevoked
	}

	// Check expiration
	if now.Sub(sess.CreatedAt) > m.cfg.AbsoluteTimeout {
		_ = m.store.RevokeByID(ctx, sess.ID, now)
		return nil, "", ErrSessionAbsExpired
	}

	// Check idle timeout
	if now.Sub(sess.LastSeenAt) > m.cfg.IdleTimeout {
		_ = m.store.RevokeByID(ctx, sess.ID, now)
		return nil, "", ErrSessionIdleExpired
	}

	// Grace period apabila token sebelumnya match
	if matchedPrev {
		if sess.PrevValidUntil == nil || now.After(*sess.PrevValidUntil) {
			return nil, "", ErrNoSession
		}
		sess.LastSeenAt = now
		if err := m.store.UpdateLastSeen(ctx, sess.ID, now); err != nil {
			return nil, "", err
		}
		return sess, "", nil
	}

	// Check secara periodik
	if now.Sub(sess.RotatedAt) >= m.cfg.RotationInterval {
		rotatedToken, err := m.rotateInternal(ctx, sess, now)
		if err != nil {
			return nil, "", err
		}
		return sess, rotatedToken, nil
	}

	// Mengecek last seen
	sess.LastSeenAt = now
	if err := m.store.UpdateLastSeen(ctx, sess.ID, now); err != nil {
		return nil, "", err
	}
	return sess, "", nil
}

// Melakukan rotasi langsung sesi
func (m *SessionManager) ForceRotate(ctx context.Context, sess *Session) (string, error) {
	now := m.NowFunc().UTC()
	if now.Sub(sess.CreatedAt) > m.cfg.AbsoluteTimeout {
		_ = m.store.RevokeByID(ctx, sess.ID, now)
		return "", ErrSessionAbsExpired
	}
	return m.rotateInternal(ctx, sess, now)
}

func (m *SessionManager) rotateInternal(ctx context.Context, sess *Session, now time.Time) (string, error) {
	newRawToken, newHash, err := GenerateOpaqueToken()
	if err != nil {
		return "", err
	}

	prevHash := make([]byte, len(sess.IDHash))
	copy(prevHash, sess.IDHash)
	validUntil := now.Add(m.cfg.GracePeriod)

	if err := m.store.Rotate(ctx, sess.ID, newHash, prevHash, now, validUntil); err != nil {
		return "", err
	}

	sess.PrevIDHash = prevHash
	sess.PrevValidUntil = &validUntil
	sess.IDHash = newHash
	sess.RotatedAt = now
	sess.LastSeenAt = now
	sess.RawToken = newRawToken
	return newRawToken, nil
}

// Revokasi sebauh sesi melalui token yang dimilikinya
func (m *SessionManager) Revoke(ctx context.Context, rawToken string) error {
	if strings.TrimSpace(rawToken) == "" {
		return nil
	}
	tokenHash := HashToken(rawToken)
	sess, _, err := m.store.GetByTokenHash(ctx, tokenHash)
	if err != nil || sess == nil {
		return err
	}
	now := m.NowFunc().UTC()
	return m.store.RevokeByID(ctx, sess.ID, now)
}

// Revoke seluruh sesi dari suatu user
func (m *SessionManager) RevokeUserSessions(ctx context.Context, userID int64) error {
	now := m.NowFunc().UTC()
	return m.store.RevokeByUserID(ctx, userID, now)
}

// Menulis session cookie ke HTTP response
func (m *SessionManager) SetCookie(w http.ResponseWriter, rawToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.cfg.CookieName,
		Value:    rawToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.cfg.AbsoluteTimeout.Seconds()),
	})
}

// Menghapus cookie dari client
func (m *SessionManager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.cfg.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// Membaca cookie dari HTTP req
func (m *SessionManager) ReadCookie(r *http.Request) string {
	c, err := r.Cookie(m.cfg.CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// Implementasi SessionStore dengan postgress
type PostgresSessionStore struct {
	db *sql.DB
}

func NewPostgresSessionStore(db *sql.DB) *PostgresSessionStore {
	return &PostgresSessionStore{db: db}
}

func (s *PostgresSessionStore) Insert(ctx context.Context, sess *Session) error {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO sessions (id_hash, user_id, created_at, last_seen_at, rotated_at, prev_id_hash, prev_valid_until, revoked_at, csrf_secret)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, sess.IDHash, sess.UserID, sess.CreatedAt, sess.LastSeenAt, sess.RotatedAt,
		sess.PrevIDHash, sess.PrevValidUntil, sess.RevokedAt, sess.CSRFSecret).Scan(&sess.ID)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *PostgresSessionStore) GetByTokenHash(ctx context.Context, tokenHash []byte) (*Session, bool, error) {
	var sess Session
	var prevValidUntil, revokedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.id_hash, s.user_id, s.created_at, s.last_seen_at, s.rotated_at,
		       s.prev_id_hash, s.prev_valid_until, s.revoked_at, s.csrf_secret
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE (s.id_hash = $1 OR s.prev_id_hash = $1)
		  AND u.is_active = TRUE
		LIMIT 1
	`, tokenHash).Scan(
		&sess.ID,
		&sess.IDHash,
		&sess.UserID,
		&sess.CreatedAt,
		&sess.LastSeenAt,
		&sess.RotatedAt,
		&sess.PrevIDHash,
		&prevValidUntil,
		&revokedAt,
		&sess.CSRFSecret,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrNoSession
	}
	if err != nil {
		return nil, false, fmt.Errorf("query session: %w", err)
	}

	if prevValidUntil.Valid {
		t := prevValidUntil.Time
		sess.PrevValidUntil = &t
	}
	if revokedAt.Valid {
		t := revokedAt.Time
		sess.RevokedAt = &t
	}

	matchedPrev := subtle.ConstantTimeCompare(sess.IDHash, tokenHash) != 1 &&
		len(sess.PrevIDHash) > 0 &&
		subtle.ConstantTimeCompare(sess.PrevIDHash, tokenHash) == 1

	return &sess, matchedPrev, nil
}

func (s *PostgresSessionStore) UpdateLastSeen(ctx context.Context, sessionID int64, lastSeen time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET last_seen_at = $1 WHERE id = $2
	`, lastSeen, sessionID)
	return err
}

func (s *PostgresSessionStore) Rotate(ctx context.Context, sessionID int64, newHash, prevHash []byte, rotatedAt, prevValidUntil time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET id_hash = $1,
		    prev_id_hash = $2,
		    rotated_at = $3,
		    last_seen_at = $3,
		    prev_valid_until = $4
		WHERE id = $5
	`, newHash, prevHash, rotatedAt, prevValidUntil, sessionID)
	return err
}

func (s *PostgresSessionStore) RevokeByID(ctx context.Context, sessionID int64, revokedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = $1 WHERE id = $2 AND revoked_at IS NULL
	`, revokedAt, sessionID)
	return err
}

func (s *PostgresSessionStore) RevokeByUserID(ctx context.Context, userID int64, revokedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = $1 WHERE user_id = $2 AND revoked_at IS NULL
	`, revokedAt, userID)
	return err
}

package auth

// Import pkg
import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Setup error msg
var (
	ErrInvalidCredentials = errors.New("Invalid username or password")
)

type contextKey int

const (
	userContextKey contextKey = iota + 1
	sessionContextKey
)

// Struktur untuk menyimpan user yang sudah terautentikasi dalam menggunakan platform
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email,omitempty"`
	PasswordHash string    `json:"-"`
	IsActive     bool      `json:"is_active"`
	IsAdmin      bool      `json:"is_admin"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (u User) String() string {
	return fmt.Sprintf("User{ID:%d, Username:%q, IsActive:%t, IsAdmin:%t}", u.ID, u.Username, u.IsActive, u.IsAdmin)
}

func WithUserAndSession(ctx context.Context, u *User, s *Session) context.Context {
	ctx = context.WithValue(ctx, userContextKey, u)
	ctx = context.WithValue(ctx, sessionContextKey, s)
	return ctx
}

func UserFromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userContextKey).(*User)
	return u, ok && u != nil
}

func SessionFromContext(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(sessionContextKey).(*Session)
	return s, ok && s != nil
}

// Setup untuk melakukan autentikasi user, manajemen sesi, dan seeding admin
type Service struct {
	db        *sql.DB
	sessions  *SessionManager
	dummyHash string
}

func NewService(db *sql.DB, sessions *SessionManager) (*Service, error) {
	dummy, err := HashPassword("dummy-anti-enumeration-secret")
	if err != nil {
		return nil, fmt.Errorf("initialize dummy hash: %w", err)
	}
	return &Service{
		db:        db,
		sessions:  sessions,
		dummyHash: dummy,
	}, nil
}

func (s *Service) Sessions() *SessionManager {
	return s.sessions
}

// Verifikasi user dan password lalu membuat rotasi sesi
func (s *Service) Authenticate(ctx context.Context, username, password string) (*User, *Session, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		_, _ = VerifyPassword("invalid", s.dummyHash)
		return nil, nil, ErrInvalidCredentials
	}

	user, err := s.getUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_, _ = VerifyPassword(password, s.dummyHash)
			return nil, nil, ErrInvalidCredentials
		}
		return nil, nil, fmt.Errorf("lookup user: %w", err)
	}

	ok, verifyErr := VerifyPassword(password, user.PasswordHash)
	if verifyErr != nil || !ok || !user.IsActive {
		return nil, nil, ErrInvalidCredentials
	}

	sess, err := s.sessions.CreateSession(ctx, user.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("create session: %w", err)
	}

	// Rotasi session id
	newToken, err := s.sessions.ForceRotate(ctx, sess)
	if err != nil {
		return nil, nil, fmt.Errorf("rotate session on login: %w", err)
	}
	sess.RawToken = newToken

	return user, sess, nil
}

func (s *Service) getUserByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	var email sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.email, u.password_hash, u.is_active, u.created_at, u.updated_at,
		       EXISTS (
		           SELECT 1 FROM user_roles ur
		           JOIN roles r ON r.id = ur.role_id
		           WHERE ur.user_id = u.id AND LOWER(r.name) = 'admin'
		       ) AS is_admin
		FROM users u
		WHERE LOWER(u.username) = LOWER($1)
	`, username).Scan(
		&u.ID, &u.Username, &email, &u.PasswordHash, &u.IsActive, &u.CreatedAt, &u.UpdatedAt, &u.IsAdmin,
	)
	if err != nil {
		return nil, err
	}
	if email.Valid {
		u.Email = email.String
	}
	return &u, nil
}

// Mengambil user dan live platform user dari database
func (s *Service) GetUserByID(ctx context.Context, userID int64) (*User, error) {
	var u User
	var email sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.email, u.password_hash, u.is_active, u.created_at, u.updated_at,
		       EXISTS (
		           SELECT 1 FROM user_roles ur
		           JOIN roles r ON r.id = ur.role_id
		           WHERE ur.user_id = u.id AND LOWER(r.name) = 'admin'
		       ) AS is_admin
		FROM users u
		WHERE u.id = $1
	`, userID).Scan(
		&u.ID, &u.Username, &email, &u.PasswordHash, &u.IsActive, &u.CreatedAt, &u.UpdatedAt, &u.IsAdmin,
	)
	if err != nil {
		return nil, err
	}
	if email.Valid {
		u.Email = email.String
	}
	return &u, nil
}

// Melakukan update awal user admin tanpa melakukan login
func (s *Service) SeedAdmin(ctx context.Context, username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("admin username cannot be empty")
	}
	if strings.TrimSpace(password) == "" {
		return nil, ErrEmptyPassword
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin seed-admin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var adminRoleID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO roles (name, description, is_system)
		VALUES ('admin', 'Platform Administrator', TRUE)
		ON CONFLICT (LOWER(name)) DO UPDATE SET is_system = TRUE
		RETURNING id
	`).Scan(&adminRoleID)
	if err != nil {
		return nil, fmt.Errorf("ensure admin role: %w", err)
	}

	var userID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO users (username, password_hash, is_active)
		VALUES ($1, $2, TRUE)
		ON CONFLICT (LOWER(username)) DO UPDATE
		SET password_hash = EXCLUDED.password_hash,
		    is_active = TRUE,
		    updated_at = NOW()
		RETURNING id
	`, username, hash).Scan(&userID)
	if err != nil {
		return nil, fmt.Errorf("upsert admin user: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, role_id) DO NOTHING
	`, userID, adminRoleID)
	if err != nil {
		return nil, fmt.Errorf("assign admin role: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit seed-admin: %w", err)
	}

	return s.GetUserByID(ctx, userID)
}

// Mendaftarkan user baru secara mandiri tanpa hak akses / privilege apa pun
func (s *Service) RegisterUser(ctx context.Context, username, email, password string) (*User, error) {
	username = strings.TrimSpace(username)
	email = strings.TrimSpace(email)
	if username == "" || len(username) > 50 {
		return nil, errors.New("username must be between 1 and 50 characters")
	}
	if strings.TrimSpace(password) == "" {
		return nil, ErrEmptyPassword
	}
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	var nullEmail sql.NullString
	if email != "" {
		nullEmail = sql.NullString{String: email, Valid: true}
	}

	var userID int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO users (username, email, password_hash, is_active)
		VALUES ($1, $2, $3, TRUE)
		RETURNING id
	`, username, nullEmail, hash).Scan(&userID)
	if err != nil {
		return nil, fmt.Errorf("register user: %w", err)
	}

	return s.GetUserByID(ctx, userID)
}

// Membaca password admin awal dari .env
func ReadSeedPassword(r io.Reader) (string, error) {
	if envPass := os.Getenv("SEED_ADMIN_PASSWORD"); envPass != "" {
		return envPass, nil
	}
	if r == nil {
		return "", errors.New("no password provided in SEED_ADMIN_PASSWORD or stdin")
	}
	scanner := bufio.NewScanner(r)
	if scanner.Scan() {
		pass := strings.TrimRight(scanner.Text(), "\r\n")
		if pass != "" {
			return pass, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	return "", errors.New("empty password from stdin")
}

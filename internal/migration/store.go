package migration

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool: pool,
	}
}

func (s *Store) CreateDraft(ctx context.Context, databaseID int, title string, sql string, createdBy int) (*Migration, error) {
	m := &Migration{
		DatabaseID: databaseID,
		Title:      title,
		SQL:        sql,
		Status:     S_Draft,
		CreatedBy:  createdBy,
	}

	query := "INSERT INTO migrations(database_id, title, sql, status, created_by, created_at) VALUES ($1, $2, $3, $4, $5, NOW()) RETURNING id, created_at"

	err := s.pool.QueryRow(ctx, query, m.DatabaseID, m.Title, m.SQL, m.Status, m.CreatedBy).Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create draft migration: %w", err)
	}

	return m, nil
}

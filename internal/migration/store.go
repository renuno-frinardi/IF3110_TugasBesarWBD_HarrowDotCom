package migration

import (
	"context"
	"encoding/json"
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
	m, _, err := s.CreateDraftWithRevision(ctx, databaseID, title, sql, "", true, "Legacy draft", ModeSingle, BatchConfig{}, createdBy)
	return m, err
}

func (s *Store) CreateDraftWithRevision(ctx context.Context, databaseID int, title, upSQL, downSQL string, irreversible bool, reason, mode string, cfg BatchConfig, createdBy int) (*Migration, *Revision, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	m := &Migration{
		DatabaseID: databaseID,
		Title:      title,
		SQL:        upSQL,
		Status:     S_Draft,
		CreatedBy:  createdBy,
	}
	err = tx.QueryRow(ctx, "INSERT INTO migrations(database_id, title, sql, status, created_by, created_at) VALUES ($1,$2,$3,$4,$5,NOW()) RETURNING id, created_at", m.DatabaseID, m.Title, m.SQL, m.Status, m.CreatedBy).Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create draft migration: %w", err)
	}
	cfgBytes, _ := json.Marshal(cfg)
	hash := revisionHash(upSQL, downSQL, irreversible, reason, mode, cfg)
	r := &Revision{
		MigrationID: m.ID, RevisionNo: 1, UpSQL: upSQL, DownSQL: downSQL,
		Irreversible: irreversible, IrreversibleReason: reason, Mode: mode, BatchConfig: cfg,
		Status: StatusDraft, ContentHash: hash, CreatedBy: createdBy,
	}
	err = tx.QueryRow(ctx, `INSERT INTO migration_revisions (migration_id, revision_no, up_sql, down_sql, irreversible, irreversible_reason, mode, batch_config, status, content_hash, created_by)
          VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,'draft',$9,$10) RETURNING id, created_at`, 
		  r.MigrationID, r.RevisionNo, r.UpSQL, r.DownSQL, r.Irreversible, r.IrreversibleReason, r.Mode, cfgBytes, r.ContentHash, r.CreatedBy).Scan(&r.ID, &r.CreatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("create migration revision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return m, r, nil
}

func (s *Store) GetByID(ctx context.Context, id int) (*Migration, error) {
	m := &Migration{}

	query := `SELECT id, database_id, version, title, sql, status, active,
			created_by, executed_by, created_at, updated_at,
			start_time, finish_time, error_message
			FROM migrations
			WHERE id = $1`

	err := s.pool.QueryRow(ctx, query, id).Scan(&m.ID, &m.DatabaseID, &m.Version, &m.Title, &m.SQL, &m.Status, &m.Active,
		&m.CreatedBy, &m.ExecutedBy, &m.CreatedAt, &m.UpdatedAt, &m.StartTime, &m.FinishTime, &m.ErrorMessage)

	if err != nil {
		return nil, fmt.Errorf("Failed to load the migration: %w", err)
	}

	return m, nil
}

func (s *Store) GetByDatabase(ctx context.Context, databaseID int) ([]Migration, error) {
	query := `SELECT m.id, m.database_id, m.version, m.title, m.sql, m.status,
			m.created_by, m.executed_by, m.created_at, m.updated_at,
			m.start_time, m.finish_time, m.error_message, u.username
			FROM migrations m
			LEFT JOIN users u ON u.id = m.executed_by
			WHERE m.database_id = $1
			ORDER BY m.version, m.created_at, m.id`

	rows, err := s.pool.Query(ctx, query, databaseID)
	if err != nil {
		return nil, fmt.Errorf("Failed to load the migration list: %w", err)
	}
	defer rows.Close()

	migrations := make([]Migration, 0)

	for rows.Next() {
		var m Migration

		err := rows.Scan(&m.ID, &m.DatabaseID, &m.Version, &m.Title, &m.SQL, &m.Status, &m.CreatedBy, &m.ExecutedBy,
			&m.CreatedAt, &m.UpdatedAt, &m.StartTime, &m.FinishTime, &m.ErrorMessage, &m.ExecutedByName)

		if err != nil {
			return nil, fmt.Errorf("Failed to read migration data: %w", err)
		}

		migrations = append(migrations, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Failed to read the migration list: %w", err)
	}

	return migrations, nil
}

func (s *Store) Update(ctx context.Context, id int, title string, sql string) (*Migration, error) {
	m := &Migration{}
	query := `UPDATE migrations 
			SET title = $1, sql = $2, updated_at = NOW()
			WHERE id=$3 AND status = 'draft' 
			RETURNING id, database_id, version, title, sql, status, created_by, executed_by, created_at,
			updated_at, start_time, finish_time, error_message`

	err := s.pool.QueryRow(ctx, query, title, sql, id).Scan(&m.ID, &m.DatabaseID, &m.Version, &m.Title, &m.SQL, &m.Status, &m.CreatedBy, &m.ExecutedBy, &m.CreatedAt, &m.UpdatedAt, &m.StartTime, &m.FinishTime, &m.ErrorMessage)

	if err != nil {
		return nil, fmt.Errorf("Failed to update the draft migration: %w", err)
	}
	return m, nil
}

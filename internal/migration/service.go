package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var InvalidInput = errors.New("input tidak valid")
var Forbidden = errors.New("tidak memiliki akses")

type Service struct {
	store  *Store
	access *DatabaseAccess
}

func NewService(store *Store, access *DatabaseAccess) *Service {
	return &Service{
		store:  store,
		access: access,
	}
}

func (s *Service) checkAccess(ctx context.Context, userID int, databaseID int, needEdit bool) error {
	if userID <= 0 {
		return Forbidden
	}

	if s.access == nil {
		return errors.New("pemeriksaan akses belum dikonfigurasi")
	}

	allowed, err := s.access.CanAccess(ctx, userID, databaseID, needEdit)

	if err != nil {
		return err
	}
	if !allowed {
		return Forbidden
	}
	return nil
}

func isDraftValid(title string, sql string) error {
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("%w: judul harus diisi", InvalidInput)
	}

	if strings.TrimSpace(sql) == "" {
		return fmt.Errorf("%w: SQL harus diisi", InvalidInput)
	}

	return nil
}

func (s *Service) CreateDraft(ctx context.Context, userID int, databaseID int, title string, sql string) (*Migration, error) {
	if err := s.checkAccess(ctx, userID, databaseID, true); err != nil {
		return nil, err
	}

	if err := isDraftValid(title, sql); err != nil {
		return nil, err
	}
	return s.store.CreateDraft(ctx, databaseID, title, sql, userID)
}

func (s *Service) GetByID(ctx context.Context, userID int, id int) (*Migration, error) {
	m, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.checkAccess(ctx, userID, m.DatabaseID, false); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Service) GetByDatabase(ctx context.Context, userID int, databaseID int) ([]Migration, error) {
	if err := s.checkAccess(ctx, userID, databaseID, false); err != nil {
		return nil, err
	}
	return s.store.GetByDatabase(ctx, databaseID)
}

func (s *Service) Update(ctx context.Context, userID int, id int, title string, sql string) (*Migration, error) {
	m, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, userID, m.DatabaseID, true); err != nil {
		return nil, err
	}
	if err := isDraftValid(title, sql); err != nil {
		return nil, err
	}
	return s.store.Update(ctx, id, title, sql)
}

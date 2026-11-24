package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ivan/engram/internal/db"
)

var (
	ErrConfirmationMismatch = errors.New("confirmation ID does not match project ID")
	ErrProjectNotFound      = errors.New("project not found")
	ErrEmptyProjectID       = errors.New("project ID cannot be empty")
	ErrInvalidSettings      = errors.New("invalid JSON settings")
)

type Project struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Settings    json.RawMessage `json:"settings"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	DeletedAt   *time.Time      `json:"deleted_at,omitempty"`
}

type Querier interface {
	CreateProject(ctx context.Context, arg db.CreateProjectParams) (db.Project, error)
	GetProject(ctx context.Context, id string) (db.Project, error)
	ListProjects(ctx context.Context) ([]db.Project, error)
	UpdateProjectSettings(ctx context.Context, arg db.UpdateProjectSettingsParams) (db.Project, error)
	SoftDeleteProject(ctx context.Context, id string) error
	PurgeProject(ctx context.Context, id string) error
}

type Service struct {
	pool    *pgxpool.Pool
	queries Querier
}

func NewService(pool *pgxpool.Pool) *Service {
	var q Querier
	if pool != nil {
		q = db.New(pool)
	}
	return &Service{
		pool:    pool,
		queries: q,
	}
}

func NewServiceWithQuerier(q Querier) *Service {
	return &Service{
		queries: q,
	}
}

func toProject(p db.Project) Project {
	settings := json.RawMessage("{}")
	if len(p.Settings) > 0 {
		settings = json.RawMessage(p.Settings)
	}

	var deletedAt *time.Time
	if p.DeletedAt.Valid {
		t := p.DeletedAt.Time
		deletedAt = &t
	}

	var createdAt time.Time
	if p.CreatedAt.Valid {
		createdAt = p.CreatedAt.Time
	}

	var updatedAt time.Time
	if p.UpdatedAt.Valid {
		updatedAt = p.UpdatedAt.Time
	}

	return Project{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Settings:    settings,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		DeletedAt:   deletedAt,
	}
}

func normalizeSettings(settings json.RawMessage) ([]byte, error) {
	if len(settings) == 0 || string(settings) == "null" {
		return []byte("{}"), nil
	}
	if !json.Valid(settings) {
		return nil, ErrInvalidSettings
	}
	var js interface{}
	if err := json.Unmarshal(settings, &js); err != nil {
		return nil, ErrInvalidSettings
	}
	return []byte(settings), nil
}

func (s *Service) CreateProject(ctx context.Context, id, name, description string, settings json.RawMessage) (*Project, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrEmptyProjectID
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = id
	}
	cleanSettings, err := normalizeSettings(settings)
	if err != nil {
		return nil, err
	}

	dbProj, err := s.queries.CreateProject(ctx, db.CreateProjectParams{
		ID:          id,
		Name:        name,
		Description: description,
		Settings:    cleanSettings,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create project: %w", err)
	}

	proj := toProject(dbProj)
	return &proj, nil
}

func (s *Service) GetProject(ctx context.Context, id string) (*Project, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrEmptyProjectID
	}

	dbProj, err := s.queries.GetProject(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	proj := toProject(dbProj)
	return &proj, nil
}

func (s *Service) ListProjects(ctx context.Context) ([]Project, error) {
	dbProjects, err := s.queries.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}

	result := make([]Project, 0, len(dbProjects))
	for _, p := range dbProjects {
		result = append(result, toProject(p))
	}
	return result, nil
}

func (s *Service) UpdateProject(ctx context.Context, id, name, description string, settings json.RawMessage) (*Project, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrEmptyProjectID
	}

	existing, err := s.GetProject(ctx, id)
	if err != nil {
		return nil, err
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = existing.Name
	}
	if description == "" && existing.Description != "" {
		description = existing.Description
	}

	var cleanSettings []byte
	if len(settings) == 0 || string(settings) == "null" {
		cleanSettings = []byte(existing.Settings)
	} else {
		cleanSettings, err = normalizeSettings(settings)
		if err != nil {
			return nil, err
		}
	}

	dbProj, err := s.queries.UpdateProjectSettings(ctx, db.UpdateProjectSettingsParams{
		ID:          id,
		Name:        name,
		Description: description,
		Settings:    cleanSettings,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, fmt.Errorf("failed to update project: %w", err)
	}

	proj := toProject(dbProj)
	return &proj, nil
}

func (s *Service) DeleteProject(ctx context.Context, id, confirmID string, purge bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrEmptyProjectID
	}
	if confirmID != id {
		return ErrConfirmationMismatch
	}

	// Verify project exists (if soft-delete, must be active)
	if !purge {
		_, err := s.GetProject(ctx, id)
		if err != nil {
			return err
		}
	}

	if purge {
		if err := s.queries.PurgeProject(ctx, id); err != nil {
			return fmt.Errorf("failed to purge project: %w", err)
		}
	} else {
		if err := s.queries.SoftDeleteProject(ctx, id); err != nil {
			return fmt.Errorf("failed to soft delete project: %w", err)
		}
	}

	return nil
}

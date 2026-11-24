package projects

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ivan/engram/internal/db"
)

type mockQuerier struct {
	projects map[string]db.Project
}

func newMockQuerier() *mockQuerier {
	return &mockQuerier{
		projects: make(map[string]db.Project),
	}
}

func (m *mockQuerier) CreateProject(ctx context.Context, arg db.CreateProjectParams) (db.Project, error) {
	now := time.Now()
	nowPg := pgtype.Timestamptz{Time: now, Valid: true}
	p := db.Project{
		ID:          arg.ID,
		Name:        arg.Name,
		Description: arg.Description,
		Settings:    arg.Settings,
		CreatedAt:   nowPg,
		UpdatedAt:   nowPg,
		DeletedAt:   pgtype.Timestamptz{Valid: false},
	}
	m.projects[arg.ID] = p
	return p, nil
}

func (m *mockQuerier) GetProject(ctx context.Context, id string) (db.Project, error) {
	p, ok := m.projects[id]
	if !ok || p.DeletedAt.Valid {
		return db.Project{}, pgx.ErrNoRows
	}
	return p, nil
}

func (m *mockQuerier) ListProjects(ctx context.Context) ([]db.Project, error) {
	var list []db.Project
	for _, p := range m.projects {
		if !p.DeletedAt.Valid {
			list = append(list, p)
		}
	}
	return list, nil
}

func (m *mockQuerier) UpdateProjectSettings(ctx context.Context, arg db.UpdateProjectSettingsParams) (db.Project, error) {
	p, ok := m.projects[arg.ID]
	if !ok || p.DeletedAt.Valid {
		return db.Project{}, pgx.ErrNoRows
	}
	p.Name = arg.Name
	p.Description = arg.Description
	p.Settings = arg.Settings
	p.UpdatedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	m.projects[arg.ID] = p
	return p, nil
}

func (m *mockQuerier) SoftDeleteProject(ctx context.Context, id string) error {
	p, ok := m.projects[id]
	if !ok || p.DeletedAt.Valid {
		return pgx.ErrNoRows
	}
	p.DeletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	m.projects[id] = p
	return nil
}

func (m *mockQuerier) PurgeProject(ctx context.Context, id string) error {
	delete(m.projects, id)
	return nil
}

func TestCreateProject(t *testing.T) {
	ctx := context.Background()
	mock := newMockQuerier()
	svc := NewServiceWithQuerier(mock)

	// Empty ID
	_, err := svc.CreateProject(ctx, "", "Name", "Desc", nil)
	if !errors.Is(err, ErrEmptyProjectID) {
		t.Fatalf("expected ErrEmptyProjectID, got %v", err)
	}

	// Invalid Settings
	_, err = svc.CreateProject(ctx, "proj-1", "Name", "Desc", json.RawMessage(`{invalid`))
	if !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("expected ErrInvalidSettings, got %v", err)
	}

	// Default name to ID if name is empty
	p, err := svc.CreateProject(ctx, "proj-auto", "", "Desc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name != "proj-auto" {
		t.Fatalf("expected name to default to 'proj-auto', got '%s'", p.Name)
	}
	if string(p.Settings) != "{}" {
		t.Fatalf("expected empty settings '{}', got '%s'", string(p.Settings))
	}

	// Full project creation
	settings := json.RawMessage(`{"docs":{"root":"docs"}}`)
	p2, err := svc.CreateProject(ctx, "proj-2", "Project Two", "Test project", settings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p2.ID != "proj-2" || p2.Name != "Project Two" {
		t.Fatalf("mismatched project fields: %+v", p2)
	}
}

func TestGetAndListProjects(t *testing.T) {
	ctx := context.Background()
	mock := newMockQuerier()
	svc := NewServiceWithQuerier(mock)

	// Get non-existent
	_, err := svc.GetProject(ctx, "non-existent")
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("expected ErrProjectNotFound, got %v", err)
	}

	// Empty list
	list, err := svc.ListProjects(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 projects, got %d", len(list))
	}

	// Create and get
	_, err = svc.CreateProject(ctx, "p1", "Project 1", "", nil)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	_, err = svc.CreateProject(ctx, "p2", "Project 2", "", nil)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	got, err := svc.GetProject(ctx, "p1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.ID != "p1" {
		t.Fatalf("expected p1, got %s", got.ID)
	}

	list, err = svc.ListProjects(ctx)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(list))
	}
}

func TestUpdateProject(t *testing.T) {
	ctx := context.Background()
	mock := newMockQuerier()
	svc := NewServiceWithQuerier(mock)

	// Update non-existent
	_, err := svc.UpdateProject(ctx, "non-existent", "New", "Desc", nil)
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("expected ErrProjectNotFound, got %v", err)
	}

	// Create project
	origSettings := json.RawMessage(`{"theme":"dark"}`)
	_, err = svc.CreateProject(ctx, "proj-update", "Initial Name", "Initial Desc", origSettings)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Partial update: preserve existing settings if nil passed
	updated, err := svc.UpdateProject(ctx, "proj-update", "Updated Name", "", nil)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Name != "Updated Name" {
		t.Fatalf("expected Updated Name, got %s", updated.Name)
	}
	if string(updated.Settings) != `{"theme":"dark"}` {
		t.Fatalf("expected preserved settings, got %s", string(updated.Settings))
	}

	// Invalid settings update
	_, err = svc.UpdateProject(ctx, "proj-update", "New Name", "Desc", json.RawMessage(`{bad-json`))
	if !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("expected ErrInvalidSettings, got %v", err)
	}
}

func TestDeleteProject(t *testing.T) {
	ctx := context.Background()
	mock := newMockQuerier()
	svc := NewServiceWithQuerier(mock)

	// Create project
	_, err := svc.CreateProject(ctx, "to-delete", "To Delete", "", nil)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Mismatched confirm ID
	err = svc.DeleteProject(ctx, "to-delete", "wrong-id", false)
	if !errors.Is(err, ErrConfirmationMismatch) {
		t.Fatalf("expected ErrConfirmationMismatch, got %v", err)
	}

	// Soft delete
	err = svc.DeleteProject(ctx, "to-delete", "to-delete", false)
	if err != nil {
		t.Fatalf("soft delete failed: %v", err)
	}

	// After soft delete, GetProject should return ErrProjectNotFound
	_, err = svc.GetProject(ctx, "to-delete")
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("expected ErrProjectNotFound after soft delete, got %v", err)
	}

	// Second delete on already deleted project should return ErrProjectNotFound
	err = svc.DeleteProject(ctx, "to-delete", "to-delete", false)
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("expected ErrProjectNotFound on already deleted project, got %v", err)
	}

	// Recreate and purge (hard delete)
	_, err = svc.CreateProject(ctx, "to-purge", "To Purge", "", nil)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	err = svc.DeleteProject(ctx, "to-purge", "to-purge", true)
	if err != nil {
		t.Fatalf("purge failed: %v", err)
	}

	_, err = svc.GetProject(ctx, "to-purge")
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("expected ErrProjectNotFound after purge, got %v", err)
	}
}

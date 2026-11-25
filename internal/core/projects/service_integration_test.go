package projects

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	platformdb "github.com/ivan/engram/internal/platform/db"
)

func TestService_PostgresIntegration(t *testing.T) {
	dbHost := os.Getenv("ENGRAM_DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}
	dbPort := 5439
	if rawPort := os.Getenv("ENGRAM_DB_PORT"); rawPort != "" {
		if parsed, err := strconv.Atoi(rawPort); err == nil {
			dbPort = parsed
		}
	}
	dbUser := "engram"
	dbPass := "engram_dev_secret"
	dbName := "engram"

	ctx := context.Background()
	pool, err := platformdb.NewPool(ctx, platformdb.Config{
		Host:     dbHost,
		Port:     dbPort,
		User:     dbUser,
		Password: dbPass,
		Database: dbName,
		SSLMode:  "disable",
	})
	if err != nil {
		t.Skipf("Skipping integration test; Postgres not reachable: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("Skipping integration test; Postgres ping failed: %v", err)
	}

	svc := NewService(pool)
	testID := "integration-test-proj"

	// Cleanup in case prior run left it
	_ = svc.DeleteProject(ctx, testID, testID, true)

	// 1. CreateProject with settings
	settings := json.RawMessage(`{"docs":{"root":"docs","todo_file":"TODO.md"}}`)
	created, err := svc.CreateProject(ctx, testID, "Integration Proj", "Desc", settings)
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	if created.ID != testID || created.Name != "Integration Proj" {
		t.Fatalf("unexpected created project: %+v", created)
	}

	// 2. GetProject
	got, err := svc.GetProject(ctx, testID)
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if got.ID != testID {
		t.Fatalf("expected ID %s, got %s", testID, got.ID)
	}

	// 3. ListProjects
	list, err := svc.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	found := false
	for _, p := range list {
		if p.ID == testID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("project %s not found in ListProjects", testID)
	}

	// 4. UpdateProject
	updatedSettings := json.RawMessage(`{"docs":{"root":"documentation"}}`)
	updated, err := svc.UpdateProject(ctx, testID, "Updated Name", "New Desc", updatedSettings)
	if err != nil {
		t.Fatalf("UpdateProject failed: %v", err)
	}
	if updated.Name != "Updated Name" || updated.Description != "New Desc" {
		t.Fatalf("unexpected updated project: %+v", updated)
	}

	// 5. Soft Delete
	if err := svc.DeleteProject(ctx, testID, testID, false); err != nil {
		t.Fatalf("DeleteProject (soft) failed: %v", err)
	}

	// Verify not found after soft delete
	_, err = svc.GetProject(ctx, testID)
	if err != ErrProjectNotFound {
		t.Fatalf("expected ErrProjectNotFound, got %v", err)
	}

	// 6. Purge
	if err := svc.DeleteProject(ctx, testID, testID, true); err != ErrProjectNotFound {
		// Since it's soft deleted, DeleteProject checks GetProject and returns ErrProjectNotFound
		// Now revive it to test purge
		_, err = svc.CreateProject(ctx, testID, "Revived", "", nil)
		if err != nil {
			t.Fatalf("revive CreateProject failed: %v", err)
		}
		if err := svc.DeleteProject(ctx, testID, testID, true); err != nil {
			t.Fatalf("DeleteProject (purge) failed: %v", err)
		}
	}
}

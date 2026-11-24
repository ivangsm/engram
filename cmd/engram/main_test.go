package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectProjectID(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Explicit flag
	id := detectProjectID("flag-project", tmpDir)
	if id != "flag-project" {
		t.Fatalf("expected flag-project, got %s", id)
	}

	// 2. Environment variable
	t.Setenv("ENGRAM_PROJECT_ID", "env-project")
	id = detectProjectID("", tmpDir)
	if id != "env-project" {
		t.Fatalf("expected env-project, got %s", id)
	}

	// Clear env for directory tests
	t.Setenv("ENGRAM_PROJECT_ID", "")
	baseID := detectProjectID("", tmpDir)
	if baseID != filepath.Base(tmpDir) {
		t.Fatalf("expected directory basename %s, got %s", filepath.Base(tmpDir), baseID)
	}
}

func TestDetectTopology_DocsDir(t *testing.T) {
	tmpDir := t.TempDir()
	docsDir := filepath.Join(tmpDir, "checamos-docs")
	if err := os.Mkdir(docsDir, 0755); err != nil {
		t.Fatal(err)
	}
	todoFile := filepath.Join(docsDir, "TODO.md")
	if err := os.WriteFile(todoFile, []byte("# TODO"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := detectTopology(tmpDir, "", "")
	if cfg.Root != "checamos-docs" {
		t.Fatalf("expected Root 'checamos-docs', got '%s'", cfg.Root)
	}
	if cfg.TodoFile != "TODO.md" {
		t.Fatalf("expected TodoFile 'TODO.md', got '%s'", cfg.TodoFile)
	}
}

func TestDetectTopology_LooseMarkdownAndSerena(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "TODO.md"), []byte("# TODO"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "ARCHITECTURE.md"), []byte("# Arch"), 0644); err != nil {
		t.Fatal(err)
	}
	serenaDir := filepath.Join(tmpDir, ".serena", "memories")
	if err := os.MkdirAll(serenaDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := detectTopology(tmpDir, "", "")
	if cfg.Root != "." {
		t.Fatalf("expected Root '.', got '%s'", cfg.Root)
	}
	if cfg.TodoFile != "TODO.md" {
		t.Fatalf("expected TodoFile 'TODO.md', got '%s'", cfg.TodoFile)
	}
	if len(cfg.Sources) < 3 {
		t.Fatalf("expected at least 3 sources, got %v", cfg.Sources)
	}
}

func TestDetectTopology_DefaultFallback(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := detectTopology(tmpDir, "", "")
	if cfg.Root != "docs" {
		t.Fatalf("expected default Root 'docs', got '%s'", cfg.Root)
	}
}

func TestDetectTopology_Overrides(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := detectTopology(tmpDir, "custom-docs", "CUSTOM_TODO.md")
	if cfg.Root != "custom-docs" {
		t.Fatalf("expected 'custom-docs', got '%s'", cfg.Root)
	}
	if cfg.TodoFile != "CUSTOM_TODO.md" {
		t.Fatalf("expected 'CUSTOM_TODO.md', got '%s'", cfg.TodoFile)
	}
}

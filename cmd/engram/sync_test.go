package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSyncPlan_MixedTopology(t *testing.T) {
	root := t.TempDir()

	// 1. Serena memories
	writeTestFile(t, filepath.Join(root, ".serena", "memories", "auth-flow.md"), "# Auth Flow\nJWT tokens expire in 15m.\n")
	// 2. Rules
	writeTestFile(t, filepath.Join(root, ".agents", "rules", "sql-rules.md"), "---\ndigest: Always use parameterized SQL queries\n---\n# SQL Rules\nNever interpolate.\n")
	// 3. Docs + ADR
	writeTestFile(t, filepath.Join(root, "docs", "architecture.md"), "# Architecture\nHexagonal layout.\n")
	writeTestFile(t, filepath.Join(root, "docs", "adr", "0001-postgres.md"), "# ADR 0001\nUse PostgreSQL 18.\n")
	// 4. EGM directory README + root README
	writeTestFile(t, filepath.Join(root, "README.md"), "# MyProject\nMain readme.\n")
	writeTestFile(t, filepath.Join(root, "internal", "core", "README.md"), "# Core Package\nDomain services.\n")
	// 5. Pendientes (one pending, one completed)
	writeTestFile(t, filepath.Join(root, "pendientes", "PND-042.md"), "# Fix race condition\nPriority: high\nDetails here.\n")
	writeTestFile(t, filepath.Join(root, "pendientes", "PND-043.md"), "# Old bug\nStatus: completed\nDone.\n")
	// 6. TODO.md
	writeTestFile(t, filepath.Join(root, "TODO.md"), "# Tasks\n- [ ] [TASK-101] Implement sync\n- [ ] Add unit tests\n- [x] Already done\n")
	// 7. Ignored dir (must not be synced)
	writeTestFile(t, filepath.Join(root, "node_modules", "pkg", "README.md"), "# Ignored\n")

	plan, err := buildSyncPlan(syncOptions{
		ProjectID: "birdple-v2",
		Path:      root,
	})
	if err != nil {
		t.Fatalf("buildSyncPlan failed: %v", err)
	}

	memByKey := make(map[string]syncMemoryItem)
	for _, m := range plan.Memories {
		memByKey[m.Key] = m
	}

	expectedKeys := map[string]string{
		"serena/auth-flow":       "serena",
		"rules/sql-rules":        "rules",
		"docs/architecture":      "docs",
		"docs/adr/0001-postgres": "adr",
		"root/README":            "root",
		"egm/internal/core/README": "egm",
	}
	for key, wantScope := range expectedKeys {
		m, ok := memByKey[key]
		if !ok {
			t.Errorf("expected memory key %q not found in plan (got keys: %v)", key, keysOf(memByKey))
			continue
		}
		if m.Scope != wantScope {
			t.Errorf("key %q: expected scope %q, got %q", key, wantScope, m.Scope)
		}
	}

	if _, ok := memByKey["egm/node_modules/pkg/README"]; ok {
		t.Errorf("node_modules should have been ignored")
	}

	if memByKey["rules/sql-rules"].Digest != "Always use parameterized SQL queries" {
		t.Errorf("expected frontmatter digest, got %q", memByKey["rules/sql-rules"].Digest)
	}
	if !strings.Contains(memByKey["serena/auth-flow"].Digest, "Auth Flow") {
		t.Errorf("expected heading in digest, got %q", memByKey["serena/auth-flow"].Digest)
	}

	taskByID := make(map[string]syncTaskItem)
	for _, tk := range plan.Tasks {
		taskByID[tk.ID] = tk
	}
	if tk, ok := taskByID["PND-042"]; !ok {
		t.Errorf("expected PND-042 in tasks")
	} else if tk.Priority != 10 {
		t.Errorf("expected PND-042 priority 10, got %d", tk.Priority)
	}
	if _, ok := taskByID["PND-043"]; ok {
		t.Errorf("completed PND-043 should be skipped by default")
	}
	if _, ok := taskByID["TASK-101"]; !ok {
		t.Errorf("expected TASK-101 from TODO.md")
	}
	if len(plan.Tasks) != 3 {
		t.Errorf("expected 3 pending tasks (PND-042, TASK-101, Add unit tests), got %d", len(plan.Tasks))
	}
}

func TestBuildSyncPlan_SiblingDocsRepo(t *testing.T) {
	parent := t.TempDir()
	apiRepo := filepath.Join(parent, "checamos-api")
	docsRepo := filepath.Join(parent, "checamos-docs")

	writeTestFile(t, filepath.Join(apiRepo, "README.md"), "# API\nService.\n")
	writeTestFile(t, filepath.Join(docsRepo, "domain", "payments.md"), "# Payments\nStripe and MercadoPago.\n")

	plan, err := buildSyncPlan(syncOptions{
		ProjectID: "checamos",
		Path:      apiRepo,
	})
	if err != nil {
		t.Fatalf("buildSyncPlan failed: %v", err)
	}

	foundDocs := false
	for _, m := range plan.Memories {
		if m.Key == "docs/domain/payments" && m.Scope == "docs" {
			foundDocs = true
		}
	}
	if !foundDocs {
		t.Fatalf("expected sibling checamos-docs/domain/payments.md to be discovered, got %+v", plan.Memories)
	}
}

func TestSync_IdempotencyAndDryRun(t *testing.T) {
	root := t.TempDir()
	existingContent := "# Existing\nUnchanged content."
	newContent := "# New\nFresh content."

	writeTestFile(t, filepath.Join(root, "docs", "existing.md"), existingContent+"\n")
	writeTestFile(t, filepath.Join(root, "docs", "new.md"), newContent+"\n")
	writeTestFile(t, filepath.Join(root, "pendientes", "PND-1.md"), "# First task\nDo it.\n")

	existingHash := sha256Hex(existingContent)

	var mu sync.Mutex
	storedKeys := []string{}
	createdTasks := []string{}

	srv, _ := newMockMCPServer(t, func(name string, args map[string]interface{}) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		switch name {
		case "create_project":
			return `{"id":"demo"}`, false
		case "list_memory_manifest":
			b, _ := json.Marshal([]map[string]string{{"key": "docs/existing"}})
			return string(b), false
		case "blame_memory":
			return `{"current_hash":"` + existingHash + `"}`, false
		case "store_memory":
			storedKeys = append(storedKeys, args["key"].(string))
			return `{"revision_number":1}`, false
		case "create_task":
			createdTasks = append(createdTasks, args["task_id"].(string))
			return `{"id":"` + args["task_id"].(string) + `"}`, false
		default:
			return `{}`, false
		}
	})
	defer srv.Close()

	// Dry run must not hit daemon
	var dryOut, dryErr bytes.Buffer
	code := run([]string{"sync", "demo", "--path", root, "--dry-run"}, "http://127.0.0.1:0", "tok", &dryOut, &dryErr)
	if code != 0 {
		t.Fatalf("dry-run failed (%d): %s", code, dryErr.String())
	}
	if !strings.Contains(dryOut.String(), "Memories discovered: 2") {
		t.Fatalf("unexpected dry-run output:\n%s", dryOut.String())
	}

	// Real sync: should skip docs/existing, store docs/new, create PND-1
	var out, errOut bytes.Buffer
	code = run([]string{"sync", "demo", "--path", root}, srv.URL, "tok", &out, &errOut)
	if code != 0 {
		t.Fatalf("sync failed (%d): %s", code, errOut.String())
	}

	if len(storedKeys) != 1 || storedKeys[0] != "docs/new" {
		t.Fatalf("expected only docs/new to be stored, got %v", storedKeys)
	}
	if len(createdTasks) != 1 || createdTasks[0] != "PND-1" {
		t.Fatalf("expected PND-1 to be created, got %v", createdTasks)
	}
	if !strings.Contains(out.String(), "Memories unchanged: 1") {
		t.Fatalf("expected unchanged count 1 in output:\n%s", out.String())
	}
}

func keysOf(m map[string]syncMemoryItem) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

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
	// 5. Standard TODO.md
	writeTestFile(t, filepath.Join(root, "TODO.md"), "# Tasks\n- [ ] [TASK-101] Implement sync\n- [ ] Add unit tests\n- [x] Already done\n")
	// 6. Ignored dir (must not be synced)
	writeTestFile(t, filepath.Join(root, "node_modules", "pkg", "README.md"), "# Ignored\n")

	plan, err := buildSyncPlan(syncOptions{
		ProjectID: "demo-repo",
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
		"serena/auth-flow":         "serena",
		"rules/sql-rules":          "rules",
		"docs/architecture":        "docs",
		"docs/adr/0001-postgres":   "adr",
		"root/README":              "root",
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
	if _, ok := taskByID["TASK-101"]; !ok {
		t.Errorf("expected TASK-101 from TODO.md")
	}
	if len(plan.Tasks) != 2 {
		t.Errorf("expected 2 pending tasks (TASK-101, Add unit tests), got %d", len(plan.Tasks))
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

func TestSync_CuratedPlanAndIdempotency(t *testing.T) {
	root := t.TempDir()
	existingContent := "# Existing\nUnchanged content."
	newContent := "# New\nFresh content."

	writeTestFile(t, filepath.Join(root, "docs", "existing.md"), existingContent+"\n")
	writeTestFile(t, filepath.Join(root, "docs", "new.md"), newContent+"\n")

	planPath := filepath.Join(root, "curated-plan.json")
	planJSON := `{
  "project_id": "demo",
  "memories": [
    {
      "key": "docs/existing",
      "scope": "docs",
      "digest": "Existing doc digest",
      "file": "docs/existing.md",
      "rationale": "curated check"
    },
    {
      "key": "docs/new",
      "scope": "arch",
      "digest": "New architectural invariant",
      "file": "docs/new.md",
      "rationale": "agent curated"
    }
  ],
  "tasks": [
    {
      "id": "TASK-1",
      "title": "Curated active task",
      "description": "Details",
      "priority": 10
    }
  ],
  "complete_tasks": ["TASK-2"]
}`
	writeTestFile(t, planPath, planJSON)

	existingHash := sha256Hex(existingContent)

	var mu sync.Mutex
	storedKeys := []string{}
	createdTasks := []string{}
	completedTasks := []string{}

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
		case "list_tasks":
			return `[{"id":"TASK-2","status":"pending"}]`, false
		case "claim_task":
			return `{"id":"` + args["task_id"].(string) + `","lease_id":"lease-uuid-123"}`, false
		case "complete_task":
			completedTasks = append(completedTasks, args["task_id"].(string))
			return `{"id":"` + args["task_id"].(string) + `","status":"completed"}`, false
		default:
			return `{}`, false
		}
	})
	defer srv.Close()

	// Dry run must not hit daemon
	var dryOut, dryErr bytes.Buffer
	code := run([]string{"sync", "demo", "--path", root, "--plan", planPath, "--dry-run"}, "http://127.0.0.1:0", "tok", &dryOut, &dryErr)
	if code != 0 {
		t.Fatalf("dry-run failed (%d): %s", code, dryErr.String())
	}
	if !strings.Contains(dryOut.String(), "Memories discovered: 2") {
		t.Fatalf("unexpected dry-run output:\n%s", dryOut.String())
	}

	// Real sync: skips docs/existing, stores docs/new, creates TASK-1, completes TASK-2
	var out, errOut bytes.Buffer
	code = run([]string{"sync", "demo", "--path", root, "--plan", planPath}, srv.URL, "tok", &out, &errOut)
	if code != 0 {
		t.Fatalf("sync failed (%d): %s", code, errOut.String())
	}

	if len(storedKeys) != 1 || storedKeys[0] != "docs/new" {
		t.Fatalf("expected only docs/new to be stored, got %v", storedKeys)
	}
	if len(createdTasks) != 1 || createdTasks[0] != "TASK-1" {
		t.Fatalf("expected TASK-1 to be created, got %v", createdTasks)
	}
	if len(completedTasks) != 1 || completedTasks[0] != "TASK-2" {
		t.Fatalf("expected TASK-2 to be reconciled as completed, got %v", completedTasks)
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

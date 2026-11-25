package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivan/engram/internal/mcp"
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

	// 3. Clear env for directory tests
	t.Setenv("ENGRAM_PROJECT_ID", "")
	baseID := detectProjectID("", tmpDir)
	if baseID != filepath.Base(tmpDir) {
		t.Fatalf("expected directory basename %s, got %s", filepath.Base(tmpDir), baseID)
	}
}

func TestDetectProjectID_GitWorktreeParentEngramJSON(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	base := t.TempDir()
	containerDir := filepath.Join(base, "checamos.mx")
	mainRepo := filepath.Join(containerDir, "checamos-api")
	worktreeParent := filepath.Join(base, "orca", "workspaces", "checamos-api")
	worktreeDir := filepath.Join(worktreeParent, "feat-2-1")

	if err := os.MkdirAll(mainRepo, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeParent, 0755); err != nil {
		t.Fatal(err)
	}

	// Place .engram.json in container root above mainRepo (not inside worktree)
	if err := os.WriteFile(filepath.Join(containerDir, ".engram.json"), []byte(`{"project_id":"checamos"}`), 0644); err != nil {
		t.Fatal(err)
	}

	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	runGit(mainRepo, "init", "-b", "main")
	runGit(mainRepo, "config", "user.email", "test@example.com")
	runGit(mainRepo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(mainRepo, "README.md"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(mainRepo, "add", ".")
	runGit(mainRepo, "commit", "-m", "init")
	runGit(mainRepo, "worktree", "add", "-b", "feat-2-1", worktreeDir)

	t.Setenv("ENGRAM_PROJECT_ID", "")
	got := detectProjectID("", worktreeDir)
	if got != "checamos" {
		t.Fatalf("expected worktree to resolve 'checamos' from parent container .engram.json, got %q", got)
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

func TestCLI_HelpFlags(t *testing.T) {
	cases := [][]string{
		{"help"},
		{"-h"},
		{"--help"},
		{"store", "--help"},
		{"store", "-h"},
		{"task", "--help"},
		{"task", "ls", "--help"},
		{"task", "create", "--help"},
		{"task", "renew", "--help"},
		{"task", "complete", "--help"},
		{"task", "fail", "--help"},
		{"handoff", "--help"},
		{"project", "--help"},
		{"init", "--help"},
		{"manifest", "--help"},
		{"recall", "--help"},
		{"search", "--help"},
		{"blame", "--help"},
		{"claim", "--help"},
	}

	for _, args := range cases {
		var out, errOut bytes.Buffer
		code := run(args, "http://127.0.0.1:0", "tok", &out, &errOut)
		if code != 0 {
			t.Errorf("args %v returned exit code %d (stderr: %s)", args, code, errOut.String())
		}
		if out.Len() == 0 {
			t.Errorf("args %v produced empty help output", args)
		}
	}

	var out, errOut bytes.Buffer
	if code := run([]string{"unknown-cmd"}, "http://127.0.0.1:0", "tok", &out, &errOut); code != 1 {
		t.Fatalf("expected exit 1 for unknown command, got %d", code)
	}
}

type recordedRPC struct {
	Name      string
	Arguments map[string]interface{}
}

func newMockMCPServer(t *testing.T, handler func(name string, args map[string]interface{}) (string, bool)) (*httptest.Server, *[]recordedRPC) {
	t.Helper()
	var calls []recordedRPC

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req mcp.JSONRPCRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("invalid rpc body: %v", err)
		}
		var callReq mcp.CallToolRequest
		if err := json.Unmarshal(req.Params, &callReq); err != nil {
			t.Fatalf("invalid tools/call params: %v", err)
		}
		var argsMap map[string]interface{}
		_ = json.Unmarshal(callReq.Arguments, &argsMap)
		calls = append(calls, recordedRPC{Name: callReq.Name, Arguments: argsMap})

		text, isErr := handler(callReq.Name, argsMap)
		resp := mcp.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcp.CallToolResult{
				Content: []mcp.ToolContent{{Type: "text", Text: text}},
				IsError: isErr,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return srv, &calls
}

func TestCLI_StoreFlagsOCCAuthorshipAndFileTrim(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "contract.md")
	if err := os.WriteFile(tmpFile, []byte("# Contract\nLine 2\n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	srv, calls := newMockMCPServer(t, func(name string, args map[string]interface{}) (string, bool) {
		if name == "blame_memory" {
			return `{"current_hash":"abc123head"}`, false
		}
		return `{"revision_number":2}`, false
	})
	defer srv.Close()

	t.Setenv("ENGRAM_AUTHOR", "saherla")
	t.Setenv("ENGRAM_AGENT_MODEL", "claude-opus-5-5")
	t.Setenv("ENGRAM_MACHINE_ID", "orca-worker-1")

	var out, errOut bytes.Buffer
	code := run([]string{
		"store", "checamos", "auth-contract", tmpFile,
		"--rationale", "- update from file",
		"--occ",
	}, srv.URL, "tok", &out, &errOut)
	if code != 0 {
		t.Fatalf("store failed (code=%d): %s", code, errOut.String())
	}

	if len(*calls) != 2 {
		t.Fatalf("expected 2 RPC calls (blame_memory + store_memory), got %d", len(*calls))
	}
	if (*calls)[0].Name != "blame_memory" {
		t.Fatalf("expected first call blame_memory, got %s", (*calls)[0].Name)
	}
	storeCall := (*calls)[1]
	if storeCall.Name != "store_memory" {
		t.Fatalf("expected second call store_memory, got %s", storeCall.Name)
	}
	if storeCall.Arguments["content"] != "# Contract\nLine 2" {
		t.Fatalf("expected trimmed trailing newlines, got %q", storeCall.Arguments["content"])
	}
	if storeCall.Arguments["expected_parent_hash"] != "abc123head" {
		t.Fatalf("expected expected_parent_hash='abc123head', got %v", storeCall.Arguments["expected_parent_hash"])
	}
	if storeCall.Arguments["author_user"] != "saherla" {
		t.Fatalf("expected author_user='saherla', got %v", storeCall.Arguments["author_user"])
	}
	if storeCall.Arguments["author_agent_model"] != "claude-opus-5-5" {
		t.Fatalf("expected author_agent_model='claude-opus-5-5', got %v", storeCall.Arguments["author_agent_model"])
	}
	if storeCall.Arguments["machine_id"] != "orca-worker-1" {
		t.Fatalf("expected machine_id='orca-worker-1', got %v", storeCall.Arguments["machine_id"])
	}
	if storeCall.Arguments["rationale"] != "- update from file" {
		t.Fatalf("expected rationale='- update from file', got %v", storeCall.Arguments["rationale"])
	}
}

func TestCLI_StoreExplicitExpectedParentHash(t *testing.T) {
	srv, calls := newMockMCPServer(t, func(name string, args map[string]interface{}) (string, bool) {
		return `{"revision_number":2}`, false
	})
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := run([]string{
		"store", "p1", "k1", "new text",
		"--rationale", "occ update",
		"--expected-parent-hash", "deadbeef",
		"--author", "ivan",
		"--model", "gemini-4-carbon",
	}, srv.URL, "tok", &out, &errOut)
	if code != 0 {
		t.Fatalf("store failed: %s", errOut.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("expected 1 RPC call, got %d", len(*calls))
	}
	args := (*calls)[0].Arguments
	if args["expected_parent_hash"] != "deadbeef" {
		t.Fatalf("expected deadbeef, got %v", args["expected_parent_hash"])
	}
	if args["author_user"] != "ivan" || args["author_agent_model"] != "gemini-4-carbon" {
		t.Fatalf("unexpected authorship: %v", args)
	}
}

func TestCLI_TaskLifecycleAndHandoffComplete(t *testing.T) {
	srv, calls := newMockMCPServer(t, func(name string, args map[string]interface{}) (string, bool) {
		if name == "list_tasks" {
			return `[{"id":"CU-1","title":"Subtask 2.1","status":"in_progress","priority":10,"leased_by":"saherla","updated_at":"2026-11-25T00:00:00Z"}]`, false
		}
		return `{"ok":true}`, false
	})
	defer srv.Close()

	var out, errOut bytes.Buffer

	// 1. task create
	if code := run([]string{"task", "create", "checamos", "CU-1", "Subtask 2.1", "--priority", "10", "--desc", "Implement"}, srv.URL, "tok", &out, &errOut); code != 0 {
		t.Fatalf("task create failed: %s", errOut.String())
	}
	// 2. task ls
	out.Reset()
	if code := run([]string{"task", "ls", "checamos", "--status", "in_progress"}, srv.URL, "tok", &out, &errOut); code != 0 {
		t.Fatalf("task ls failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "CU-1") || !strings.Contains(out.String(), "saherla") {
		t.Fatalf("unexpected task ls output: %s", out.String())
	}
	// 3. task renew
	if code := run([]string{"task", "renew", "checamos", "CU-1", "--lease", "lease-uuid-1", "--ttl", "600"}, srv.URL, "tok", &out, &errOut); code != 0 {
		t.Fatalf("task renew failed: %s", errOut.String())
	}
	// 4. task complete
	if code := run([]string{"task", "complete", "checamos", "CU-1", "--lease", "lease-uuid-1"}, srv.URL, "tok", &out, &errOut); code != 0 {
		t.Fatalf("task complete failed: %s", errOut.String())
	}
	// 5. task fail
	if code := run([]string{"task", "fail", "checamos", "CU-1", "--reason", "boom"}, srv.URL, "tok", &out, &errOut); code != 0 {
		t.Fatalf("task fail failed: %s", errOut.String())
	}
	// 6. handoff --complete --lease --from-agent
	if code := run([]string{
		"handoff", "checamos", "CU-1", "pass", "Done", "go test ./... passed",
		"--from-agent", "claude-opus",
		"--complete",
		"--lease", "lease-uuid-1",
	}, srv.URL, "tok", &out, &errOut); code != 0 {
		t.Fatalf("handoff --complete failed: %s", errOut.String())
	}

	// Verify handoff --lease without --complete fails cleanly
	errOut.Reset()
	if code := run([]string{
		"handoff", "checamos", "CU-1", "PASS", "Done", "tests", "--lease", "lease-uuid-1",
	}, srv.URL, "tok", &out, &errOut); code == 0 {
		t.Fatalf("expected error when --lease is passed without --complete")
	}

	last := (*calls)[len(*calls)-1]
	if last.Name != "record_handoff" {
		t.Fatalf("expected record_handoff, got %s", last.Name)
	}
	if last.Arguments["from_agent"] != "claude-opus" {
		t.Fatalf("expected from_agent=claude-opus, got %v", last.Arguments["from_agent"])
	}
	if last.Arguments["complete_task"] != true || last.Arguments["lease_id"] != "lease-uuid-1" {
		t.Fatalf("expected complete_task=true and lease_id=lease-uuid-1, got %v", last.Arguments)
	}
}

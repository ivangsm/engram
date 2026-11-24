package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ivan/engram/internal/mcp"
)

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func main() {
	serverURL := getEnv("ENGRAM_SERVER_URL", "http://localhost:8085")
	token := getEnv("ENGRAM_AUTH_TOKEN", "engram_dev_token_2026")

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "init":
		runInit(serverURL, token, args)
	case "project":
		runProject(serverURL, token, args)
	case "manifest":
		runManifest(serverURL, token, args)
	case "recall":
		runRecall(serverURL, token, args)
	case "search":
		runSearch(serverURL, token, args)
	case "store":
		runStore(serverURL, token, args)
	case "blame":
		runBlame(serverURL, token, args)
	case "claim":
		runClaim(serverURL, token, args)
	case "handoff":
		runHandoff(serverURL, token, args)
	case "version":
		fmt.Println("engram CLI v0.1.0")
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`Engram CLI - High-density memory & state plane for AI agents and engineers

Usage:
  engram <command> [arguments]

Commands:
  init [--project <id>] [--name <name>] [--docs <dir>] [--todo <file>]
      Auto-detect topology, register project namespace in daemon, and write .engram.json

  project <ls|get|create|rm> [arguments]
      Manage project namespaces in daemon
        engram project ls
        engram project get <id>
        engram project create <id> [--name <name>] [--desc <desc>]
        engram project rm <id> --confirm <id> [--purge]

  manifest <project_id> [--scope <scope>]
      List skeletal memory manifest (zero token dump)

  recall <project_id> <key>
      Hydrate full memory content for a specific key

  search <project_id> <query> [--limit <n>]
      Search memories using hybrid vector + fulltext rank

  store <project_id> <key> <content_or_file> --rationale <reason> [--scope <scope>] [--task <task_id>]
      Store or version a memory with cryptographic blame provenance

  blame <project_id> <key>
      Audit revision history, authors, models, rationales, diffs and hash chain

  claim <project_id> [task_id] [--leased-by <id>] [--ttl <seconds>]
      Claim next available task or specific task lease using row-level locking

  handoff <project_id> <task_id> <verdict> <summary> <evidence>
      Record an immutable handoff (<150 words summary, verified evidence)

Environment Variables:
  ENGRAM_SERVER_URL   Target daemon URL (default: http://localhost:8085)
  ENGRAM_AUTH_TOKEN   Bearer token for daemon access
`)
}

func callTool(serverURL, token, toolName string, toolArgs interface{}) (*mcp.CallToolResult, error) {
	argsBytes, err := json.Marshal(toolArgs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool args: %w", err)
	}

	rpcReq := mcp.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: json.RawMessage(fmt.Sprintf(`{"name": "%s", "arguments": %s}`,
			toolName, string(argsBytes))),
	}

	reqBytes, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal RPC request: %w", err)
	}

	req, err := http.NewRequest("POST", serverURL+"/mcp/message", bytes.NewReader(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon connection failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	var rpcResp mcp.JSONRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("failed to decode RPC response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error [%d]: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	resultBytes, _ := json.Marshal(rpcResp.Result)
	var callResult mcp.CallToolResult
	if err := json.Unmarshal(resultBytes, &callResult); err != nil {
		return nil, fmt.Errorf("failed to decode tool result: %w", err)
	}

	return &callResult, nil
}

func printToolResult(res *mcp.CallToolResult, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if res.IsError {
		for _, c := range res.Content {
			fmt.Fprintf(os.Stderr, "Error: %s\n", c.Text)
		}
		os.Exit(1)
	}
	for _, c := range res.Content {
		fmt.Println(c.Text)
	}
}

func runManifest(serverURL, token string, args []string) {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	scope := fs.String("scope", "", "Scope filter")
	_ = parseFlagsAndArgs(fs, args)

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: engram manifest <project_id> [--scope <scope>]\n")
		os.Exit(1)
	}
	projectID := fs.Arg(0)

	res, err := callTool(serverURL, token, "list_memory_manifest", map[string]string{
		"project_id": projectID,
		"scope":      *scope,
	})
	printToolResult(res, err)
}

func runRecall(serverURL, token string, args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: engram recall <project_id> <key>\n")
		os.Exit(1)
	}
	projectID := args[0]
	key := args[1]

	res, err := callTool(serverURL, token, "recall_memory", map[string]string{
		"project_id": projectID,
		"key":        key,
	})
	printToolResult(res, err)
}

func runSearch(serverURL, token string, args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	limit := fs.Int("limit", 10, "Maximum results")
	_ = parseFlagsAndArgs(fs, args)

	if fs.NArg() < 2 {
		fmt.Fprintf(os.Stderr, "Usage: engram search <project_id> <query> [--limit <n>]\n")
		os.Exit(1)
	}
	projectID := fs.Arg(0)
	query := fs.Arg(1)

	res, err := callTool(serverURL, token, "search_memory", map[string]interface{}{
		"project_id": projectID,
		"query":      query,
		"limit":      *limit,
	})
	printToolResult(res, err)
}

func runStore(serverURL, token string, args []string) {
	fs := flag.NewFlagSet("store", flag.ExitOnError)
	rationale := fs.String("rationale", "", "Technical justification for change (required)")
	scope := fs.String("scope", "global", "Scope namespace")
	taskID := fs.String("task", "", "Associated task ID")
	digest := fs.String("digest", "", "Single-line high-density summary")
	_ = parseFlagsAndArgs(fs, args)

	if fs.NArg() < 3 || *rationale == "" {
		fmt.Fprintf(os.Stderr, "Usage: engram store <project_id> <key> <content_or_file> --rationale <reason> [--scope <scope>] [--task <task_id>]\n")
		os.Exit(1)
	}

	projectID := fs.Arg(0)
	key := fs.Arg(1)
	contentOrFile := fs.Arg(2)

	content := contentOrFile
	// Check if contentOrFile is a valid file path
	if fileBytes, err := os.ReadFile(contentOrFile); err == nil {
		content = string(fileBytes)
	}

	res, err := callTool(serverURL, token, "store_memory", map[string]string{
		"project_id": projectID,
		"key":        key,
		"content":    content,
		"digest":     *digest,
		"rationale":  *rationale,
		"scope":      *scope,
		"task_id":    *taskID,
	})
	printToolResult(res, err)
}

func runBlame(serverURL, token string, args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: engram blame <project_id> <key>\n")
		os.Exit(1)
	}
	projectID := args[0]
	key := args[1]

	res, err := callTool(serverURL, token, "blame_memory", map[string]string{
		"project_id": projectID,
		"key":        key,
	})
	printToolResult(res, err)
}

func runClaim(serverURL, token string, args []string) {
	fs := flag.NewFlagSet("claim", flag.ExitOnError)
	leasedBy := fs.String("leased-by", "", "Agent holding lease")
	ttl := fs.Int("ttl", 300, "Lease TTL in seconds")
	_ = parseFlagsAndArgs(fs, args)

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: engram claim <project_id> [task_id] [--leased-by <id>] [--ttl <seconds>]\n")
		os.Exit(1)
	}
	projectID := fs.Arg(0)
	taskID := ""
	if fs.NArg() >= 2 {
		taskID = fs.Arg(1)
	}

	payload := map[string]interface{}{
		"project_id":  projectID,
		"task_id":     taskID,
		"ttl_seconds": *ttl,
	}
	if *leasedBy != "" {
		payload["leased_by"] = *leasedBy
	}

	res, err := callTool(serverURL, token, "claim_task", payload)
	printToolResult(res, err)
}

func runHandoff(serverURL, token string, args []string) {
	if len(args) < 5 {
		fmt.Fprintf(os.Stderr, "Usage: engram handoff <project_id> <task_id> <verdict> <summary> <evidence>\n")
		os.Exit(1)
	}
	projectID := args[0]
	taskID := args[1]
	verdict := strings.ToUpper(args[2])
	summary := args[3]
	evidence := args[4]

	fromAgent := getEnv("USER", "engineer")

	res, err := callTool(serverURL, token, "record_handoff", map[string]string{
		"project_id":            projectID,
		"task_id":               taskID,
		"from_agent":            fromAgent,
		"verdict":               verdict,
		"summary":               summary,
		"reproducible_evidence": evidence,
	})
	printToolResult(res, err)
}

// -----------------------------------------------------------------------------
// Project & Init Commands (Fase 1)
// -----------------------------------------------------------------------------

type DocsConfig struct {
	Root     string   `json:"root"`
	TodoFile string   `json:"todo_file,omitempty"`
	Sources  []string `json:"sources,omitempty"`
}

type ProjectSettings struct {
	Docs DocsConfig `json:"docs"`
}

type EngramConfig struct {
	ProjectID string     `json:"project_id"`
	Name      string     `json:"name"`
	ServerURL string     `json:"server_url"`
	Docs      DocsConfig `json:"docs"`
	CreatedAt time.Time  `json:"created_at"`
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func detectGitRemoteSlug(rootDir string) string {
	cmd := exec.Command("git", "-C", rootDir, "config", "--get", "remote.origin.url")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return ""
	}
	raw = strings.TrimSuffix(raw, ".git")
	raw = strings.TrimSuffix(raw, "/")

	if idx := strings.LastIndex(raw, ":"); idx != -1 {
		raw = raw[idx+1:]
	}
	if idx := strings.LastIndex(raw, "/"); idx != -1 {
		raw = raw[idx+1:]
	}
	return raw
}

func detectGitOrDirBasename(rootDir string) string {
	cmd := exec.Command("git", "-C", rootDir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err == nil {
		top := strings.TrimSpace(string(out))
		if top != "" {
			return filepath.Base(top)
		}
	}
	abs, err := filepath.Abs(rootDir)
	if err == nil {
		base := filepath.Base(abs)
		if base != "." && base != "/" && base != "" {
			return base
		}
	}
	return "default"
}

func detectProjectID(flagProject, rootDir string) string {
	if flagProject != "" {
		return strings.TrimSpace(flagProject)
	}
	if env := os.Getenv("ENGRAM_PROJECT_ID"); env != "" {
		return strings.TrimSpace(env)
	}
	if slug := detectGitRemoteSlug(rootDir); slug != "" {
		return slug
	}
	return detectGitOrDirBasename(rootDir)
}

func detectTopology(rootDir, flagDocs, flagTodo string) DocsConfig {
	var docs DocsConfig

	// Manual override via --docs
	if flagDocs != "" {
		docs.Root = flagDocs
		if flagTodo != "" {
			docs.TodoFile = flagTodo
		} else if fileExists(filepath.Join(rootDir, flagDocs, "TODO.md")) {
			docs.TodoFile = "TODO.md"
		} else if fileExists(filepath.Join(rootDir, "TODO.md")) {
			docs.TodoFile = "TODO.md"
		}
		return docs
	}

	entries, err := os.ReadDir(rootDir)
	if err != nil {
		docs.Root = "docs"
		if flagTodo != "" {
			docs.TodoFile = flagTodo
		}
		return docs
	}

	// Rule A: Check for directory containing "docs" (case-insensitive)
	var matchedDir string
	for _, entry := range entries {
		if entry.IsDir() {
			nameLower := strings.ToLower(entry.Name())
			if nameLower == "docs" {
				matchedDir = entry.Name()
				break
			}
			if strings.Contains(nameLower, "docs") && matchedDir == "" {
				matchedDir = entry.Name()
			}
		}
	}

	if matchedDir != "" {
		docs.Root = matchedDir
		if flagTodo != "" {
			docs.TodoFile = flagTodo
		} else if fileExists(filepath.Join(rootDir, matchedDir, "TODO.md")) {
			docs.TodoFile = "TODO.md"
		} else if fileExists(filepath.Join(rootDir, "TODO.md")) {
			docs.TodoFile = "TODO.md"
		}
		return docs
	}

	// Rule B: Check for loose markdown files in root or .serena/memories
	var markdownFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			markdownFiles = append(markdownFiles, entry.Name())
		}
	}

	serenaMemoriesPath := filepath.Join(rootDir, ".serena", "memories")
	serenaPath := filepath.Join(rootDir, ".serena")
	hasSerena := dirExists(serenaMemoriesPath) || dirExists(serenaPath)

	if len(markdownFiles) > 0 || hasSerena {
		docs.Root = "."
		if flagTodo != "" {
			docs.TodoFile = flagTodo
		} else {
			docs.TodoFile = "TODO.md"
		}

		var sources []string
		for _, mf := range markdownFiles {
			sources = append(sources, mf)
		}
		if dirExists(serenaMemoriesPath) {
			sources = append(sources, ".serena/memories")
		} else if dirExists(serenaPath) {
			sources = append(sources, ".serena")
		}
		docs.Sources = sources
		return docs
	}

	// Rule C: Default fallback
	docs.Root = "docs"
	if flagTodo != "" {
		docs.TodoFile = flagTodo
	}
	return docs
}

func runInit(serverURL, token string, args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	projectFlag := fs.String("project", "", "Project ID override")
	nameFlag := fs.String("name", "", "Display name of the project")
	docsFlag := fs.String("docs", "", "Documentation root directory override")
	todoFlag := fs.String("todo", "", "TODO file override")
	_ = parseFlagsAndArgs(fs, args)

	rootDir := "."
	projectID := detectProjectID(*projectFlag, rootDir)
	name := strings.TrimSpace(*nameFlag)
	if name == "" {
		name = projectID
	}

	docs := detectTopology(rootDir, *docsFlag, *todoFlag)
	settings := ProjectSettings{Docs: docs}

	payload := map[string]interface{}{
		"project_id":  projectID,
		"name":        name,
		"description": fmt.Sprintf("Project %s initialized by engram init", projectID),
		"settings":    settings,
	}

	res, err := callTool(serverURL, token, "create_project", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error registering project in daemon: %v\n", err)
		os.Exit(1)
	}
	if res.IsError {
		errMsg := "Unknown error"
		if len(res.Content) > 0 {
			errMsg = res.Content[0].Text
		}
		fmt.Fprintf(os.Stderr, "Daemon error: %s\n", errMsg)
		os.Exit(1)
	}

	// Write .engram.json
	cfg := EngramConfig{
		ProjectID: projectID,
		Name:      name,
		ServerURL: serverURL,
		Docs:      docs,
		CreatedAt: time.Now().UTC(),
	}
	cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling .engram.json: %v\n", err)
		os.Exit(1)
	}

	cfgPath := filepath.Join(rootDir, ".engram.json")
	if err := os.WriteFile(cfgPath, append(cfgBytes, '\n'), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", cfgPath, err)
		os.Exit(1)
	}

	fmt.Println("Engram project initialized successfully:")
	fmt.Printf("  Project ID: %s\n", projectID)
	fmt.Printf("  Name:       %s\n", name)
	fmt.Printf("  Server:     %s\n", serverURL)
	fmt.Printf("  Docs Root:  %s\n", docs.Root)
	if docs.TodoFile != "" {
		fmt.Printf("  TODO File:  %s\n", docs.TodoFile)
	}
	if len(docs.Sources) > 0 {
		fmt.Printf("  Sources:    %s\n", strings.Join(docs.Sources, ", "))
	}
	fmt.Printf("  Config:     %s\n", cfgPath)
}

func printProjectUsage() {
	fmt.Print(`Engram Project Management

Usage:
  engram project <command> [arguments]

Commands:
  ls
      List active projects
  get <project_id>
      Show project details
  create <project_id> [--name <name>] [--desc <desc>]
      Create or update project namespace
  rm <project_id> --confirm <project_id> [--purge]
      Delete project namespace (soft-delete by default, or hard purge)
`)
}

func runProject(serverURL, token string, args []string) {
	if len(args) < 1 {
		printProjectUsage()
		os.Exit(1)
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "ls", "list":
		runProjectList(serverURL, token, subargs)
	case "get":
		runProjectGet(serverURL, token, subargs)
	case "create":
		runProjectCreate(serverURL, token, subargs)
	case "rm", "delete":
		runProjectRm(serverURL, token, subargs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown project subcommand: %s\n\n", subcmd)
		printProjectUsage()
		os.Exit(1)
	}
}

type projectSummary struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Settings    json.RawMessage `json:"settings"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func runProjectList(serverURL, token string, args []string) {
	res, err := callTool(serverURL, token, "list_projects", map[string]interface{}{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if res.IsError {
		for _, c := range res.Content {
			fmt.Fprintf(os.Stderr, "Error: %s\n", c.Text)
		}
		os.Exit(1)
	}

	if len(res.Content) == 0 || res.Content[0].Text == "" {
		fmt.Println("No projects found.")
		return
	}

	var projs []projectSummary
	if err := json.Unmarshal([]byte(res.Content[0].Text), &projs); err != nil {
		fmt.Println(res.Content[0].Text)
		return
	}

	if len(projs) == 0 {
		fmt.Println("No projects found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 3, ' ', 0)
	fmt.Fprintln(w, "PROJECT ID\tNAME\tDOCS ROOT\tUPDATED AT")
	for _, p := range projs {
		docsRoot := "-"
		var s struct {
			Docs struct {
				Root string `json:"root"`
			} `json:"docs"`
		}
		if len(p.Settings) > 0 && json.Unmarshal(p.Settings, &s) == nil && s.Docs.Root != "" {
			docsRoot = s.Docs.Root
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			p.ID,
			p.Name,
			docsRoot,
			p.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
		)
	}
	_ = w.Flush()
}

func runProjectGet(serverURL, token string, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: engram project get <project_id>\n")
		os.Exit(1)
	}
	projectID := args[0]
	res, err := callTool(serverURL, token, "get_project", map[string]string{
		"project_id": projectID,
	})
	printToolResult(res, err)
}

func parseFlagsAndArgs(fs *flag.FlagSet, args []string) error {
	var flags []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			flagName := strings.TrimLeft(arg, "-")
			if strings.Contains(flagName, "=") {
				continue
			}
			f := fs.Lookup(flagName)
			// If it's a known boolean flag, it doesn't consume the next argument
			if f != nil {
				if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
					continue
				}
			}
			// Otherwise, consume next argument if available
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positionals = append(positionals, arg)
		}
	}
	reordered := append(flags, positionals...)
	return fs.Parse(reordered)
}

func runProjectCreate(serverURL, token string, args []string) {
	fs := flag.NewFlagSet("project create", flag.ExitOnError)
	nameFlag := fs.String("name", "", "Display name of project")
	descFlag := fs.String("desc", "", "Description of project")
	_ = parseFlagsAndArgs(fs, args)

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: engram project create <project_id> [--name <name>] [--desc <desc>]\n")
		os.Exit(1)
	}
	projectID := fs.Arg(0)
	name := strings.TrimSpace(*nameFlag)
	if name == "" {
		name = projectID
	}

	payload := map[string]interface{}{
		"project_id":  projectID,
		"name":        name,
		"description": *descFlag,
	}
	res, err := callTool(serverURL, token, "create_project", payload)
	printToolResult(res, err)
}

func runProjectRm(serverURL, token string, args []string) {
	fs := flag.NewFlagSet("project rm", flag.ExitOnError)
	confirmFlag := fs.String("confirm", "", "Must match the project ID to confirm deletion")
	purgeFlag := fs.Bool("purge", false, "Hard delete (purge) from database instead of soft-delete")
	_ = parseFlagsAndArgs(fs, args)

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: engram project rm <project_id> --confirm <project_id> [--purge]\n")
		os.Exit(1)
	}
	projectID := fs.Arg(0)

	if *confirmFlag == "" {
		fmt.Fprintf(os.Stderr, "Error: --confirm <project_id> is required for deletion\n")
		os.Exit(1)
	}
	if *confirmFlag != projectID {
		fmt.Fprintf(os.Stderr, "Error: confirmation mismatch: --confirm '%s' does not match '%s'\n", *confirmFlag, projectID)
		os.Exit(1)
	}

	payload := map[string]interface{}{
		"project_id":         projectID,
		"confirm_project_id": *confirmFlag,
		"purge":              *purgeFlag,
	}
	res, err := callTool(serverURL, token, "delete_project", payload)
	printToolResult(res, err)
}

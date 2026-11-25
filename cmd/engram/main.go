package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	os.Exit(run(os.Args[1:], serverURL, token, os.Stdout, os.Stderr))
}

func run(args []string, serverURL, token string, out, errOut io.Writer) int {
	if len(args) < 1 {
		printUsage(errOut)
		return 1
	}

	cmd := args[0]
	subargs := args[1:]

	switch cmd {
	case "help", "-h", "--help":
		printUsage(out)
		return 0
	case "init":
		return runInit(serverURL, token, subargs, out, errOut)
	case "sync":
		return runSync(serverURL, token, subargs, out, errOut)
	case "project":
		return runProject(serverURL, token, subargs, out, errOut)
	case "manifest":
		return runManifest(serverURL, token, subargs, out, errOut)
	case "recall":
		return runRecall(serverURL, token, subargs, out, errOut)
	case "search":
		return runSearch(serverURL, token, subargs, out, errOut)
	case "store":
		return runStore(serverURL, token, subargs, out, errOut)
	case "blame":
		return runBlame(serverURL, token, subargs, out, errOut)
	case "claim":
		return runClaim(serverURL, token, subargs, out, errOut)
	case "task":
		return runTask(serverURL, token, subargs, out, errOut)
	case "handoff":
		return runHandoff(serverURL, token, subargs, out, errOut)
	case "version", "-v", "--version":
		_, _ = fmt.Fprintln(out, "engram CLI v0.1.0")
		return 0
	default:
		_, _ = fmt.Fprintf(errOut, "Unknown command: %s\n\n", cmd)
		printUsage(errOut)
		return 1
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Engram CLI - High-density memory & state plane for AI agents and engineers

Usage:
  engram <command> [arguments]

Commands:
  init [--project <id>] [--name <name>] [--docs <dir>] [--todo <file>]
      Auto-detect topology, register project namespace in daemon, and write .engram.json

  sync [project_id] [--path <dir>] [--docs <dir>] [--dry-run] [--only <sources>]
      Auto-discover and sync docs, Serena memories, EGM READMEs, rules, and tasks (PND-*/TODO.md)

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

  store <project_id> <key> <content_or_file> --rationale <reason> [options]
      Store or version a memory with cryptographic blame provenance
      Options:
        --digest <summary>                Single-line digest (preserves prior digest on update if omitted)
        --scope <scope>                   Scope namespace (default: global)
        --task <task_id>                  Associated task ID
        --expected-parent-hash <sha256>   Expected parent hash for strict OCC
        --occ                             Auto-read current hash and enforce OCC
        --author <user>                   Author identifier (default: $ENGRAM_AUTHOR or $USER)
        --model <model_id>                Agent model identifier (default: $ENGRAM_AGENT_MODEL)
        --machine-id <host>               Machine hostname override (default: $ENGRAM_MACHINE_ID or hostname)

  blame <project_id> <key>
      Audit revision history, authors, models, rationales, diffs and hash chain

  task <ls|get|create|claim|renew|complete|fail> [arguments]
      Manage operational tasks and leases in the state plane
        engram task ls <project_id> [--status <status>] [--limit <n>] [--json]
        engram task get <project_id> <task_id>
        engram task create <project_id> <task_id> <title> [--desc <text>] [--priority <n>] [--metadata <json>]
        engram task claim <project_id> [task_id] [--leased-by <id>] [--ttl <seconds>]
        engram task renew <project_id> <task_id> --lease <lease_id> [--ttl <seconds>]
        engram task complete <project_id> <task_id> [--lease <lease_id>]
        engram task fail <project_id> <task_id> [--lease <lease_id>] [--reason <text>]

  claim <project_id> [task_id] [--leased-by <id>] [--ttl <seconds>]
      Alias for 'engram task claim'

  handoff <project_id> <task_id> <verdict> <summary> <evidence> [options]
      Record an immutable handoff (<150 words summary, verified evidence)
      Options:
        --from-agent <id>    Source agent (default: $ENGRAM_AUTHOR or $USER)
        --to-agent <id>      Target agent (optional)
        --artifacts <json>   JSON array of changed paths/PRs
        --complete           Atomically mark task completed in the same transaction
        --lease <lease_id>   Lease UUID token for fenced completion (with --complete)

  help, -h, --help
      Show this help message (use 'engram <command> --help' for command details)

Environment Variables:
  ENGRAM_SERVER_URL    Target daemon URL (default: http://localhost:8085)
  ENGRAM_AUTH_TOKEN    Bearer token for daemon access
  ENGRAM_PROJECT_ID    Default project identifier override
  ENGRAM_AUTHOR        Default author / agent user for store and handoff
  ENGRAM_AGENT_MODEL   Default LLM model identifier for cryptographic blame
  ENGRAM_MACHINE_ID    Machine identifier override (default: client hostname)
`)
}

func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

func parseSubcommandFlags(fs *flag.FlagSet, args []string, usageText string, out, errOut io.Writer) (helpShown bool, ok bool) {
	if wantsHelp(args) {
		_, _ = fmt.Fprint(out, usageText)
		return true, true
	}
	fs.SetOutput(io.Discard)
	if err := parseFlagsAndArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(out, usageText)
			return true, true
		}
		_, _ = fmt.Fprintf(errOut, "Error: %v\n\n%s", err, usageText)
		return false, false
	}
	return false, true
}

var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

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

	endpoint := strings.TrimRight(serverURL, "/") + "/mcp/message"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := defaultHTTPClient.Do(req)
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

func printToolResult(res *mcp.CallToolResult, err error, out, errOut io.Writer) int {
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "Error: %v\n", err)
		return 1
	}
	if res.IsError {
		for _, c := range res.Content {
			_, _ = fmt.Fprintf(errOut, "Error: %s\n", c.Text)
		}
		return 1
	}
	for _, c := range res.Content {
		_, _ = fmt.Fprintln(out, c.Text)
	}
	return 0
}

func resolveAuthor(explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("ENGRAM_AUTHOR")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("USER")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("USERNAME")); v != "" {
		return v
	}
	return "unknown"
}

func resolveModel(explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("ENGRAM_AGENT_MODEL"))
}

func resolveMachineID(explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("ENGRAM_MACHINE_ID")); v != "" {
		return v
	}
	host, err := os.Hostname()
	if err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}
	return "unknown"
}

func runManifest(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram manifest <project_id> [--scope <scope>]\n"
	fs := flag.NewFlagSet("manifest", flag.ContinueOnError)
	scope := fs.String("scope", "", "Scope filter")
	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 1 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}
	projectID := fs.Arg(0)

	res, err := callTool(serverURL, token, "list_memory_manifest", map[string]string{
		"project_id": projectID,
		"scope":      *scope,
	})
	return printToolResult(res, err, out, errOut)
}

func runRecall(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram recall <project_id> <key>\n"
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 2 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}
	projectID := fs.Arg(0)
	key := fs.Arg(1)

	res, err := callTool(serverURL, token, "recall_memory", map[string]string{
		"project_id": projectID,
		"key":        key,
	})
	return printToolResult(res, err, out, errOut)
}

func runSearch(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram search <project_id> <query> [--limit <n>]\n"
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	limit := fs.Int("limit", 10, "Maximum results")
	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 2 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}
	projectID := fs.Arg(0)
	query := fs.Arg(1)

	res, err := callTool(serverURL, token, "search_memory", map[string]interface{}{
		"project_id": projectID,
		"query":      query,
		"limit":      *limit,
	})
	return printToolResult(res, err, out, errOut)
}

func runStore(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = `Usage: engram store <project_id> <key> <content_or_file> --rationale <reason> [options]

Options:
  --rationale <reason>              Technical justification for change (required)
  --digest <summary>                Single-line high-density digest (preserves prior digest on update if omitted)
  --scope <scope>                   Scope namespace (default: global)
  --task <task_id>                  Associated task ID
  --expected-parent-hash <sha256>   Expected parent content hash for strict OCC
  --occ                             Auto-fetch current hash and enforce OCC
  --author <user>                   Author user (default: $ENGRAM_AUTHOR or $USER)
  --model <model_id>                Agent model identifier (default: $ENGRAM_AGENT_MODEL)
  --machine-id <host>               Machine identifier (default: $ENGRAM_MACHINE_ID or hostname)
`
	fs := flag.NewFlagSet("store", flag.ContinueOnError)
	rationale := fs.String("rationale", "", "Technical justification for change (required)")
	scope := fs.String("scope", "global", "Scope namespace")
	taskID := fs.String("task", "", "Associated task ID")
	digest := fs.String("digest", "", "Single-line high-density summary")
	expectedParentHash := fs.String("expected-parent-hash", "", "Expected parent content hash for OCC")
	occ := fs.Bool("occ", false, "Auto-fetch current hash and validate OCC")
	author := fs.String("author", "", "Author username")
	model := fs.String("model", "", "Agent model identifier")
	machineID := fs.String("machine-id", "", "Machine identifier")

	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 3 || strings.TrimSpace(*rationale) == "" {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}

	projectID := fs.Arg(0)
	key := fs.Arg(1)
	contentOrFile := fs.Arg(2)

	content := contentOrFile
	if fileBytes, err := os.ReadFile(contentOrFile); err == nil {
		content = strings.TrimRight(string(fileBytes), "\r\n")
	}

	payload := map[string]interface{}{
		"project_id":  projectID,
		"key":         key,
		"content":     content,
		"digest":      *digest,
		"rationale":   *rationale,
		"scope":       *scope,
		"task_id":     *taskID,
		"author_user": resolveAuthor(*author),
		"machine_id":  resolveMachineID(*machineID),
	}

	if modelID := resolveModel(*model); modelID != "" {
		payload["author_agent_model"] = modelID
	}

	hasExplicitParentHash := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "expected-parent-hash" {
			hasExplicitParentHash = true
		}
	})

	if hasExplicitParentHash {
		payload["expected_parent_hash"] = *expectedParentHash
	} else if *occ {
		headHash, err := fetchCurrentHashForOCC(serverURL, token, projectID, key)
		if err != nil {
			_, _ = fmt.Fprintf(errOut, "Error resolving OCC hash: %v\n", err)
			return 1
		}
		payload["expected_parent_hash"] = headHash
	}

	res, err := callTool(serverURL, token, "store_memory", payload)
	return printToolResult(res, err, out, errOut)
}

func fetchCurrentHashForOCC(serverURL, token, projectID, key string) (string, error) {
	res, err := callTool(serverURL, token, "blame_memory", map[string]string{
		"project_id": projectID,
		"key":        key,
	})
	if err != nil {
		return "", err
	}
	if res.IsError {
		var msg string
		if len(res.Content) > 0 {
			msg = res.Content[0].Text
		}
		if strings.Contains(strings.ToLower(msg), "not found") {
			return "", nil
		}
		return "", errors.New(msg)
	}
	if len(res.Content) == 0 {
		return "", nil
	}
	var blame struct {
		CurrentHash string `json:"current_hash"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &blame); err != nil {
		return "", fmt.Errorf("invalid blame response: %w", err)
	}
	return blame.CurrentHash, nil
}

func runBlame(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram blame <project_id> <key>\n"
	fs := flag.NewFlagSet("blame", flag.ContinueOnError)
	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 2 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}
	projectID := fs.Arg(0)
	key := fs.Arg(1)

	res, err := callTool(serverURL, token, "blame_memory", map[string]string{
		"project_id": projectID,
		"key":        key,
	})
	return printToolResult(res, err, out, errOut)
}

func runClaim(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram claim <project_id> [task_id] [--leased-by <id>] [--ttl <seconds>]\n"
	fs := flag.NewFlagSet("claim", flag.ContinueOnError)
	leasedBy := fs.String("leased-by", "", "Agent holding lease (default: $ENGRAM_AUTHOR or $USER)")
	ttl := fs.Int("ttl", 300, "Lease TTL in seconds")
	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 1 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}
	projectID := fs.Arg(0)
	taskID := ""
	if fs.NArg() >= 2 {
		taskID = fs.Arg(1)
	}

	payload := map[string]interface{}{
		"project_id":  projectID,
		"task_id":     taskID,
		"leased_by":   resolveAuthor(*leasedBy),
		"ttl_seconds": *ttl,
	}

	res, err := callTool(serverURL, token, "claim_task", payload)
	return printToolResult(res, err, out, errOut)
}

func runHandoff(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = `Usage: engram handoff <project_id> <task_id> <verdict> <summary> <evidence> [options]

Options:
  --from-agent <id>    Source agent identifier (default: $ENGRAM_AUTHOR or $USER)
  --to-agent <id>      Target agent identifier
  --artifacts <json>   JSON array of changed files, PRs, or logs
  --complete           Atomically mark task completed in the same transaction
  --lease <lease_id>   Active lease UUID token for fenced completion (requires --complete)
`
	fs := flag.NewFlagSet("handoff", flag.ContinueOnError)
	fromAgent := fs.String("from-agent", "", "Source agent identifier")
	toAgent := fs.String("to-agent", "", "Target agent identifier")
	artifacts := fs.String("artifacts", "", "JSON serialized artifacts array")
	complete := fs.Bool("complete", false, "Atomically complete the task")
	var leaseID string
	fs.StringVar(&leaseID, "lease", "", "Active lease UUID token")
	fs.StringVar(&leaseID, "lease-id", "", "Active lease UUID token")

	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 5 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}
	if leaseID != "" && !*complete {
		_, _ = fmt.Fprintln(errOut, "Error: --lease requires --complete")
		return 1
	}

	projectID := fs.Arg(0)
	taskID := fs.Arg(1)
	verdict := strings.ToUpper(fs.Arg(2))
	summary := fs.Arg(3)
	evidence := fs.Arg(4)

	payload := map[string]interface{}{
		"project_id":            projectID,
		"task_id":               taskID,
		"from_agent":            resolveAuthor(*fromAgent),
		"verdict":               verdict,
		"summary":               summary,
		"reproducible_evidence": evidence,
	}
	if strings.TrimSpace(*toAgent) != "" {
		payload["to_agent"] = strings.TrimSpace(*toAgent)
	}
	if strings.TrimSpace(*artifacts) != "" {
		payload["artifacts"] = *artifacts
	}
	if *complete {
		payload["complete_task"] = true
		if leaseID != "" {
			payload["lease_id"] = leaseID
		}
	}

	res, err := callTool(serverURL, token, "record_handoff", payload)
	return printToolResult(res, err, out, errOut)
}

// -----------------------------------------------------------------------------
// Task Lifecycle Commands
// -----------------------------------------------------------------------------

func printTaskUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Engram Task Management

Usage:
  engram task <command> [arguments]

Commands:
  ls <project_id> [--status <status>] [--limit <n>] [--json]
      List tasks (optional status: pending, in_progress, completed, failed, blocked)
  get <project_id> <task_id>
      Show detailed task state and active lease metadata
  create <project_id> <task_id> <title> [--desc <text>] [--priority <n>] [--metadata <json>]
      Enqueue a new operational task
  claim <project_id> [task_id] [--leased-by <id>] [--ttl <seconds>]
      Atomically claim next available or specific task lease
  renew <project_id> <task_id> --lease <lease_id> [--ttl <seconds>]
      Extend active lease expiration (fenced by lease_id)
  complete <project_id> <task_id> [--lease <lease_id>]
      Mark task completed and release active lease
  fail <project_id> <task_id> [--lease <lease_id>] [--reason <text>]
      Mark task failed and record autopsy reason
`)
}

func runTask(serverURL, token string, args []string, out, errOut io.Writer) int {
	if len(args) < 1 {
		printTaskUsage(errOut)
		return 1
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "help", "-h", "--help":
		printTaskUsage(out)
		return 0
	case "ls", "list":
		return runTaskList(serverURL, token, subargs, out, errOut)
	case "get":
		return runTaskGet(serverURL, token, subargs, out, errOut)
	case "create":
		return runTaskCreate(serverURL, token, subargs, out, errOut)
	case "claim":
		return runClaim(serverURL, token, subargs, out, errOut)
	case "renew":
		return runTaskRenew(serverURL, token, subargs, out, errOut)
	case "complete":
		return runTaskComplete(serverURL, token, subargs, out, errOut)
	case "fail":
		return runTaskFail(serverURL, token, subargs, out, errOut)
	default:
		_, _ = fmt.Fprintf(errOut, "Unknown task subcommand: %s\n\n", subcmd)
		printTaskUsage(errOut)
		return 1
	}
}

type taskSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Priority  int32     `json:"priority"`
	LeasedBy  *string   `json:"leased_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

func runTaskList(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram task ls <project_id> [--status <status>] [--limit <n>] [--json]\n"
	fs := flag.NewFlagSet("task ls", flag.ContinueOnError)
	statusFlag := fs.String("status", "", "Status filter (pending, in_progress, completed, failed, blocked)")
	limitFlag := fs.Int("limit", 50, "Maximum number of tasks")
	jsonFlag := fs.Bool("json", false, "Print raw JSON output")

	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 1 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}

	res, err := callTool(serverURL, token, "list_tasks", map[string]interface{}{
		"project_id": fs.Arg(0),
		"status":     *statusFlag,
		"limit":      *limitFlag,
	})
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "Error: %v\n", err)
		return 1
	}
	if res.IsError {
		for _, c := range res.Content {
			_, _ = fmt.Fprintf(errOut, "Error: %s\n", c.Text)
		}
		return 1
	}
	if len(res.Content) == 0 || res.Content[0].Text == "" {
		_, _ = fmt.Fprintln(out, "No tasks found.")
		return 0
	}
	if *jsonFlag {
		_, _ = fmt.Fprintln(out, res.Content[0].Text)
		return 0
	}

	var tasks []taskSummary
	if err := json.Unmarshal([]byte(res.Content[0].Text), &tasks); err != nil {
		_, _ = fmt.Fprintln(out, res.Content[0].Text)
		return 0
	}
	if len(tasks) == 0 {
		_, _ = fmt.Fprintln(out, "No tasks found.")
		return 0
	}

	w := tabwriter.NewWriter(out, 0, 4, 3, ' ', 0)
	_, _ = fmt.Fprintln(w, "TASK ID\tSTATUS\tPRIORITY\tLEASED BY\tTITLE\tUPDATED AT")
	for _, t := range tasks {
		leasedBy := "-"
		if t.LeasedBy != nil && *t.LeasedBy != "" {
			leasedBy = *t.LeasedBy
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n",
			t.ID,
			t.Status,
			t.Priority,
			leasedBy,
			t.Title,
			t.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
		)
	}
	_ = w.Flush()
	return 0
}

func runTaskGet(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram task get <project_id> <task_id>\n"
	fs := flag.NewFlagSet("task get", flag.ContinueOnError)
	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 2 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}

	res, err := callTool(serverURL, token, "get_task", map[string]string{
		"project_id": fs.Arg(0),
		"task_id":    fs.Arg(1),
	})
	return printToolResult(res, err, out, errOut)
}

func runTaskCreate(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram task create <project_id> <task_id> <title> [--desc <text>] [--priority <n>] [--metadata <json>]\n"
	fs := flag.NewFlagSet("task create", flag.ContinueOnError)
	descFlag := fs.String("desc", "", "Task description / acceptance criteria")
	priorityFlag := fs.Int("priority", 0, "Task priority (higher runs first)")
	metaFlag := fs.String("metadata", "", "Optional JSON metadata")

	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 3 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}

	payload := map[string]interface{}{
		"project_id":  fs.Arg(0),
		"task_id":     fs.Arg(1),
		"title":       fs.Arg(2),
		"description": *descFlag,
		"priority":    *priorityFlag,
	}
	if strings.TrimSpace(*metaFlag) != "" {
		payload["metadata"] = *metaFlag
	}

	res, err := callTool(serverURL, token, "create_task", payload)
	return printToolResult(res, err, out, errOut)
}

func runTaskRenew(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram task renew <project_id> <task_id> --lease <lease_id> [--ttl <seconds>]\n"
	fs := flag.NewFlagSet("task renew", flag.ContinueOnError)
	var leaseID string
	var extendSec int
	fs.StringVar(&leaseID, "lease", "", "Active lease UUID token (required)")
	fs.StringVar(&leaseID, "lease-id", "", "Active lease UUID token (required)")
	fs.IntVar(&extendSec, "ttl", 300, "Additional lease duration in seconds")
	fs.IntVar(&extendSec, "extend", 300, "Additional lease duration in seconds")

	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 2 || strings.TrimSpace(leaseID) == "" {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}

	res, err := callTool(serverURL, token, "renew_task_lease", map[string]interface{}{
		"project_id":     fs.Arg(0),
		"task_id":        fs.Arg(1),
		"lease_id":       strings.TrimSpace(leaseID),
		"extend_seconds": extendSec,
	})
	return printToolResult(res, err, out, errOut)
}

func runTaskComplete(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram task complete <project_id> <task_id> [--lease <lease_id>]\n"
	fs := flag.NewFlagSet("task complete", flag.ContinueOnError)
	var leaseID string
	fs.StringVar(&leaseID, "lease", "", "Optional active lease UUID token")
	fs.StringVar(&leaseID, "lease-id", "", "Optional active lease UUID token")

	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 2 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}

	payload := map[string]interface{}{
		"project_id": fs.Arg(0),
		"task_id":    fs.Arg(1),
	}
	if strings.TrimSpace(leaseID) != "" {
		payload["lease_id"] = strings.TrimSpace(leaseID)
	}

	res, err := callTool(serverURL, token, "complete_task", payload)
	return printToolResult(res, err, out, errOut)
}

func runTaskFail(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram task fail <project_id> <task_id> [--lease <lease_id>] [--reason <text>]\n"
	fs := flag.NewFlagSet("task fail", flag.ContinueOnError)
	var leaseID string
	fs.StringVar(&leaseID, "lease", "", "Optional active lease UUID token")
	fs.StringVar(&leaseID, "lease-id", "", "Optional active lease UUID token")
	reasonFlag := fs.String("reason", "", "Autopsy reason or error message")

	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 2 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}

	payload := map[string]interface{}{
		"project_id": fs.Arg(0),
		"task_id":    fs.Arg(1),
		"reason":     *reasonFlag,
	}
	if strings.TrimSpace(leaseID) != "" {
		payload["lease_id"] = strings.TrimSpace(leaseID)
	}

	res, err := callTool(serverURL, token, "fail_task", payload)
	return printToolResult(res, err, out, errOut)
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

func searchUpForEngramConfig(startDir string) string {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}
	dir := abs
	for {
		candidate := filepath.Join(dir, ".engram.json")
		if fileExists(candidate) {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func findEngramConfig(rootDir string) string {
	if found := searchUpForEngramConfig(rootDir); found != "" {
		return found
	}

	if topOut, err := exec.Command("git", "-C", rootDir, "rev-parse", "--show-toplevel").Output(); err == nil {
		if top := strings.TrimSpace(string(topOut)); top != "" {
			if found := searchUpForEngramConfig(top); found != "" {
				return found
			}
		}
	}

	if commonOut, err := exec.Command("git", "-C", rootDir, "rev-parse", "--git-common-dir").Output(); err == nil {
		commonDir := strings.TrimSpace(string(commonOut))
		if commonDir != "" {
			if !filepath.IsAbs(commonDir) {
				commonDir = filepath.Join(rootDir, commonDir)
			}
			commonDir = filepath.Clean(commonDir)
			// In regular repos/worktrees, commonDir is "<repo>/.git"; check parent repo and its ancestors.
			searchStart := commonDir
			if filepath.Base(commonDir) == ".git" {
				searchStart = filepath.Dir(commonDir)
			}
			if found := searchUpForEngramConfig(searchStart); found != "" {
				return found
			}
		}
	}

	return ""
}

func readProjectIDFromConfig(cfgPath string) string {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return ""
	}
	var cfg struct {
		ProjectID      string `json:"project_id"`
		ProjectIDCamel string `json:"projectId"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	if strings.TrimSpace(cfg.ProjectID) != "" {
		return strings.TrimSpace(cfg.ProjectID)
	}
	return strings.TrimSpace(cfg.ProjectIDCamel)
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
	if cfgPath := findEngramConfig(rootDir); cfgPath != "" {
		if id := readProjectIDFromConfig(cfgPath); id != "" {
			return id
		}
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
		sources = append(sources, markdownFiles...)
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

func runInit(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram init [--project <id>] [--name <name>] [--docs <dir>] [--todo <file>]\n"
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "Project ID override")
	nameFlag := fs.String("name", "", "Display name of the project")
	docsFlag := fs.String("docs", "", "Documentation root directory override")
	todoFlag := fs.String("todo", "", "TODO file override")
	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

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
		_, _ = fmt.Fprintf(errOut, "Error registering project in daemon: %v\n", err)
		return 1
	}
	if res.IsError {
		errMsg := "Unknown error"
		if len(res.Content) > 0 {
			errMsg = res.Content[0].Text
		}
		_, _ = fmt.Fprintf(errOut, "Daemon error: %s\n", errMsg)
		return 1
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
		_, _ = fmt.Fprintf(errOut, "Error marshaling .engram.json: %v\n", err)
		return 1
	}

	cfgPath := filepath.Join(rootDir, ".engram.json")
	if err := os.WriteFile(cfgPath, append(cfgBytes, '\n'), 0644); err != nil {
		_, _ = fmt.Fprintf(errOut, "Error writing %s: %v\n", cfgPath, err)
		return 1
	}

	_, _ = fmt.Fprintln(out, "Engram project initialized successfully:")
	_, _ = fmt.Fprintf(out, "  Project ID: %s\n", projectID)
	_, _ = fmt.Fprintf(out, "  Name:       %s\n", name)
	_, _ = fmt.Fprintf(out, "  Server:     %s\n", serverURL)
	_, _ = fmt.Fprintf(out, "  Docs Root:  %s\n", docs.Root)
	if docs.TodoFile != "" {
		_, _ = fmt.Fprintf(out, "  TODO File:  %s\n", docs.TodoFile)
	}
	if len(docs.Sources) > 0 {
		_, _ = fmt.Fprintf(out, "  Sources:    %s\n", strings.Join(docs.Sources, ", "))
	}
	_, _ = fmt.Fprintf(out, "  Config:     %s\n", cfgPath)
	return 0
}

func printProjectUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Engram Project Management

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

func runProject(serverURL, token string, args []string, out, errOut io.Writer) int {
	if len(args) < 1 {
		printProjectUsage(errOut)
		return 1
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "help", "-h", "--help":
		printProjectUsage(out)
		return 0
	case "ls", "list":
		return runProjectList(serverURL, token, subargs, out, errOut)
	case "get":
		return runProjectGet(serverURL, token, subargs, out, errOut)
	case "create":
		return runProjectCreate(serverURL, token, subargs, out, errOut)
	case "rm", "delete":
		return runProjectRm(serverURL, token, subargs, out, errOut)
	default:
		_, _ = fmt.Fprintf(errOut, "Unknown project subcommand: %s\n\n", subcmd)
		printProjectUsage(errOut)
		return 1
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

func runProjectList(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram project ls\n"
	fs := flag.NewFlagSet("project ls", flag.ContinueOnError)
	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	res, err := callTool(serverURL, token, "list_projects", map[string]interface{}{})
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "Error: %v\n", err)
		return 1
	}
	if res.IsError {
		for _, c := range res.Content {
			_, _ = fmt.Fprintf(errOut, "Error: %s\n", c.Text)
		}
		return 1
	}

	if len(res.Content) == 0 || res.Content[0].Text == "" {
		_, _ = fmt.Fprintln(out, "No projects found.")
		return 0
	}

	var projs []projectSummary
	if err := json.Unmarshal([]byte(res.Content[0].Text), &projs); err != nil {
		_, _ = fmt.Fprintln(out, res.Content[0].Text)
		return 0
	}

	if len(projs) == 0 {
		_, _ = fmt.Fprintln(out, "No projects found.")
		return 0
	}

	w := tabwriter.NewWriter(out, 0, 4, 3, ' ', 0)
	_, _ = fmt.Fprintln(w, "PROJECT ID\tNAME\tDOCS ROOT\tUPDATED AT")
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
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			p.ID,
			p.Name,
			docsRoot,
			p.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
		)
	}
	_ = w.Flush()
	return 0
}

func runProjectGet(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram project get <project_id>\n"
	fs := flag.NewFlagSet("project get", flag.ContinueOnError)
	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 1 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}
	res, err := callTool(serverURL, token, "get_project", map[string]string{
		"project_id": fs.Arg(0),
	})
	return printToolResult(res, err, out, errOut)
}

func parseFlagsAndArgs(fs *flag.FlagSet, args []string) error {
	var flags []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			flagName := strings.TrimLeft(arg, "-")
			if strings.Contains(flagName, "=") {
				continue
			}
			f := fs.Lookup(flagName)
			if f != nil {
				if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
					continue
				}
				// Known non-boolean flag: unconditionally consume the next arg as its value.
				if i+1 < len(args) {
					i++
					flags = append(flags, args[i])
				}
				continue
			}
			// Unknown flag fallback: only consume if next arg doesn't look like a flag.
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

func runProjectCreate(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram project create <project_id> [--name <name>] [--desc <desc>]\n"
	fs := flag.NewFlagSet("project create", flag.ContinueOnError)
	nameFlag := fs.String("name", "", "Display name of project")
	descFlag := fs.String("desc", "", "Description of project")
	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 1 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
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
	return printToolResult(res, err, out, errOut)
}

func runProjectRm(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = "Usage: engram project rm <project_id> --confirm <project_id> [--purge]\n"
	fs := flag.NewFlagSet("project rm", flag.ContinueOnError)
	confirmFlag := fs.String("confirm", "", "Must match the project ID to confirm deletion")
	purgeFlag := fs.Bool("purge", false, "Hard delete (purge) from database instead of soft-delete")
	if help, ok := parseSubcommandFlags(fs, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	if fs.NArg() < 1 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}
	projectID := fs.Arg(0)

	if *confirmFlag == "" {
		_, _ = fmt.Fprintln(errOut, "Error: --confirm <project_id> is required for deletion")
		return 1
	}
	if *confirmFlag != projectID {
		_, _ = fmt.Fprintf(errOut, "Error: confirmation mismatch: --confirm '%s' does not match '%s'\n", *confirmFlag, projectID)
		return 1
	}

	payload := map[string]interface{}{
		"project_id":         projectID,
		"confirm_project_id": *confirmFlag,
		"purge":              *purgeFlag,
	}
	res, err := callTool(serverURL, token, "delete_project", payload)
	return printToolResult(res, err, out, errOut)
}

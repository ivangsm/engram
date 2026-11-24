package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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
	_ = fs.Parse(args)

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
	_ = fs.Parse(args)

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
	_ = fs.Parse(args)

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
	_ = fs.Parse(args)

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

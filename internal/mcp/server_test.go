package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMCPServer_InitializeAndToolsList(t *testing.T) {
	registry := NewRegistry(nil, nil)
	server := NewServer(registry)

	// Test initialize
	initReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}
	res, err := server.processRPC(context.Background(), initReq)
	if err != nil {
		t.Fatalf("unexpected error during initialize: %v", err)
	}

	initRes, ok := res.(InitializeResult)
	if !ok {
		t.Fatalf("expected InitializeResult, got %T", res)
	}
	if initRes.ServerInfo.Name != "engram" {
		t.Fatalf("expected server name 'engram', got '%s'", initRes.ServerInfo.Name)
	}

	// Test tools/list
	toolsReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}
	toolsResRaw, err := server.processRPC(context.Background(), toolsReq)
	if err != nil {
		t.Fatalf("unexpected error during tools/list: %v", err)
	}

	toolsRes, ok := toolsResRaw.(ToolsListResult)
	if !ok {
		t.Fatalf("expected ToolsListResult, got %T", toolsResRaw)
	}

	toolNames := make(map[string]bool)
	for _, tool := range toolsRes.Tools {
		toolNames[tool.Name] = true
	}

	expectedTools := []string{
		"list_memory_manifest",
		"recall_memory",
		"search_memory",
		"store_memory",
		"blame_memory",
		"create_task",
		"claim_task",
		"renew_task_lease",
		"record_handoff",
		"complete_task",
		"fail_task",
	}

	for _, expected := range expectedTools {
		if !toolNames[expected] {
			t.Errorf("missing expected tool in registry: %s", expected)
		}
	}
}

func TestMCPServer_Ping(t *testing.T) {
	registry := NewRegistry(nil, nil)
	server := NewServer(registry)

	pingReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      "ping-1",
		Method:  "ping",
	}
	res, err := server.processRPC(context.Background(), pingReq)
	if err != nil {
		t.Fatalf("unexpected error during ping: %v", err)
	}
	resBytes, _ := json.Marshal(res)
	if string(resBytes) != "{}" {
		t.Fatalf("expected empty object for ping, got %s", string(resBytes))
	}
}

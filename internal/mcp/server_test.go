package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ivan/engram/internal/core/projects"
	"github.com/ivan/engram/internal/db"
)

type mcpMockQuerier struct {
	projects map[string]db.Project
}

func (m *mcpMockQuerier) CreateProject(ctx context.Context, arg db.CreateProjectParams) (db.Project, error) {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	p := db.Project{
		ID:          arg.ID,
		Name:        arg.Name,
		Description: arg.Description,
		Settings:    arg.Settings,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	m.projects[arg.ID] = p
	return p, nil
}

func (m *mcpMockQuerier) GetProject(ctx context.Context, id string) (db.Project, error) {
	p, ok := m.projects[id]
	if !ok || p.DeletedAt.Valid {
		return db.Project{}, pgx.ErrNoRows
	}
	return p, nil
}

func (m *mcpMockQuerier) ListProjects(ctx context.Context) ([]db.Project, error) {
	var list []db.Project
	for _, p := range m.projects {
		if !p.DeletedAt.Valid {
			list = append(list, p)
		}
	}
	return list, nil
}

func (m *mcpMockQuerier) UpdateProjectSettings(ctx context.Context, arg db.UpdateProjectSettingsParams) (db.Project, error) {
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

func (m *mcpMockQuerier) SoftDeleteProject(ctx context.Context, id string) error {
	p, ok := m.projects[id]
	if !ok || p.DeletedAt.Valid {
		return pgx.ErrNoRows
	}
	p.DeletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	m.projects[id] = p
	return nil
}

func (m *mcpMockQuerier) PurgeProject(ctx context.Context, id string) error {
	delete(m.projects, id)
	return nil
}

func TestMCPServer_ProjectTools(t *testing.T) {
	mock := &mcpMockQuerier{projects: make(map[string]db.Project)}
	projSvc := projects.NewServiceWithQuerier(mock)
	registry := NewRegistry(nil, nil, projSvc)
	server := NewServer(registry)
	ctx := context.Background()

	// 1. create_project
	createReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      10,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"create_project","arguments":{"project_id":"p-test","name":"Test Project","settings":{"docs":{"root":"docs"}}}}`),
	}
	res, err := server.processRPC(ctx, createReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callRes := res.(*CallToolResult)
	if callRes.IsError {
		t.Fatalf("create_project failed: %s", callRes.Content[0].Text)
	}

	// 2. get_project
	getReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      11,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"get_project","arguments":{"project_id":"p-test"}}`),
	}
	res, err = server.processRPC(ctx, getReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callRes = res.(*CallToolResult)
	if callRes.IsError {
		t.Fatalf("get_project failed: %s", callRes.Content[0].Text)
	}

	// 3. list_projects
	listReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      12,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"list_projects","arguments":{}}`),
	}
	res, err = server.processRPC(ctx, listReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callRes = res.(*CallToolResult)
	if callRes.IsError {
		t.Fatalf("list_projects failed: %s", callRes.Content[0].Text)
	}

	// 4. delete_project mismatch
	delFailReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      13,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"delete_project","arguments":{"project_id":"p-test","confirm_project_id":"wrong"}}`),
	}
	res, err = server.processRPC(ctx, delFailReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callRes = res.(*CallToolResult)
	if !callRes.IsError {
		t.Fatalf("expected error on mismatched confirmation")
	}

	// 5. delete_project success
	delReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      14,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"delete_project","arguments":{"project_id":"p-test","confirm_project_id":"p-test","purge":true}}`),
	}
	res, err = server.processRPC(ctx, delReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callRes = res.(*CallToolResult)
	if callRes.IsError {
		t.Fatalf("delete_project failed: %s", callRes.Content[0].Text)
	}
}

func TestMCPServer_InitializeAndToolsList(t *testing.T) {
	registry := NewRegistry(nil, nil, nil)
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
		"list_tasks",
		"get_task",
		"claim_task",
		"renew_task_lease",
		"record_handoff",
		"complete_task",
		"fail_task",
		"list_projects",
		"get_project",
		"create_project",
		"delete_project",
	}

	for _, expected := range expectedTools {
		if !toolNames[expected] {
			t.Errorf("missing expected tool in registry: %s", expected)
		}
	}
}

func TestMCPServer_Ping(t *testing.T) {
	registry := NewRegistry(nil, nil, nil)
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

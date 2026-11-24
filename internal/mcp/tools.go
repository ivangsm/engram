package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/ivan/engram/internal/core/memories"
	"github.com/ivan/engram/internal/core/state"
)

type Registry struct {
	memoriesSvc *memories.Service
	stateSvc    *state.Service
}

func NewRegistry(m *memories.Service, s *state.Service) *Registry {
	return &Registry{
		memoriesSvc: m,
		stateSvc:    s,
	}
}

func (r *Registry) ListTools() []Tool {
	return []Tool{
		{
			Name:        "list_memory_manifest",
			Description: "Progressive disclosure: returns a lightweight skeletal manifest of all memory keys, one-line digests, token counts, and scopes. ZERO content dumping.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"project_id": {Type: "string", Description: "Project/repository identifier (e.g., 'engram', 'birdple-v2')"},
					"scope":      {Type: "string", Description: "Optional scope filter (e.g., 'global', 'arch', 'rules', 'conventions')"},
				},
				Required: []string{"project_id"},
			},
		},
		{
			Name:        "recall_memory",
			Description: "Hydrates the full content, invariants, and metadata of a specific memory key under demand.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"project_id": {Type: "string", Description: "Project/repository identifier"},
					"key":        {Type: "string", Description: "Exact memory key to retrieve (e.g., 'auth-contracts', 'db-schema')"},
				},
				Required: []string{"project_id", "key"},
			},
		},
		{
			Name:        "search_memory",
			Description: "Executes hybrid ranking (cosine vector similarity + tsvector fulltext rank) to find relevant memories.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"project_id": {Type: "string", Description: "Project/repository identifier"},
					"query":      {Type: "string", Description: "Search query text or keywords"},
					"limit":      {Type: "integer", Description: "Maximum number of results (default: 10)"},
				},
				Required: []string{"project_id", "query"},
			},
		},
		{
			Name:        "store_memory",
			Description: "Stores or versions a semantic memory. Appends to audit log with SHA-256 hash chaining, unified diffs, and provenance. Supports strict OCC via expected_parent_hash.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"project_id":           {Type: "string", Description: "Project/repository identifier"},
					"key":                  {Type: "string", Description: "Unique memory key"},
					"content":              {Type: "string", Description: "Atomic invariants, architectural rules, or semantic facts"},
					"digest":               {Type: "string", Description: "High-density single-line digest (<20 words)"},
					"scope":                {Type: "string", Description: "Scope identifier (default: 'global')"},
					"task_id":              {Type: "string", Description: "Associated task ID (e.g., 'TASK-101')"},
					"rationale":            {Type: "string", Description: "Technical justification for introducing or updating this memory"},
					"expected_parent_hash": {Type: "string", Description: "Optional expected parent content hash for optimistic concurrency validation"},
					"author_user":          {Type: "string", Description: "Username of the author (defaults to env or current user)"},
					"author_agent_model":   {Type: "string", Description: "Model identifier (e.g., 'gemini-3.8-flash', 'claude-3-7-sonnet')"},
					"machine_id":           {Type: "string", Description: "Machine hostname or environment ID"},
				},
				Required: []string{"project_id", "key", "content", "rationale"},
			},
		},
		{
			Name:        "blame_memory",
			Description: "Returns cryptographic provenance, revision genealogy, authors, machines, models, rationales, unified diffs, and hash chain validity for a memory.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"project_id": {Type: "string", Description: "Project/repository identifier"},
					"key":        {Type: "string", Description: "Memory key to audit"},
				},
				Required: []string{"project_id", "key"},
			},
		},
		{
			Name:        "create_task",
			Description: "Enqueues an operational task in the state plane.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"project_id":  {Type: "string", Description: "Project/repository identifier"},
					"task_id":     {Type: "string", Description: "Unique task identifier (e.g., 'TASK-101')"},
					"title":       {Type: "string", Description: "Short summary of the task"},
					"description": {Type: "string", Description: "Detailed task instructions or acceptance criteria"},
					"priority":    {Type: "integer", Description: "Priority level (higher values executed first)"},
					"metadata":    {Type: "string", Description: "Optional JSON metadata"},
				},
				Required: []string{"project_id", "task_id", "title"},
			},
		},
		{
			Name:        "claim_task",
			Description: "Atomically claims a task lease using row-level locking (FOR UPDATE SKIP LOCKED). If task_id is omitted, claims highest priority pending task.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"project_id":  {Type: "string", Description: "Project/repository identifier"},
					"task_id":     {Type: "string", Description: "Optional specific task ID to claim. Omit to claim next available."},
					"leased_by":   {Type: "string", Description: "Agent or worker identifier holding the lease"},
					"ttl_seconds": {Type: "integer", Description: "Lease duration in seconds (default: 300)"},
				},
				Required: []string{"project_id"},
			},
		},
		{
			Name:        "renew_task_lease",
			Description: "Heartbeat mechanism: extends the lease expiration on an active claimed task using lease fencing.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"project_id":     {Type: "string", Description: "Project/repository identifier"},
					"task_id":        {Type: "string", Description: "Task ID holding the lease"},
					"lease_id":       {Type: "string", Description: "Active lease UUID token required for fencing"},
					"extend_seconds": {Type: "integer", Description: "Additional duration in seconds (default: 300)"},
				},
				Required: []string{"project_id", "task_id", "lease_id"},
			},
		},
		{
			Name:        "record_handoff",
			Description: "Records an immutable cross-agent handoff. Enforces strict contract: <150 words summary, valid verdict, and reproducible evidence.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"project_id":            {Type: "string", Description: "Project/repository identifier"},
					"task_id":               {Type: "string", Description: "Task being handed off"},
					"from_agent":            {Type: "string", Description: "Source agent identifier"},
					"to_agent":              {Type: "string", Description: "Target agent identifier (optional)"},
					"verdict":               {Type: "string", Description: "Verdict: PASS, FAIL, BLOCKED, NEEDS_REVIEW, HANDOFF, LEASE_EXPIRED"},
					"summary":               {Type: "string", Description: "Strictly <150 words executive summary of progress and remaining work"},
					"reproducible_evidence": {Type: "string", Description: "Commands, test outputs, or reproducible artifact hashes"},
					"artifacts":             {Type: "string", Description: "JSON serialized string of changed file paths, PRs, or logs"},
				},
				Required: []string{"project_id", "task_id", "from_agent", "verdict", "summary", "reproducible_evidence"},
			},
		},
		{
			Name:        "complete_task",
			Description: "Marks an operational task as completed and releases active leases, validating lease fencing.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"project_id": {Type: "string", Description: "Project/repository identifier"},
					"task_id":    {Type: "string", Description: "Task ID to complete"},
					"lease_id":   {Type: "string", Description: "Optional active lease UUID token"},
				},
				Required: []string{"project_id", "task_id"},
			},
		},
		{
			Name:        "fail_task",
			Description: "Marks an operational task as failed, recording autopsy reason and releasing leases.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"project_id": {Type: "string", Description: "Project/repository identifier"},
					"task_id":    {Type: "string", Description: "Task ID to mark as failed"},
					"lease_id":   {Type: "string", Description: "Optional active lease UUID token"},
					"reason":     {Type: "string", Description: "Reason or error causing failure"},
				},
				Required: []string{"project_id", "task_id"},
			},
		},
	}
}

func (r *Registry) ExecuteTool(ctx context.Context, name string, args json.RawMessage) (*CallToolResult, error) {
	switch name {
	case "list_memory_manifest":
		var in struct {
			ProjectID string `json:"project_id"`
			Scope     string `json:"scope"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return errorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		manifest, err := r.memoriesSvc.GetManifest(ctx, in.ProjectID, in.Scope)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data, _ := json.MarshalIndent(manifest, "", "  ")
		return textResult(string(data)), nil

	case "recall_memory":
		var in struct {
			ProjectID string `json:"project_id"`
			Key       string `json:"key"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return errorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		mem, err := r.memoriesSvc.Recall(ctx, in.ProjectID, in.Key)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data, _ := json.MarshalIndent(mem, "", "  ")
		return textResult(string(data)), nil

	case "search_memory":
		var in struct {
			ProjectID string `json:"project_id"`
			Query     string `json:"query"`
			Limit     int32  `json:"limit"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return errorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		res, err := r.memoriesSvc.Search(ctx, in.ProjectID, in.Query, nil, in.Limit)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data, _ := json.MarshalIndent(res, "", "  ")
		return textResult(string(data)), nil

	case "store_memory":
		var in struct {
			ProjectID          string  `json:"project_id"`
			Key                string  `json:"key"`
			Content            string  `json:"content"`
			Digest             string  `json:"digest"`
			Scope              string  `json:"scope"`
			TaskID             string  `json:"task_id"`
			Rationale          string  `json:"rationale"`
			ExpectedParentHash *string `json:"expected_parent_hash"`
			AuthorUser         string  `json:"author_user"`
			AuthorAgentModel   string  `json:"author_agent_model"`
			MachineID          string  `json:"machine_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return errorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}

		if in.AuthorUser == "" {
			in.AuthorUser = os.Getenv("USER")
			if in.AuthorUser == "" {
				in.AuthorUser = "ivan"
			}
		}
		if in.MachineID == "" {
			hostname, _ := os.Hostname()
			in.MachineID = hostname
		}
		if in.Digest == "" {
			// Extract first line as fallback digest
			firstLine := in.Content
			for i, r := range in.Content {
				if r == '\n' || r == '\r' {
					firstLine = in.Content[:i]
					break
				}
			}
			if len(firstLine) > 120 {
				firstLine = firstLine[:117] + "..."
			}
			in.Digest = firstLine
		}

		res, err := r.memoriesSvc.Store(ctx, memories.StoreInput{
			ProjectID:          in.ProjectID,
			Key:                in.Key,
			Scope:              in.Scope,
			Digest:             in.Digest,
			Content:            in.Content,
			AuthorUser:         in.AuthorUser,
			AuthorAgentModel:   in.AuthorAgentModel,
			MachineID:          in.MachineID,
			TaskID:             in.TaskID,
			Rationale:          in.Rationale,
			ExpectedParentHash: in.ExpectedParentHash,
		})
		if err != nil {
			if staleErr, ok := err.(*memories.StaleRevisionError); ok {
				errData, _ := json.MarshalIndent(map[string]interface{}{
					"error":           "ERR_STALE_REVISION",
					"message":         staleErr.Error(),
					"key":             staleErr.Key,
					"head_rev":        staleErr.HeadRev,
					"head_hash":       staleErr.HeadHash,
					"current_content": staleErr.CurrentContent,
					"diff_unified":    staleErr.DiffUnified,
				}, "", "  ")
				return errorResult(string(errData)), nil
			}
			return errorResult(err.Error()), nil
		}
		data, _ := json.MarshalIndent(res, "", "  ")
		return textResult(string(data)), nil

	case "blame_memory":
		var in struct {
			ProjectID string `json:"project_id"`
			Key       string `json:"key"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return errorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		blame, err := r.memoriesSvc.Blame(ctx, in.ProjectID, in.Key)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data, _ := json.MarshalIndent(blame, "", "  ")
		return textResult(string(data)), nil

	case "create_task":
		var in struct {
			ProjectID   string `json:"project_id"`
			TaskID      string `json:"task_id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Priority    int32  `json:"priority"`
			Metadata    string `json:"metadata"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return errorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		metaBytes := json.RawMessage("{}")
		if in.Metadata != "" {
			metaBytes = json.RawMessage(in.Metadata)
		}
		t, err := r.stateSvc.CreateTask(ctx, in.ProjectID, in.TaskID, in.Title, in.Description, in.Priority, metaBytes)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data, _ := json.MarshalIndent(t, "", "  ")
		return textResult(string(data)), nil

	case "claim_task":
		var in struct {
			ProjectID  string `json:"project_id"`
			TaskID     string `json:"task_id"`
			LeasedBy   string `json:"leased_by"`
			TTLSeconds int32  `json:"ttl_seconds"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return errorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		t, err := r.stateSvc.ClaimTask(ctx, in.ProjectID, in.TaskID, in.LeasedBy, in.TTLSeconds)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data, _ := json.MarshalIndent(t, "", "  ")
		return textResult(string(data)), nil

	case "renew_task_lease":
		var in struct {
			ProjectID     string `json:"project_id"`
			TaskID        string `json:"task_id"`
			LeaseID       string `json:"lease_id"`
			ExtendSeconds int32  `json:"extend_seconds"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return errorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		t, err := r.stateSvc.RenewTaskLease(ctx, in.ProjectID, in.TaskID, in.LeaseID, in.ExtendSeconds)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data, _ := json.MarshalIndent(t, "", "  ")
		return textResult(string(data)), nil

	case "record_handoff":
		var in struct {
			ProjectID            string  `json:"project_id"`
			TaskID               string  `json:"task_id"`
			FromAgent            string  `json:"from_agent"`
			ToAgent              *string `json:"to_agent"`
			Verdict              string  `json:"verdict"`
			Summary              string  `json:"summary"`
			ReproducibleEvidence string  `json:"reproducible_evidence"`
			Artifacts            string  `json:"artifacts"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return errorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}

		artifactsRaw := json.RawMessage("[]")
		if in.Artifacts != "" {
			artifactsRaw = json.RawMessage(in.Artifacts)
		}

		h, err := r.stateSvc.RecordHandoff(ctx, state.HandoffInput{
			ProjectID:            in.ProjectID,
			TaskID:               in.TaskID,
			FromAgent:            in.FromAgent,
			ToAgent:              in.ToAgent,
			Verdict:              in.Verdict,
			Summary:              in.Summary,
			ReproducibleEvidence: in.ReproducibleEvidence,
			Artifacts:            artifactsRaw,
		})
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data, _ := json.MarshalIndent(h, "", "  ")
		return textResult(string(data)), nil

	case "complete_task":
		var in struct {
			ProjectID string `json:"project_id"`
			TaskID    string `json:"task_id"`
			LeaseID   string `json:"lease_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return errorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		t, err := r.stateSvc.CompleteTask(ctx, in.ProjectID, in.TaskID, in.LeaseID)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data, _ := json.MarshalIndent(t, "", "  ")
		return textResult(string(data)), nil

	case "fail_task":
		var in struct {
			ProjectID string `json:"project_id"`
			TaskID    string `json:"task_id"`
			LeaseID   string `json:"lease_id"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return errorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		t, err := r.stateSvc.FailTask(ctx, in.ProjectID, in.TaskID, in.LeaseID, in.Reason)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data, _ := json.MarshalIndent(t, "", "  ")
		return textResult(string(data)), nil

	default:
		return errorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
}

func textResult(msg string) *CallToolResult {
	return &CallToolResult{
		Content: []ToolContent{
			{
				Type: "text",
				Text: msg,
			},
		},
		IsError: false,
	}
}

func errorResult(msg string) *CallToolResult {
	return &CallToolResult{
		Content: []ToolContent{
			{
				Type: "text",
				Text: msg,
			},
		},
		IsError: true,
	}
}

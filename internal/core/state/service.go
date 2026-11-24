package state

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ivan/engram/internal/db"
)

type Service struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{
		pool:    pool,
		queries: db.New(pool),
	}
}

type TaskDetail struct {
	ProjectID      string          `json:"project_id"`
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Status         string          `json:"status"`
	Priority       int32           `json:"priority"`
	LeasedBy       *string         `json:"leased_by,omitempty"`
	LeaseID        *string         `json:"lease_id,omitempty"`
	ClaimCount     int32           `json:"claim_count"`
	MaxRetries     int32           `json:"max_retries"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	ClaimedAt      *time.Time      `json:"claimed_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if s == "" {
		return u, nil
	}
	err := u.Scan(s)
	return u, err
}

func newRandomUUID() pgtype.UUID {
	var u pgtype.UUID
	_, _ = rand.Read(u.Bytes[:])
	// set version 4 and variant RFC 4122
	u.Bytes[6] = (u.Bytes[6] & 0x0f) | 0x40
	u.Bytes[8] = (u.Bytes[8] & 0x3f) | 0x80
	u.Valid = true
	return u
}

func formatUUID(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		u.Bytes[0:4], u.Bytes[4:6], u.Bytes[6:8], u.Bytes[8:10], u.Bytes[10:16])
}

func mapTask(t db.AgentTask) TaskDetail {
	var leaseExp *time.Time
	if t.LeaseExpiresAt.Valid {
		tm := t.LeaseExpiresAt.Time
		leaseExp = &tm
	}
	var claimed *time.Time
	if t.ClaimedAt.Valid {
		tm := t.ClaimedAt.Time
		claimed = &tm
	}
	var completed *time.Time
	if t.CompletedAt.Valid {
		tm := t.CompletedAt.Time
		completed = &tm
	}
	var leaseIDStr *string
	if t.LeaseID.Valid {
		s := formatUUID(t.LeaseID)
		leaseIDStr = &s
	}

	meta := json.RawMessage("{}")
	if len(t.Metadata) > 0 {
		meta = json.RawMessage(t.Metadata)
	}

	return TaskDetail{
		ProjectID:      t.ProjectID,
		ID:             t.ID,
		Title:          t.Title,
		Description:    t.Description,
		Status:         t.Status,
		Priority:       t.Priority,
		LeasedBy:       t.LeasedBy,
		LeaseID:        leaseIDStr,
		ClaimCount:     t.ClaimCount,
		MaxRetries:     t.MaxRetries,
		LeaseExpiresAt: leaseExp,
		ClaimedAt:      claimed,
		CompletedAt:    completed,
		Metadata:       meta,
		CreatedAt:      t.CreatedAt.Time,
		UpdatedAt:      t.UpdatedAt.Time,
	}
}

func (s *Service) recordEvent(ctx context.Context, projectID, topic string, payload interface{}) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = s.queries.CreateEvent(ctx, db.CreateEventParams{
		ProjectID: projectID,
		Topic:     topic,
		Payload:   payloadBytes,
	})
	_, _ = s.pool.Exec(ctx, "NOTIFY engram_events, 'ping'")
}

func (s *Service) CreateTask(ctx context.Context, projectID, id, title, description string, priority int32, metadata json.RawMessage) (*TaskDetail, error) {
	if len(metadata) == 0 {
		metadata = json.RawMessage("{}")
	}

	// Ensure project exists
	_, err := s.queries.CreateProject(ctx, db.CreateProjectParams{
		ID:          projectID,
		Name:        projectID,
		Description: "Auto-created project namespace",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to ensure project exists: %w", err)
	}

	t, err := s.queries.CreateTask(ctx, db.CreateTaskParams{
		ProjectID:   projectID,
		ID:          id,
		Title:       title,
		Description: description,
		Status:      "pending",
		Priority:    priority,
		Metadata:    []byte(metadata),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	s.recordEvent(ctx, projectID, "task.created", map[string]interface{}{
		"id":       id,
		"title":    title,
		"priority": priority,
	})

	td := mapTask(t)
	return &td, nil
}

func (s *Service) GetTask(ctx context.Context, projectID, id string) (*TaskDetail, error) {
	t, err := s.queries.GetTask(ctx, db.GetTaskParams{
		ProjectID: projectID,
		ID:        id,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("task '%s' not found in project '%s'", id, projectID)
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	td := mapTask(t)
	return &td, nil
}

func (s *Service) ListTasks(ctx context.Context, projectID, status string, limit int32) ([]TaskDetail, error) {
	if limit <= 0 {
		limit = 50
	}
	tasks, err := s.queries.ListTasks(ctx, db.ListTasksParams{
		ProjectID: projectID,
		Column2:   status,
		Limit:     limit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	result := make([]TaskDetail, len(tasks))
	for i, t := range tasks {
		result[i] = mapTask(t)
	}
	return result, nil
}

// ClaimTask claims either a specific task or the next highest-priority pending task using row-level locking.
func (s *Service) ClaimTask(ctx context.Context, projectID, taskID, leasedBy string, ttlSeconds int32) (*TaskDetail, error) {
	if ttlSeconds <= 0 {
		ttlSeconds = 300 // default 5 minutes lease
	}
	if leasedBy == "" {
		leasedBy = "anonymous-agent"
	}

	// Ensure project exists
	_, _ = s.queries.CreateProject(ctx, db.CreateProjectParams{
		ID:          projectID,
		Name:        projectID,
		Description: "Auto-created project namespace",
	})

	var t db.AgentTask
	var err error
	leaseUUID := newRandomUUID()

	if taskID != "" {
		// Claim specific task if available or lease expired
		t, err = s.queries.ClaimSpecificTask(ctx, db.ClaimSpecificTaskParams{
			ProjectID: projectID,
			ID:        taskID,
			LeasedBy:  &leasedBy,
			LeaseID:   leaseUUID,
			Column5:   float64(ttlSeconds),
		})
	} else {
		// Claim next available pending task with SKIP LOCKED
		t, err = s.queries.ClaimNextPendingTask(ctx, db.ClaimNextPendingTaskParams{
			ProjectID: projectID,
			LeasedBy:  &leasedBy,
			LeaseID:   leaseUUID,
			Column4:   float64(ttlSeconds),
		})
	}

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrTaskNotFoundOrBusy
		}
		return nil, fmt.Errorf("failed to claim task: %w", err)
	}

	// If task failed due to poison-pill threshold (claim_count >= max_retries)
	if t.Status == "failed" {
		_, _ = s.queries.CreateHandoff(ctx, db.CreateHandoffParams{
			ProjectID:            projectID,
			TaskID:               t.ID,
			FromAgent:            "system/lease-reaper",
			ToAgent:              &leasedBy,
			Verdict:              "FAIL",
			Summary:              fmt.Sprintf("Task exceeded max retries (%d); automatically marked failed", t.MaxRetries),
			ReproducibleEvidence: fmt.Sprintf("Poison-pill threshold triggered in claim CTE (claim_count: %d)", t.ClaimCount),
			Artifacts:            []byte("[]"),
		})
		s.recordEvent(ctx, projectID, "task.failed", map[string]interface{}{
			"id":     t.ID,
			"reason": "poison_pill_max_retries",
		})
		td := mapTask(t)
		return &td, errors.New("task exceeded max retries and transitioned to FAILED status")
	}

	s.recordEvent(ctx, projectID, "task.claimed", map[string]interface{}{
		"id":         t.ID,
		"leased_by":  leasedBy,
		"lease_id":   formatUUID(leaseUUID),
		"expires_in": ttlSeconds,
	})

	td := mapTask(t)
	return &td, nil
}

func (s *Service) RenewTaskLease(ctx context.Context, projectID, taskID, leaseID string, extendSeconds int32) (*TaskDetail, error) {
	if extendSeconds <= 0 {
		extendSeconds = 300
	}
	u, err := parseUUID(leaseID)
	if err != nil || !u.Valid {
		return nil, fmt.Errorf("invalid lease_id: %w", err)
	}

	t, err := s.queries.RenewTaskLease(ctx, db.RenewTaskLeaseParams{
		ProjectID: projectID,
		ID:        taskID,
		LeaseID:   u,
		Column4:   float64(extendSeconds),
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrLeaseLost
		}
		return nil, fmt.Errorf("failed to renew task lease: %w", err)
	}

	s.recordEvent(ctx, projectID, "task.renewed", map[string]interface{}{
		"id":         taskID,
		"lease_id":   leaseID,
		"extend_sec": extendSeconds,
	})

	td := mapTask(t)
	return &td, nil
}

func (s *Service) CompleteTask(ctx context.Context, projectID, taskID, leaseID string) (*TaskDetail, error) {
	var u pgtype.UUID
	if leaseID != "" {
		parsed, err := parseUUID(leaseID)
		if err != nil {
			return nil, fmt.Errorf("invalid lease_id: %w", err)
		}
		u = parsed
	}

	t, err := s.queries.CompleteTask(ctx, db.CompleteTaskParams{
		ProjectID: projectID,
		ID:        taskID,
		LeaseID:   u,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrLeaseLost
		}
		return nil, fmt.Errorf("failed to complete task: %w", err)
	}

	s.recordEvent(ctx, projectID, "task.completed", map[string]interface{}{
		"id": taskID,
	})

	td := mapTask(t)
	return &td, nil
}

func (s *Service) FailTask(ctx context.Context, projectID, taskID, leaseID, reason string) (*TaskDetail, error) {
	var u pgtype.UUID
	if leaseID != "" {
		parsed, err := parseUUID(leaseID)
		if err != nil {
			return nil, fmt.Errorf("invalid lease_id: %w", err)
		}
		u = parsed
	}

	t, err := s.queries.FailTask(ctx, db.FailTaskParams{
		ProjectID: projectID,
		ID:        taskID,
		LeaseID:   u,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrLeaseLost
		}
		return nil, fmt.Errorf("failed to fail task: %w", err)
	}

	if reason != "" {
		_, _ = s.queries.CreateHandoff(ctx, db.CreateHandoffParams{
			ProjectID:            projectID,
			TaskID:               taskID,
			FromAgent:            "agent",
			Verdict:              "FAIL",
			Summary:              reason,
			ReproducibleEvidence: "Explicit FailTask invocation",
			Artifacts:            []byte("[]"),
		})
	}

	s.recordEvent(ctx, projectID, "task.failed", map[string]interface{}{
		"id":     taskID,
		"reason": reason,
	})

	td := mapTask(t)
	return &td, nil
}

func (s *Service) ReleaseTask(ctx context.Context, projectID, taskID, leaseID string) (*TaskDetail, error) {
	var u pgtype.UUID
	if leaseID != "" {
		parsed, err := parseUUID(leaseID)
		if err != nil {
			return nil, fmt.Errorf("invalid lease_id: %w", err)
		}
		u = parsed
	}

	t, err := s.queries.ReleaseTask(ctx, db.ReleaseTaskParams{
		ProjectID: projectID,
		ID:        taskID,
		LeaseID:   u,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrLeaseLost
		}
		return nil, fmt.Errorf("failed to release task: %w", err)
	}

	s.recordEvent(ctx, projectID, "task.released", map[string]interface{}{
		"id": taskID,
	})

	td := mapTask(t)
	return &td, nil
}

type HandoffDetail struct {
	ID                   string          `json:"id"`
	ProjectID            string          `json:"project_id"`
	TaskID               string          `json:"task_id"`
	FromAgent            string          `json:"from_agent"`
	ToAgent              *string         `json:"to_agent,omitempty"`
	Verdict              string          `json:"verdict"`
	Summary              string          `json:"summary"`
	ReproducibleEvidence string          `json:"reproducible_evidence"`
	Artifacts            json.RawMessage `json:"artifacts,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
}

func (s *Service) RecordHandoff(ctx context.Context, in HandoffInput) (*HandoffDetail, error) {
	if err := ValidateHandoff(in); err != nil {
		return nil, err
	}

	in.Verdict = strings.ToUpper(strings.TrimSpace(in.Verdict))

	artifactsBytes := []byte("[]")
	if len(in.Artifacts) > 0 {
		artifactsBytes = in.Artifacts
	}

	// Ensure project and task exist
	_, _ = s.queries.CreateProject(ctx, db.CreateProjectParams{
		ID:          in.ProjectID,
		Name:        in.ProjectID,
		Description: "Auto-created project namespace",
	})

	_, err := s.queries.GetTask(ctx, db.GetTaskParams{
		ProjectID: in.ProjectID,
		ID:        in.TaskID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			// Auto create placeholder task if not already present
			_, _ = s.queries.CreateTask(ctx, db.CreateTaskParams{
				ProjectID:   in.ProjectID,
				ID:          in.TaskID,
				Title:       in.TaskID,
				Description: "Task created via handoff",
				Status:      "in_progress",
				Priority:    0,
				Metadata:    []byte("{}"),
			})
		}
	}

	h, err := s.queries.CreateHandoff(ctx, db.CreateHandoffParams{
		ProjectID:            in.ProjectID,
		TaskID:               in.TaskID,
		FromAgent:            in.FromAgent,
		ToAgent:              in.ToAgent,
		Verdict:              in.Verdict,
		Summary:              in.Summary,
		ReproducibleEvidence: in.ReproducibleEvidence,
		Artifacts:            artifactsBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to record handoff: %w", err)
	}

	s.recordEvent(ctx, in.ProjectID, "task.handoff", map[string]interface{}{
		"task_id": in.TaskID,
		"verdict": in.Verdict,
		"from":    in.FromAgent,
	})

	idStr := fmt.Sprintf("%x-%x-%x-%x-%x",
		h.ID.Bytes[0:4], h.ID.Bytes[4:6], h.ID.Bytes[6:8], h.ID.Bytes[8:10], h.ID.Bytes[10:16])

	return &HandoffDetail{
		ID:                   idStr,
		ProjectID:            h.ProjectID,
		TaskID:               h.TaskID,
		FromAgent:            h.FromAgent,
		ToAgent:              h.ToAgent,
		Verdict:              h.Verdict,
		Summary:              h.Summary,
		ReproducibleEvidence: h.ReproducibleEvidence,
		Artifacts:            json.RawMessage(h.Artifacts),
		CreatedAt:            h.CreatedAt.Time,
	}, nil
}

func (s *Service) ListHandoffs(ctx context.Context, projectID, taskID string) ([]HandoffDetail, error) {
	rows, err := s.queries.ListHandoffsForTask(ctx, db.ListHandoffsForTaskParams{
		ProjectID: projectID,
		TaskID:    taskID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list handoffs: %w", err)
	}

	result := make([]HandoffDetail, len(rows))
	for i, h := range rows {
		idStr := fmt.Sprintf("%x-%x-%x-%x-%x",
			h.ID.Bytes[0:4], h.ID.Bytes[4:6], h.ID.Bytes[6:8], h.ID.Bytes[8:10], h.ID.Bytes[10:16])
		result[i] = HandoffDetail{
			ID:                   idStr,
			ProjectID:            h.ProjectID,
			TaskID:               h.TaskID,
			FromAgent:            h.FromAgent,
			ToAgent:              h.ToAgent,
			Verdict:              h.Verdict,
			Summary:              h.Summary,
			ReproducibleEvidence: h.ReproducibleEvidence,
			Artifacts:            json.RawMessage(h.Artifacts),
			CreatedAt:            h.CreatedAt.Time,
		}
	}
	return result, nil
}

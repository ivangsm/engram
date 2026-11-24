package memories

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

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

// EstimateTokens calculates an approximate token count for English/Code text.
func EstimateTokens(text string) int32 {
	if len(text) == 0 {
		return 0
	}
	// Conservative estimate: 1 token ~ 3.7 chars
	tokens := int32(float64(len(text)) / 3.7)
	if tokens == 0 {
		return 1
	}
	return tokens
}

type ManifestItem struct {
	Key              string    `json:"key"`
	Scope            string    `json:"scope"`
	Digest           string    `json:"digest"`
	TokenCount       int32     `json:"token_count"`
	LatestRevisionID *string   `json:"latest_revision_id,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (s *Service) GetManifest(ctx context.Context, projectID, scope string) ([]ManifestItem, error) {
	rows, err := s.queries.GetMemoryManifest(ctx, db.GetMemoryManifestParams{
		ProjectID: projectID,
		Column2:   scope,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch memory manifest: %w", err)
	}

	items := make([]ManifestItem, len(rows))
	for i, r := range rows {
		var revID *string
		if r.LatestRevisionID.Valid {
			idStr := fmt.Sprintf("%x-%x-%x-%x-%x",
				r.LatestRevisionID.Bytes[0:4],
				r.LatestRevisionID.Bytes[4:6],
				r.LatestRevisionID.Bytes[6:8],
				r.LatestRevisionID.Bytes[8:10],
				r.LatestRevisionID.Bytes[10:16])
			revID = &idStr
		}

		items[i] = ManifestItem{
			Key:              r.Key,
			Scope:            r.Scope,
			Digest:           r.Digest,
			TokenCount:       r.TokenCount,
			LatestRevisionID: revID,
			UpdatedAt:        r.UpdatedAt.Time,
		}
	}
	return items, nil
}

type MemoryDetail struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	Key        string    `json:"key"`
	Scope      string    `json:"scope"`
	Digest     string    `json:"digest"`
	Content    string    `json:"content"`
	TokenCount int32     `json:"token_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *Service) Recall(ctx context.Context, projectID, key string) (*MemoryDetail, error) {
	row, err := s.queries.RecallMemory(ctx, db.RecallMemoryParams{
		ProjectID: projectID,
		Key:       key,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("memory '%s' not found in project '%s'", key, projectID)
		}
		return nil, fmt.Errorf("failed to recall memory: %w", err)
	}

	idStr := fmt.Sprintf("%x-%x-%x-%x-%x",
		row.ID.Bytes[0:4],
		row.ID.Bytes[4:6],
		row.ID.Bytes[6:8],
		row.ID.Bytes[8:10],
		row.ID.Bytes[10:16])

	return &MemoryDetail{
		ID:         idStr,
		ProjectID:  row.ProjectID,
		Key:        row.Key,
		Scope:      row.Scope,
		Digest:     row.Digest,
		Content:    row.Content,
		TokenCount: row.TokenCount,
		CreatedAt:  row.CreatedAt.Time,
		UpdatedAt:  row.UpdatedAt.Time,
	}, nil
}

type StoreInput struct {
	ProjectID          string
	Key                string
	Scope              string
	Digest             string
	Content            string
	AuthorUser         string
	AuthorAgentModel   string
	MachineID          string
	TaskID             string
	Rationale          string
	ExpectedParentHash *string
	Embedding          []float32
}

type StaleRevisionError struct {
	Key            string `json:"key"`
	HeadRev        int32  `json:"head_rev"`
	HeadHash       string `json:"head_hash"`
	CurrentContent string `json:"current_content"`
	DiffUnified    string `json:"diff_unified"`
}

func (e *StaleRevisionError) Error() string {
	return fmt.Sprintf("stale revision collision against key '%s' (head rev %d, hash %s); reconcile against HEAD", e.Key, e.HeadRev, e.HeadHash)
}

type StoreResult struct {
	MemoryID       string `json:"memory_id"`
	RevisionID     string `json:"revision_id"`
	RevisionNumber int32  `json:"revision_number"`
	ContentHash    string `json:"content_hash"`
	TokenCount     int32  `json:"token_count"`
}

func (s *Service) Store(ctx context.Context, in StoreInput) (*StoreResult, error) {
	if in.Scope == "" {
		in.Scope = "global"
	}
	if in.AuthorUser == "" {
		in.AuthorUser = "unknown"
	}
	if in.AuthorAgentModel == "" {
		in.AuthorAgentModel = "unknown"
	}
	if in.MachineID == "" {
		in.MachineID = "unknown"
	}
	if in.TaskID == "" {
		in.TaskID = "untracked"
	}
	if in.Rationale == "" {
		in.Rationale = "routine update"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	// Ensure project exists
	_, err = qtx.CreateProject(ctx, db.CreateProjectParams{
		ID:          in.ProjectID,
		Name:        in.ProjectID,
		Description: "Auto-created project namespace",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to ensure project exists: %w", err)
	}

	// Check if existing memory exists to determine parent hash & diff
	existing, err := qtx.RecallMemory(ctx, db.RecallMemoryParams{
		ProjectID: in.ProjectID,
		Key:       in.Key,
	})

	var parentHash *string
	var prevContent string
	var revisionNumber int32 = 1

	if err == nil {
		// Existing memory found
		prevContent = existing.Content
		if existing.LatestRevisionID.Valid {
			lastRev, err := qtx.GetLatestRevisionForMemory(ctx, existing.ID)
			if err == nil {
				revisionNumber = lastRev.RevisionNumber + 1
				pHash := lastRev.ContentHash
				parentHash = &pHash

				// Strict OCC validation
				if in.ExpectedParentHash != nil && *in.ExpectedParentHash != lastRev.ContentHash {
					diff, _ := GenerateUnifiedDiff(existing.Content, in.Content, in.Key)
					return nil, &StaleRevisionError{
						Key:            in.Key,
						HeadRev:        lastRev.RevisionNumber,
						HeadHash:       lastRev.ContentHash,
						CurrentContent: existing.Content,
						DiffUnified:    diff,
					}
				}
			}
		}
	}

	contentHash := ComputeSHA256(in.Content)
	var patchDiff *string
	if prevContent != "" && prevContent != in.Content {
		diff, diffErr := GenerateUnifiedDiff(prevContent, in.Content, in.Key)
		if diffErr == nil && diff != "" {
			patchDiff = &diff
		}
	}

	tokenCount := EstimateTokens(in.Content)

	var vec *pgvector.Vector
	if len(in.Embedding) > 0 {
		v := pgvector.NewVector(in.Embedding)
		vec = &v
	}

	// 1. Upsert memory record
	mem, err := qtx.UpsertMemory(ctx, db.UpsertMemoryParams{
		ProjectID:  in.ProjectID,
		Key:        in.Key,
		Scope:      in.Scope,
		Digest:     in.Digest,
		Content:    in.Content,
		TokenCount: tokenCount,
		Embedding:  vec,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upsert memory: %w", err)
	}

	// 2. Insert audit revision
	rev, err := qtx.CreateRevision(ctx, db.CreateRevisionParams{
		MemoryID:         mem.ID,
		ProjectID:        in.ProjectID,
		RevisionNumber:   revisionNumber,
		AuthorUser:       in.AuthorUser,
		AuthorAgentModel: in.AuthorAgentModel,
		MachineID:        in.MachineID,
		TaskID:           in.TaskID,
		Rationale:        in.Rationale,
		ContentHash:      contentHash,
		ParentHash:       parentHash,
		PatchDiff:        patchDiff,
		Content:          in.Content,
		Digest:           in.Digest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create revision: %w", err)
	}

	// 3. Link latest revision
	err = qtx.SetMemoryLatestRevision(ctx, db.SetMemoryLatestRevisionParams{
		ID:               mem.ID,
		LatestRevisionID: rev.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set latest revision: %w", err)
	}

	// 4. Record transactional outbox event
	payloadBytes, _ := json.Marshal(map[string]interface{}{
		"action":      "memory_stored",
		"key":         in.Key,
		"rev":         revisionNumber,
		"hash":        contentHash,
		"token_count": tokenCount,
	})
	_, _ = qtx.CreateEvent(ctx, db.CreateEventParams{
		ProjectID: in.ProjectID,
		Topic:     "memory.stored",
		Payload:   payloadBytes,
	})

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Zero-payload wake-up ping for outbox listener
	_, _ = s.pool.Exec(ctx, "NOTIFY engram_events, 'ping'")

	memIDStr := fmt.Sprintf("%x-%x-%x-%x-%x",
		mem.ID.Bytes[0:4], mem.ID.Bytes[4:6], mem.ID.Bytes[6:8], mem.ID.Bytes[8:10], mem.ID.Bytes[10:16])
	revIDStr := fmt.Sprintf("%x-%x-%x-%x-%x",
		rev.ID.Bytes[0:4], rev.ID.Bytes[4:6], rev.ID.Bytes[6:8], rev.ID.Bytes[8:10], rev.ID.Bytes[10:16])

	return &StoreResult{
		MemoryID:       memIDStr,
		RevisionID:     revIDStr,
		RevisionNumber: revisionNumber,
		ContentHash:    contentHash,
		TokenCount:     tokenCount,
	}, nil
}

type BlameEntry struct {
	RevisionNumber   int32     `json:"revision_number"`
	AuthorUser       string    `json:"author_user"`
	AuthorAgentModel string    `json:"author_agent_model"`
	MachineID        string    `json:"machine_id"`
	TaskID           string    `json:"task_id"`
	Rationale        string    `json:"rationale"`
	ContentHash      string    `json:"content_hash"`
	ParentHash       *string   `json:"parent_hash"`
	PatchDiff        *string   `json:"patch_diff,omitempty"`
	Digest           string    `json:"digest"`
	CreatedAt        time.Time `json:"created_at"`
}

type BlameResult struct {
	Key          string       `json:"key"`
	Scope        string       `json:"scope"`
	CurrentHash  string       `json:"current_hash"`
	TotalTokens  int32        `json:"total_tokens"`
	Revisions    []BlameEntry `json:"revisions"`
	ChainValid   bool         `json:"chain_valid"`
	ChainMessage string       `json:"chain_message,omitempty"`
}

func (s *Service) Blame(ctx context.Context, projectID, key string) (*BlameResult, error) {
	mem, err := s.queries.RecallMemory(ctx, db.RecallMemoryParams{
		ProjectID: projectID,
		Key:       key,
	})
	if err != nil {
		return nil, fmt.Errorf("memory '%s' not found: %w", key, err)
	}

	revisions, err := s.queries.ListRevisionsForMemoryWithContent(ctx, mem.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list revisions: %w", err)
	}

	entries := make([]BlameEntry, len(revisions))
	chainItems := make([]RevisionChainItem, len(revisions))

	// Revisions are in DESC order; reverse for chain verification
	for i, r := range revisions {
		entries[i] = BlameEntry{
			RevisionNumber:   r.RevisionNumber,
			AuthorUser:       r.AuthorUser,
			AuthorAgentModel: r.AuthorAgentModel,
			MachineID:        r.MachineID,
			TaskID:           r.TaskID,
			Rationale:        r.Rationale,
			ContentHash:      r.ContentHash,
			ParentHash:       r.ParentHash,
			PatchDiff:        r.PatchDiff,
			Digest:           r.Digest,
			CreatedAt:        r.CreatedAt.Time,
		}

		chainIdx := len(revisions) - 1 - i
		chainItems[chainIdx] = RevisionChainItem{
			RevisionNumber: r.RevisionNumber,
			Content:        r.Content,
			ContentHash:    r.ContentHash,
			ParentHash:     r.ParentHash,
		}
	}

	valid, msg := VerifyHashChain(chainItems)

	currentHash := ""
	if len(revisions) > 0 {
		currentHash = revisions[0].ContentHash
	}

	return &BlameResult{
		Key:          mem.Key,
		Scope:        mem.Scope,
		CurrentHash:  currentHash,
		TotalTokens:  mem.TokenCount,
		Revisions:    entries,
		ChainValid:   valid,
		ChainMessage: msg,
	}, nil
}

type SearchItem struct {
	Key         string  `json:"key"`
	Scope       string  `json:"scope"`
	Digest      string  `json:"digest"`
	Content     string  `json:"content"`
	TokenCount  int32   `json:"token_count"`
	Score       float64 `json:"score"`
	VectorScore float64 `json:"vector_score,omitempty"`
	TextRank    float64 `json:"text_rank,omitempty"`
}

func (s *Service) Search(ctx context.Context, projectID, query string, embedding []float32, limit int32) ([]SearchItem, error) {
	if limit <= 0 {
		limit = 10
	}

	if len(embedding) > 0 {
		vec := pgvector.NewVector(embedding)
		// 0.65 vector weight, 0.35 text weight, distance threshold 0.8
		rows, err := s.queries.SearchMemoriesHybrid(ctx, db.SearchMemoriesHybridParams{
			ProjectID:      projectID,
			PlaintoTsquery: query,
			Embedding:      &vec,
			Column4:        0.65,
			Column5:        0.80,
			Limit:          limit,
		})
		if err != nil {
			return nil, fmt.Errorf("hybrid search failed: %w", err)
		}

		results := make([]SearchItem, len(rows))
		for i, r := range rows {
			results[i] = SearchItem{
				Key:         r.Key,
				Scope:       r.Scope,
				Digest:      r.Digest,
				Content:     r.Content,
				TokenCount:  r.TokenCount,
				Score:       r.CombinedScore,
				VectorScore: r.VectorSimilarity,
				TextRank:    r.TextRank,
			}
		}
		return results, nil
	}

	// Fulltext search fallback
	rows, err := s.queries.SearchMemoriesText(ctx, db.SearchMemoriesTextParams{
		ProjectID:      projectID,
		PlaintoTsquery: query,
		Limit:          limit,
	})
	if err != nil {
		return nil, fmt.Errorf("text search failed: %w", err)
	}

	results := make([]SearchItem, len(rows))
	for i, r := range rows {
		results[i] = SearchItem{
			Key:        r.Key,
			Scope:      r.Scope,
			Digest:     r.Digest,
			Content:    r.Content,
			TokenCount: r.TokenCount,
			Score:      r.TextRank,
			TextRank:   r.TextRank,
		}
	}
	return results, nil
}

package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrSummaryTooLong     = errors.New("handoff summary exceeds 150 words standard contract limit")
	ErrSummaryEmpty       = errors.New("handoff summary must not be empty")
	ErrEvidenceEmpty      = errors.New("handoff reproducible_evidence must not be empty")
	ErrInvalidVerdict     = errors.New("invalid verdict: must be one of PASS, FAIL, BLOCKED, NEEDS_REVIEW, HANDOFF, LEASE_EXPIRED")
	ErrTaskNotFoundOrBusy = errors.New("task not found or currently locked under active lease")
	ErrLeaseLost          = errors.New("task lease expired or claimed by another worker (ERR_LEASE_LOST)")
)

var validVerdicts = map[string]bool{
	"PASS":          true,
	"FAIL":          true,
	"BLOCKED":       true,
	"NEEDS_REVIEW":  true,
	"HANDOFF":       true,
	"LEASE_EXPIRED": true,
}

// CountWords counts the number of whitespace-delimited words in s.
func CountWords(s string) int {
	return len(strings.Fields(s))
}

type HandoffInput struct {
	ProjectID            string          `json:"project_id"`
	TaskID               string          `json:"task_id"`
	FromAgent            string          `json:"from_agent"`
	ToAgent              *string         `json:"to_agent,omitempty"`
	Verdict              string          `json:"verdict"`
	Summary              string          `json:"summary"`
	ReproducibleEvidence string          `json:"reproducible_evidence"`
	Artifacts            json.RawMessage `json:"artifacts,omitempty"`
	CompleteTask         bool            `json:"complete_task,omitempty"`
	LeaseID              string          `json:"lease_id,omitempty"`
}

func ValidateHandoff(in HandoffInput) error {
	if strings.TrimSpace(in.Summary) == "" {
		return ErrSummaryEmpty
	}

	wordCount := CountWords(in.Summary)
	if wordCount > 150 {
		return fmt.Errorf("%w: got %d words", ErrSummaryTooLong, wordCount)
	}

	if strings.TrimSpace(in.ReproducibleEvidence) == "" {
		return ErrEvidenceEmpty
	}

	upperVerdict := strings.ToUpper(strings.TrimSpace(in.Verdict))
	if !validVerdicts[upperVerdict] {
		return fmt.Errorf("%w: '%s'", ErrInvalidVerdict, in.Verdict)
	}

	return nil
}

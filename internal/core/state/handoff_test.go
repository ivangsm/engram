package state

import (
	"strings"
	"testing"
)

func TestValidateHandoff_WordLimit(t *testing.T) {
	// 151 words
	longSummary := strings.Repeat("word ", 151)
	err := ValidateHandoff(HandoffInput{
		ProjectID:            "p1",
		TaskID:               "t1",
		FromAgent:            "agent-a",
		Verdict:              "PASS",
		Summary:              longSummary,
		ReproducibleEvidence: "go test passed",
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds 150 words") {
		t.Fatalf("expected 150 words error, got %v", err)
	}

	// 150 words exact - valid
	exact150 := strings.Repeat("word ", 150)
	err = ValidateHandoff(HandoffInput{
		ProjectID:            "p1",
		TaskID:               "t1",
		FromAgent:            "agent-a",
		Verdict:              "PASS",
		Summary:              exact150,
		ReproducibleEvidence: "go test passed",
	})
	if err != nil {
		t.Fatalf("expected valid 150 words, got error: %v", err)
	}
}

func TestValidateHandoff_Verdict(t *testing.T) {
	for _, v := range []string{"PASS", "FAIL", "BLOCKED", "NEEDS_REVIEW", "HANDOFF", "LEASE_EXPIRED"} {
		err := ValidateHandoff(HandoffInput{
			ProjectID:            "p1",
			TaskID:               "t1",
			FromAgent:            "agent-a",
			Verdict:              v,
			Summary:              "Valid short summary.",
			ReproducibleEvidence: "Evidence here.",
		})
		if err != nil {
			t.Fatalf("verdict %s should be valid, got: %v", v, err)
		}
	}

	err := ValidateHandoff(HandoffInput{
		ProjectID:            "p1",
		TaskID:               "t1",
		FromAgent:            "agent-a",
		Verdict:              "INVALID_STATUS",
		Summary:              "Summary.",
		ReproducibleEvidence: "Evidence.",
	})
	if err == nil {
		t.Fatalf("expected error for invalid verdict")
	}
}

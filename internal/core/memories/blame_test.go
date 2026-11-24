package memories

import (
	"testing"
)

func TestComputeSHA256(t *testing.T) {
	c1 := "Hello World"
	c2 := "Hello World\r\n"
	c3 := "Hello World\n"

	h1 := ComputeSHA256(c1)
	h2 := ComputeSHA256(c2)
	h3 := ComputeSHA256(c3)

	if h2 != h3 {
		t.Fatalf("expected CRLF normalization to match LF, got %s != %s", h2, h3)
	}

	if len(h1) != 64 {
		t.Fatalf("expected 64 character hex string, got %d", len(h1))
	}
}

func TestGenerateUnifiedDiff(t *testing.T) {
	oldContent := "line 1\nline 2\nline 3\n"
	newContent := "line 1\nline 2 (modified)\nline 3\nline 4 (added)\n"

	diff, err := GenerateUnifiedDiff(oldContent, newContent, "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff == "" {
		t.Fatalf("expected non-empty diff")
	}

	// Identical contents should produce empty diff
	sameDiff, err := GenerateUnifiedDiff(oldContent, oldContent, "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sameDiff != "" {
		t.Fatalf("expected empty diff for identical content, got %s", sameDiff)
	}
}

func TestVerifyHashChain(t *testing.T) {
	c1 := "initial state"
	h1 := ComputeSHA256(c1)

	c2 := "second state"
	h2 := ComputeSHA256(c2)

	chain := []RevisionChainItem{
		{RevisionNumber: 1, Content: c1, ContentHash: h1, ParentHash: nil},
		{RevisionNumber: 2, Content: c2, ContentHash: h2, ParentHash: &h1},
	}

	valid, msg := VerifyHashChain(chain)
	if !valid {
		t.Fatalf("expected valid chain, got error: %s", msg)
	}

	// Corrupt content hash
	corruptChain := []RevisionChainItem{
		{RevisionNumber: 1, Content: c1, ContentHash: h1, ParentHash: nil},
		{RevisionNumber: 2, Content: "tampered content", ContentHash: h2, ParentHash: &h1},
	}
	validCorrupt, _ := VerifyHashChain(corruptChain)
	if validCorrupt {
		t.Fatalf("expected chain verification to fail for tampered content")
	}
}

func TestEstimateTokens(t *testing.T) {
	if EstimateTokens("") != 0 {
		t.Fatalf("expected 0 for empty text")
	}
	if EstimateTokens("a") != 1 {
		t.Fatalf("expected at least 1 token for non-empty text")
	}
	tokens := EstimateTokens("func main() { fmt.Println(\"hello world\") }")
	if tokens < 5 {
		t.Fatalf("expected >= 5 tokens, got %d", tokens)
	}
}

func TestStaleRevisionError(t *testing.T) {
	err := &StaleRevisionError{
		Key:            "arch/jwt",
		HeadRev:        2,
		HeadHash:       "hash-abc",
		CurrentContent: "content",
		DiffUnified:    "diff",
	}
	msg := err.Error()
	if msg == "" {
		t.Fatalf("expected non-empty error message")
	}
}

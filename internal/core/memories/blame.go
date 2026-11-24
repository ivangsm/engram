package memories

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// ComputeSHA256 returns the hex-encoded SHA-256 hash of the normalized content.
func ComputeSHA256(content string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	hash := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(hash[:])
}

// GenerateUnifiedDiff generates a unified diff between oldContent and newContent.
func GenerateUnifiedDiff(oldContent, newContent, label string) (string, error) {
	if oldContent == newContent {
		return "", nil
	}

	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(oldContent),
		B:        difflib.SplitLines(newContent),
		FromFile: fmt.Sprintf("%s (previous)", label),
		ToFile:   fmt.Sprintf("%s (current)", label),
		Context:  3,
	}

	text, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return "", fmt.Errorf("failed to generate unified diff: %w", err)
	}

	return text, nil
}

// VerifyHashChain verifies that a series of revisions forms an unbroken cryptographic chain.
type RevisionChainItem struct {
	RevisionNumber int32
	Content        string
	ContentHash    string
	ParentHash     *string
}

func VerifyHashChain(chain []RevisionChainItem) (bool, string) {
	if len(chain) == 0 {
		return true, ""
	}

	for i := 0; i < len(chain); i++ {
		rev := chain[i]
		computedHash := ComputeSHA256(rev.Content)
		if computedHash != rev.ContentHash {
			return false, fmt.Sprintf("revision %d hash mismatch: expected %s, got %s",
				rev.RevisionNumber, rev.ContentHash, computedHash)
		}

		if i > 0 {
			prevRev := chain[i-1]
			if rev.ParentHash == nil || *rev.ParentHash != prevRev.ContentHash {
				return false, fmt.Sprintf("revision %d parent hash mismatch: points to %v, expected %s",
					rev.RevisionNumber, rev.ParentHash, prevRev.ContentHash)
			}
		} else {
			// First revision in chain
			if rev.ParentHash != nil && *rev.ParentHash != "" {
				return false, fmt.Sprintf("revision 1 must have null parent hash, got %s", *rev.ParentHash)
			}
		}
	}

	return true, ""
}

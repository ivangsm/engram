package memories

import "testing"

func TestResolveDigestForStore(t *testing.T) {
	// 1. Explicit digest always wins
	got := resolveDigestForStore("Custom curated digest", "Old digest", "# Title\nBody")
	if got != "Custom curated digest" {
		t.Fatalf("expected explicit digest, got %q", got)
	}

	// 2. Empty explicit digest preserves existing curated digest on updates (rev > 1)
	got = resolveDigestForStore("", "Existing curated digest", "# New Heading\nUpdated body content")
	if got != "Existing curated digest" {
		t.Fatalf("expected existing digest to be preserved, got %q", got)
	}

	// 3. New memory (empty existing digest) derives single-line fallback
	got = resolveDigestForStore("", "", "\n\n# First Line\nSecond Line")
	if got != "# First Line" {
		t.Fatalf("expected '# First Line', got %q", got)
	}

	// 4. Long first line truncates safely at 120 runes
	long := ""
	for i := 0; i < 150; i++ {
		long += "a"
	}
	got = resolveDigestForStore("", "", long)
	if len([]rune(got)) != 120 || got[len(got)-3:] != "..." {
		t.Fatalf("expected 120-char truncated digest ending with '...', got len=%d (%q)", len([]rune(got)), got)
	}
}

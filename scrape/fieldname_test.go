package scrape

import (
	"regexp"
	"testing"
)

func TestComputeFieldHash_Stability(t *testing.T) {
	// Same selector must produce same hash
	h1 := ComputeFieldHash("div.event-title")
	h2 := ComputeFieldHash("div.event-title")
	if h1 != h2 {
		t.Errorf("ComputeFieldHash not stable: got %q and %q", h1, h2)
	}

	// Different selectors produce different hashes
	h3 := ComputeFieldHash("div.event-date")
	if h1 == h3 {
		t.Errorf("Different selectors should produce different hashes: %q == %q", h1, h3)
	}
}

func TestComputeFieldHash_Normalization(t *testing.T) {
	// Whitespace trimming
	h1 := ComputeFieldHash("div.title")
	h2 := ComputeFieldHash("  div.title  ")
	if h1 != h2 {
		t.Errorf("Whitespace normalization failed: %q != %q", h1, h2)
	}
}

func TestComputeFieldHash_Format(t *testing.T) {
	hash := ComputeFieldHash("div.test")
	// CRC32 produces 8 hex chars
	if len(hash) != 8 {
		t.Errorf("Hash should be 8 chars, got %d: %q", len(hash), hash)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(hash) {
		t.Errorf("Hash should be lowercase hex, got %q", hash)
	}
}

func TestGenerateFieldName_TextContent(t *testing.T) {
	name := GenerateFieldName("div.title", "", 0)
	// Format: F<hash>--<index> (double hyphen for text content)
	if !regexp.MustCompile(`^F[0-9a-f]{8}--0$`).MatchString(name) {
		t.Errorf("Text content field name format wrong: got %q", name)
	}
}

func TestGenerateFieldName_Attribute(t *testing.T) {
	name := GenerateFieldName("a.link", "href", 0)
	// Format: F<hash>-href-<index>
	if !regexp.MustCompile(`^F[0-9a-f]{8}-href-0$`).MatchString(name) {
		t.Errorf("Attribute field name format wrong: got %q", name)
	}
}

func TestGenerateFieldName_MultipleTextNodes(t *testing.T) {
	name0 := GenerateFieldName("div.content", "", 0)
	name1 := GenerateFieldName("div.content", "", 1)
	name2 := GenerateFieldName("div.content", "", 2)

	// Same selector, different text node indices
	if name0 == name1 || name1 == name2 {
		t.Errorf("Different text nodes should have different names: %q, %q, %q", name0, name1, name2)
	}

	// Check format
	for i, name := range []string{name0, name1, name2} {
		pattern := regexp.MustCompile(`^F[0-9a-f]{8}--` + string('0'+byte(i)) + `$`)
		if !pattern.MatchString(name) {
			t.Errorf("Text node %d name format wrong: %q", i, name)
		}
	}
}

func TestParseFieldName_TextContent(t *testing.T) {
	comp, ok := ParseFieldName("F1a2b3c4d--0")
	if !ok {
		t.Fatal("ParseFieldName failed for text content field")
	}
	if comp.Hash != "1a2b3c4d" {
		t.Errorf("Hash = %q, want %q", comp.Hash, "1a2b3c4d")
	}
	if comp.Attribute != "" {
		t.Errorf("Attribute = %q, want empty", comp.Attribute)
	}
	if comp.TextNodeIndex != 0 {
		t.Errorf("TextNodeIndex = %d, want 0", comp.TextNodeIndex)
	}
}

func TestParseFieldName_Attribute(t *testing.T) {
	comp, ok := ParseFieldName("Fabcdef12-href-3")
	if !ok {
		t.Fatal("ParseFieldName failed for attribute field")
	}
	if comp.Hash != "abcdef12" {
		t.Errorf("Hash = %q, want %q", comp.Hash, "abcdef12")
	}
	if comp.Attribute != "href" {
		t.Errorf("Attribute = %q, want %q", comp.Attribute, "href")
	}
	if comp.TextNodeIndex != 3 {
		t.Errorf("TextNodeIndex = %d, want 3", comp.TextNodeIndex)
	}
}

func TestParseFieldName_Invalid(t *testing.T) {
	invalidNames := []string{
		"",
		"invalid",
		"F",
		"F1a2b3c4",            // missing suffix
		"F1a2b3c4-",           // incomplete
		"F1a2b3c4-href",       // missing index
		"X1a2b3c4--0",         // wrong prefix
		"F1a2b3c4--abc",       // non-numeric index
		"F1a2b3c4-HREF-0",     // uppercase attribute
		"f1a2b3c4--0",         // lowercase F
	}

	for _, name := range invalidNames {
		if _, ok := ParseFieldName(name); ok {
			t.Errorf("ParseFieldName(%q) should have failed", name)
		}
	}
}

func TestIsGoskyrFieldName(t *testing.T) {
	validNames := []string{
		"F1a2b3c4d--0",
		"Fabcdef12-href-0",
		"F00000000-src-99",
		"Fffffffff--0",
	}

	for _, name := range validNames {
		if !IsGoskyrFieldName(name) {
			t.Errorf("IsGoskyrFieldName(%q) = false, want true", name)
		}
	}

	invalidNames := []string{
		"not-a-field",
		"Aurl",
		"Atitle",
	}

	for _, name := range invalidNames {
		if IsGoskyrFieldName(name) {
			t.Errorf("IsGoskyrFieldName(%q) = true, want false", name)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	testCases := []struct {
		selector      string
		attr          string
		textNodeIndex int
	}{
		{"div.title", "", 0},
		{"div.content", "", 5},
		{"a.link", "href", 0},
		{"img.photo", "src", 0},
		{"time.date", "datetime", 0},
	}

	for _, tc := range testCases {
		name := GenerateFieldName(tc.selector, tc.attr, tc.textNodeIndex)
		comp, ok := ParseFieldName(name)
		if !ok {
			t.Errorf("Failed to parse generated name %q", name)
			continue
		}

		expectedHash := ComputeFieldHash(tc.selector)
		if comp.Hash != expectedHash {
			t.Errorf("Hash mismatch for %q: got %q, want %q", name, comp.Hash, expectedHash)
		}
		if comp.Attribute != tc.attr {
			t.Errorf("Attribute mismatch for %q: got %q, want %q", name, comp.Attribute, tc.attr)
		}
		if comp.TextNodeIndex != tc.textNodeIndex {
			t.Errorf("TextNodeIndex mismatch for %q: got %d, want %d", name, comp.TextNodeIndex, tc.textNodeIndex)
		}
	}
}

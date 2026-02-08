package scrape

import (
	"fmt"
	"hash/crc32"
	"regexp"
	"strconv"
	"strings"
)

// FieldNameFormat documents the goskyr field naming convention.
//
// Format: F<hash>[-<attr>]-<textnode>
//
// Components:
//
//	F           - Literal prefix identifying as goskyr field
//	<hash>      - 8-char hex hash of normalized selector path (CRC32)
//	<attr>      - Optional: attribute name (href, src, datetime, etc.)
//	              Empty string for text content, shown as double hyphen "--"
//	<textnode>  - 0-based text node index within element
//
// Examples:
//
//	Fa1b2c3d4--0       - Text content, first text node
//	Fa1b2c3d4--1       - Text content, second text node
//	Fa1b2c3d4-href-0   - href attribute value
//	Fa1b2c3d4-src-0    - src attribute value
//	Fa1b2c3d4-datetime-0 - datetime attribute value
//
// Stability Guarantee:
//
//	Same selector path always produces same hash.
//	The hash is computed using CRC32-IEEE on the normalized path string.
//
// Note: The text node index is the index of the text node within the element,
// NOT the position of the item in a list. All items on a list page will have
// identical field keys.
const FieldNameFormat = "F<hash>[-<attr>]-<textnode>"

// ComputeFieldHash returns a stable 8-char hex hash for a selector path.
// Uses CRC32-IEEE which produces 32-bit (8 hex char) hashes.
//
// The hash is deterministic: same selector path always produces same hash.
func ComputeFieldHash(selectorPath string) string {
	// Normalize: trim whitespace
	normalized := strings.TrimSpace(selectorPath)

	// Compute CRC32 hash
	hash := crc32.ChecksumIEEE([]byte(normalized))
	return fmt.Sprintf("%08x", hash)
}

// GenerateFieldName creates a field name from selector path, attribute, and text node index.
//
// Parameters:
//   - selectorPath: The DOM selector path (e.g., "div.event > span.title")
//   - attr: Attribute name ("href", "src", etc.) or empty string for text content
//   - textNodeIndex: 0-based index of text node within element
//
// Returns field name in format: F<hash>[-<attr>]-<textnode>
func GenerateFieldName(selectorPath, attr string, textNodeIndex int) string {
	hash := ComputeFieldHash(selectorPath)
	if attr == "" {
		// Text content: F<hash>--<index> (double hyphen because attr is empty)
		return fmt.Sprintf("F%s--%d", hash, textNodeIndex)
	}
	// Attribute: F<hash>-<attr>-<index>
	return fmt.Sprintf("F%s-%s-%d", hash, attr, textNodeIndex)
}

// FieldNameComponents contains the parsed components of a goskyr field name.
type FieldNameComponents struct {
	Hash          string // 8-char hex hash
	Attribute     string // Attribute name, or empty for text content
	TextNodeIndex int    // 0-based text node index
}

// fieldNamePattern matches goskyr field names: F<hash>-<attr>-<index> or F<hash>--<index>
var fieldNamePattern = regexp.MustCompile(`^F([0-9a-f]{8})-([a-z_]*)-(\d+)$`)

// ParseFieldName extracts components from a field name.
// Returns the components and true if parsing succeeded, or zero values and false if not.
//
// Examples:
//
//	ParseFieldName("Fa1b2c3d4--0") → {Hash: "a1b2c3d4", Attribute: "", TextNodeIndex: 0}, true
//	ParseFieldName("Fa1b2c3d4-href-0") → {Hash: "a1b2c3d4", Attribute: "href", TextNodeIndex: 0}, true
//	ParseFieldName("invalid") → {}, false
func ParseFieldName(name string) (FieldNameComponents, bool) {
	matches := fieldNamePattern.FindStringSubmatch(name)
	if matches == nil {
		return FieldNameComponents{}, false
	}

	index, err := strconv.Atoi(matches[3])
	if err != nil {
		return FieldNameComponents{}, false
	}

	return FieldNameComponents{
		Hash:          matches[1],
		Attribute:     matches[2],
		TextNodeIndex: index,
	}, true
}

// IsGoskyrFieldName checks if a string is a valid goskyr field name.
func IsGoskyrFieldName(name string) bool {
	_, ok := ParseFieldName(name)
	return ok
}

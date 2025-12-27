package scrape

import (
	"fmt"
	"regexp"
	"strings"
)

// DerivedField creates new fields from existing ones via templates or regex
type DerivedField struct {
	Source   string          `yaml:"source"`   // source field name
	Template string          `yaml:"template"` // "{name} | {date}" - delimiter-based
	Regex    string          `yaml:"regex"`    // fallback: named capture groups (?P<name>...)
	Outputs  []DerivedOutput `yaml:"outputs"`

	// Compiled regex (populated during initialization)
	compiledRegex *regexp.Regexp
	// Parsed template info
	symbols    []string
	delimiter  string
	hasTemplate bool
}

// DerivedOutput maps a template symbol to a target field
type DerivedOutput struct {
	Symbol    string           `yaml:"symbol"`
	Target    string           `yaml:"target"`
	Condition *OutputCondition `yaml:"condition,omitempty"`
	Value     string           `yaml:"value,omitempty"` // override extracted value
}

// OutputCondition controls conditional field mapping
type OutputCondition struct {
	Equals          string `yaml:"equals,omitempty"`
	NotEquals       string `yaml:"not_equals,omitempty"`
	Matches         string `yaml:"matches,omitempty"`
	NotMatches      string `yaml:"not_matches,omitempty"`
	CaseInsensitive bool   `yaml:"case_insensitive,omitempty"`

	// Compiled patterns
	matchesRegex    *regexp.Regexp
	notMatchesRegex *regexp.Regexp
}

// Initialize prepares the DerivedField for use by compiling patterns
func (df *DerivedField) Initialize() error {
	if df.Template != "" {
		symbols, delimiter, err := parseTemplatePattern(df.Template)
		if err != nil {
			return fmt.Errorf("parsing template %q: %w", df.Template, err)
		}
		df.symbols = symbols
		df.delimiter = delimiter
		df.hasTemplate = true
	} else if df.Regex != "" {
		re, err := regexp.Compile(df.Regex)
		if err != nil {
			return fmt.Errorf("compiling regex %q: %w", df.Regex, err)
		}
		df.compiledRegex = re
	} else {
		return fmt.Errorf("DerivedField requires either template or regex")
	}

	// Initialize output conditions
	for i := range df.Outputs {
		if err := df.Outputs[i].Condition.Initialize(); err != nil {
			return fmt.Errorf("initializing condition for output %d: %w", i, err)
		}
	}

	return nil
}

// Initialize compiles regex patterns in the condition
func (c *OutputCondition) Initialize() error {
	if c == nil {
		return nil
	}

	if c.Matches != "" {
		pattern := c.Matches
		if c.CaseInsensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("compiling matches pattern %q: %w", c.Matches, err)
		}
		c.matchesRegex = re
	}

	if c.NotMatches != "" {
		pattern := c.NotMatches
		if c.CaseInsensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("compiling not_matches pattern %q: %w", c.NotMatches, err)
		}
		c.notMatchesRegex = re
	}

	return nil
}

// Extract parses the input string and returns symbol values
func (df *DerivedField) Extract(input string) (map[string]string, error) {
	if df.hasTemplate {
		return parseTemplateInput(df.symbols, df.delimiter, input)
	}
	return parseRegexInput(df.compiledRegex, input)
}

// parseTemplatePattern extracts symbols and delimiter from a template string
// "{a} | {b} | {c}" → symbols: ["a", "b", "c"], delimiter: " | "
func parseTemplatePattern(template string) ([]string, string, error) {
	symbolRe := regexp.MustCompile(`\{([^}]+)\}`)

	matches := symbolRe.FindAllStringSubmatchIndex(template, -1)
	if len(matches) == 0 {
		return nil, "", fmt.Errorf("no symbols found in template %q", template)
	}

	var symbols []string
	var delimiters []string
	lastEnd := 0

	for i, match := range matches {
		// Extract delimiter (text between previous symbol and this one)
		if i > 0 {
			delim := template[lastEnd:match[0]]
			delimiters = append(delimiters, delim)
		}
		// Extract symbol name
		symbols = append(symbols, template[match[2]:match[3]])
		lastEnd = match[1]
	}

	// Validate consistent delimiter
	if len(delimiters) == 0 {
		// Single symbol, no delimiter needed
		return symbols, "", nil
	}

	delimiter := delimiters[0]
	for _, d := range delimiters {
		if d != delimiter {
			return nil, "", fmt.Errorf("mixed delimiters in template: %q vs %q (use regex for complex patterns)", delimiter, d)
		}
	}

	return symbols, delimiter, nil
}

// parseTemplateInput splits input by delimiter and assigns to symbols
func parseTemplateInput(symbols []string, delimiter string, input string) (map[string]string, error) {
	result := make(map[string]string)

	if delimiter == "" {
		// Single symbol gets entire input
		if len(symbols) == 1 {
			result[symbols[0]] = strings.TrimSpace(input)
		}
		return result, nil
	}

	parts := strings.Split(input, delimiter)

	for i, sym := range symbols {
		if i < len(parts) {
			if i == len(symbols)-1 && len(parts) > len(symbols) {
				// Last symbol gets remaining parts joined
				result[sym] = strings.TrimSpace(strings.Join(parts[i:], delimiter))
			} else {
				result[sym] = strings.TrimSpace(parts[i])
			}
		} else {
			result[sym] = ""
		}
	}

	return result, nil
}

// parseRegexInput extracts named groups from input using compiled regex
func parseRegexInput(re *regexp.Regexp, input string) (map[string]string, error) {
	result := make(map[string]string)

	match := re.FindStringSubmatch(input)
	if match == nil {
		return result, nil // No match, return empty
	}

	names := re.SubexpNames()
	for i, name := range names {
		if name != "" && i < len(match) {
			result[name] = strings.TrimSpace(match[i])
		}
	}

	return result, nil
}

// Evaluate checks if the condition is satisfied by the value
func (c *OutputCondition) Evaluate(value string) bool {
	if c == nil {
		return true // No condition = always match
	}

	testValue := value
	compareEquals := c.Equals
	compareNotEquals := c.NotEquals

	if c.CaseInsensitive {
		testValue = strings.ToLower(value)
		compareEquals = strings.ToLower(c.Equals)
		compareNotEquals = strings.ToLower(c.NotEquals)
	}

	// Check equals
	if c.Equals != "" && testValue != compareEquals {
		return false
	}

	// Check not_equals
	if c.NotEquals != "" && testValue == compareNotEquals {
		return false
	}

	// Check matches regex
	if c.matchesRegex != nil && !c.matchesRegex.MatchString(value) {
		return false
	}

	// Check not_matches regex
	if c.notMatchesRegex != nil && c.notMatchesRegex.MatchString(value) {
		return false
	}

	return true
}

// ApplyDerivedFields processes all derived fields for a record
func ApplyDerivedFields(derivedFields []DerivedField, rec map[string]interface{}) error {
	for i := range derivedFields {
		df := &derivedFields[i]

		// Get source value
		sourceVal, ok := rec[df.Source]
		if !ok {
			continue // Source field not present
		}
		sourceStr, ok := sourceVal.(string)
		if !ok {
			continue // Source not a string
		}

		// Extract symbol values
		extracted, err := df.Extract(sourceStr)
		if err != nil {
			return fmt.Errorf("extracting from field %q: %w", df.Source, err)
		}

		// Apply outputs
		for _, out := range df.Outputs {
			value, exists := extracted[out.Symbol]
			if !exists {
				continue
			}

			// Check condition
			if !out.Condition.Evaluate(value) {
				continue
			}

			// Determine final value
			finalValue := value
			if out.Value != "" {
				finalValue = out.Value // Override with specified value
			}

			// Set target field
			if finalValue != "" {
				rec[out.Target] = finalValue
			}
		}
	}

	return nil
}

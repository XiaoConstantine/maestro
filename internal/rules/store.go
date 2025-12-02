package rules

import (
	"fmt"

	"github.com/XiaoConstantine/maestro/internal/types"
)

// RuleStore manages our taxonomy of review rules.
type RuleStore struct {
	rules map[string]types.ReviewRule
	// Indexes for efficient lookup
	dimensionIndex map[string][]string // Dimension -> Rule IDs
	categoryIndex  map[string][]string // Category -> Rule IDs
}

// NewRuleStore creates a new rule store.
func NewRuleStore() *RuleStore {
	return &RuleStore{
		rules:          make(map[string]types.ReviewRule),
		dimensionIndex: make(map[string][]string),
		categoryIndex:  make(map[string][]string),
	}
}

// AddRule adds a rule to the store.
func (rs *RuleStore) AddRule(rule types.ReviewRule) error {
	// Validate rule structure
	if err := rs.validateRule(rule); err != nil {
		return fmt.Errorf("invalid rule %s: %w", rule.ID, err)
	}

	// Add to main store and indexes
	rs.rules[rule.ID] = rule
	rs.dimensionIndex[rule.Dimension] = append(
		rs.dimensionIndex[rule.Dimension],
		rule.ID,
	)
	rs.categoryIndex[rule.Category] = append(
		rs.categoryIndex[rule.Category],
		rule.ID,
	)

	return nil
}

// validateRule ensures a review rule meets our requirements before being added
// to the store.
func (rs *RuleStore) validateRule(rule types.ReviewRule) error {
	// Check required fields
	if rule.ID == "" {
		return fmt.Errorf("rule ID is required")
	}
	if rule.Dimension == "" {
		return fmt.Errorf("dimension is required")
	}
	if rule.Category == "" {
		return fmt.Errorf("category is required")
	}
	if rule.Description == "" {
		return fmt.Errorf("description is required")
	}

	// Validate dimension is one of our known dimensions
	validDimensions := map[string]bool{
		"Code Defect":                     true,
		"Security Vulnerability":          true,
		"Maintainability and Readability": true,
		"Performance Issue":               true,
		"Other":                           true,
	}
	if !validDimensions[rule.Dimension] {
		return fmt.Errorf("invalid dimension: %s", rule.Dimension)
	}

	// Ensure we don't have duplicate IDs
	if _, exists := rs.rules[rule.ID]; exists {
		return fmt.Errorf("duplicate rule ID: %s", rule.ID)
	}

	return nil
}

// FormatRuleContent creates a standardized text representation of a rule for embedding.
func FormatRuleContent(rule types.ReviewRule) string {
	return fmt.Sprintf(`Rule: %s
Category: %s
Dimension: %s
Description: %s
Good Example:
%s
Bad Example:
%s
Explanation: %s`,
		rule.Name,
		rule.Category,
		rule.Dimension,
		rule.Description,
		rule.Examples.Good,
		rule.Examples.Bad,
		rule.Examples.Explanation,
	)
}

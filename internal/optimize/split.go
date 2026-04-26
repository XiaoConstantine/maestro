package optimize

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"

	dspyoptimize "github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
)

// SplitAgentExamples creates a deterministic, repo-stratified validation split.
// Each group keeps at least one training example when possible.
func SplitAgentExamples(examples []dspyoptimize.AgentExample, validationSplit float64, minExamples int) ([]dspyoptimize.AgentExample, []dspyoptimize.AgentExample, error) {
	if len(examples) == 0 {
		return nil, nil, fmt.Errorf("at least one benchmark example is required")
	}
	if len(examples) == 1 {
		return nil, nil, fmt.Errorf("at least two benchmark examples are required to create a validation split")
	}
	if minExamples > 0 && len(examples) < minExamples {
		return nil, nil, fmt.Errorf("GEPA optimization requires at least %d benchmark examples; got %d", minExamples, len(examples))
	}
	if validationSplit <= 0 || validationSplit >= 1 {
		return nil, nil, fmt.Errorf("validation split must be between 0 and 1")
	}
	validationCount := int(math.Ceil(float64(len(examples)) * validationSplit))
	if validationCount <= 0 {
		validationCount = 1
	}
	if validationCount >= len(examples) {
		validationCount = len(examples) - 1
	}
	return stratifiedAgentExampleSplit(examples, validationCount)
}

func ValidateUnitThreshold(name string, value float64) error {
	if value <= 0 || value > 1 {
		return fmt.Errorf("%s must be in the range (0, 1]", name)
	}
	return nil
}

func ValidateReplayBaselineOnly(replayOnly, baselineOnly bool) error {
	if replayOnly && baselineOnly {
		return fmt.Errorf("--replay-only and --baseline-only are mutually exclusive")
	}
	return nil
}

func ValidateBaselineOnlyWritePath(baselineOnly bool, writeBaselinePath string) error {
	if baselineOnly && strings.TrimSpace(writeBaselinePath) == "" {
		return fmt.Errorf("--baseline-only requires --write-baseline")
	}
	return nil
}

func ValidateProtectedGateRequirement(replayOnly, baselineOnly, hasBaseline, skipProtectedGate bool, writeBaselinePath string) error {
	if replayOnly || baselineOnly || hasBaseline || skipProtectedGate || strings.TrimSpace(writeBaselinePath) != "" {
		return nil
	}
	return fmt.Errorf("--baseline is required for GEPA artifact acceptance; pass --skip-protected-gate only for local experiments")
}

type indexedAgentExample struct {
	index   int
	example dspyoptimize.AgentExample
}

type agentExampleSplitGroup struct {
	key             string
	examples        []indexedAgentExample
	validationCount int
	capacity        int
	remainder       float64
}

func stratifiedAgentExampleSplit(examples []dspyoptimize.AgentExample, validationCount int) ([]dspyoptimize.AgentExample, []dspyoptimize.AgentExample, error) {
	groups := splitAgentExampleGroups(examples, validationCount)
	capacity := 0
	for i := range groups {
		capacity += groups[i].capacity
	}
	if capacity == 0 {
		return nil, nil, fmt.Errorf("validation split requires at least one trainable example outside validation")
	}
	if validationCount > capacity {
		validationCount = capacity
	}

	allocated := 0
	for i := range groups {
		exact := float64(len(groups[i].examples)) * float64(validationCount) / float64(len(examples))
		count := int(math.Floor(exact))
		if count > groups[i].capacity {
			count = groups[i].capacity
		}
		groups[i].validationCount = count
		groups[i].remainder = exact - float64(count)
		allocated += count
	}
	for allocated < validationCount {
		sort.SliceStable(groups, func(i, j int) bool {
			if groups[i].validationCount >= groups[i].capacity {
				return false
			}
			if groups[j].validationCount >= groups[j].capacity {
				return true
			}
			if groups[i].remainder == groups[j].remainder {
				return groups[i].key < groups[j].key
			}
			return groups[i].remainder > groups[j].remainder
		})
		advanced := false
		for i := range groups {
			if groups[i].validationCount >= groups[i].capacity {
				continue
			}
			groups[i].validationCount++
			allocated++
			advanced = true
			break
		}
		if !advanced {
			break
		}
	}

	validationIndices := make(map[int]struct{}, validationCount)
	for _, group := range groups {
		ordered := append([]indexedAgentExample(nil), group.examples...)
		sort.SliceStable(ordered, func(i, j int) bool {
			left := stableExampleHash(group.key, ordered[i].example.ID)
			right := stableExampleHash(group.key, ordered[j].example.ID)
			if left == right {
				return ordered[i].example.ID < ordered[j].example.ID
			}
			return left < right
		})
		for i := 0; i < group.validationCount && i < len(ordered); i++ {
			validationIndices[ordered[i].index] = struct{}{}
		}
	}

	training := make([]dspyoptimize.AgentExample, 0, len(examples)-len(validationIndices))
	validation := make([]dspyoptimize.AgentExample, 0, len(validationIndices))
	for i, example := range examples {
		if _, ok := validationIndices[i]; ok {
			validation = append(validation, example)
			continue
		}
		training = append(training, example)
	}
	if len(training) == 0 || len(validation) == 0 {
		return nil, nil, fmt.Errorf("validation split produced training=%d validation=%d", len(training), len(validation))
	}
	return training, validation, nil
}

func splitAgentExampleGroups(examples []dspyoptimize.AgentExample, validationCount int) []agentExampleSplitGroup {
	groupIndex := make(map[string]int)
	groups := make([]agentExampleSplitGroup, 0)
	for i, example := range examples {
		key := splitAgentExampleGroupKey(example)
		idx, ok := groupIndex[key]
		if !ok {
			idx = len(groups)
			groupIndex[key] = idx
			groups = append(groups, agentExampleSplitGroup{key: key})
		}
		groups[idx].examples = append(groups[idx].examples, indexedAgentExample{index: i, example: example})
	}
	for i := range groups {
		groups[i].capacity = len(groups[i].examples) - 1
		if groups[i].capacity < 0 {
			groups[i].capacity = 0
		}
		if groups[i].capacity > validationCount {
			groups[i].capacity = validationCount
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].key < groups[j].key })
	return groups
}

func splitAgentExampleGroupKey(example dspyoptimize.AgentExample) string {
	owner := stringFromInterface(example.Inputs["owner"])
	repo := stringFromInterface(example.Inputs["repo"])
	if repo != "" {
		if owner != "" {
			return owner + "/" + repo
		}
		return repo
	}
	if metadataOwner, metadataRepo := ownerRepoFromOverviewMetadata(example.Metadata["rlm_overview_case"]); metadataRepo != "" {
		if metadataOwner != "" {
			return metadataOwner + "/" + metadataRepo
		}
		return metadataRepo
	}
	return "all"
}

func ownerRepoFromOverviewMetadata(raw interface{}) (string, string) {
	if raw == nil {
		return "", ""
	}
	if fields, ok := raw.(map[string]interface{}); ok {
		return stringFromInterface(fields["owner"]), stringFromInterface(fields["repo"])
	}
	if fields, ok := raw.(map[string]string); ok {
		return strings.TrimSpace(fields["owner"]), strings.TrimSpace(fields["repo"])
	}

	var decoded struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return "", ""
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", ""
	}
	return strings.TrimSpace(decoded.Owner), strings.TrimSpace(decoded.Repo)
}

func stableExampleHash(parts ...string) uint64 {
	h := fnv.New64a()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

func stringFromInterface(value interface{}) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

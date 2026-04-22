package review

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
)

type BenchmarkSuite struct {
	Path               string
	TrainingExamples   []optimize.AgentExample
	ValidationExamples []optimize.AgentExample
}

func LoadBenchmarkSuites(paths []string, validationSplit float64, maxCasesPerRun int) ([]BenchmarkSuite, []optimize.AgentExample, []optimize.AgentExample, error) {
	seen := make(map[string]struct{}, len(paths))
	suites := make([]BenchmarkSuite, 0, len(paths))
	training := make([]optimize.AgentExample, 0)
	validation := make([]optimize.AgentExample, 0)

	for _, path := range paths {
		resolvedPath, err := expandReviewPath(path)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("resolve suite path %q: %w", path, err)
		}
		if resolvedPath == "" {
			return nil, nil, nil, fmt.Errorf("resolve suite path %q: path is required", path)
		}
		if _, ok := seen[resolvedPath]; ok {
			continue
		}
		seen[resolvedPath] = struct{}{}

		cases, err := LoadReviewBenchmarkSuite(resolvedPath)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("load benchmark suite %q: %w", resolvedPath, err)
		}
		if maxCasesPerRun > 0 && len(cases) > maxCasesPerRun {
			cases = append([]ReviewBenchmarkCase(nil), cases[:maxCasesPerRun]...)
		}

		examples := ReviewBenchmarkExamples(cases)
		suiteTraining, suiteValidation, err := SplitBenchmarkExamples(examples, validationSplit)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("split benchmark suite %q: %w", resolvedPath, err)
		}

		suites = append(suites, BenchmarkSuite{
			Path:               resolvedPath,
			TrainingExamples:   suiteTraining,
			ValidationExamples: suiteValidation,
		})
		training = append(training, suiteTraining...)
		validation = append(validation, suiteValidation...)
	}

	if len(suites) == 0 {
		return nil, nil, nil, fmt.Errorf("at least one benchmark suite is required")
	}

	return suites, training, validation, nil
}

func SplitBenchmarkExamples(examples []optimize.AgentExample, validationSplit float64) ([]optimize.AgentExample, []optimize.AgentExample, error) {
	if len(examples) == 0 {
		return nil, nil, fmt.Errorf("at least one benchmark example is required")
	}
	if len(examples) == 1 {
		return nil, nil, fmt.Errorf("at least two benchmark examples are required to create a validation split")
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

	validationSelections := selectBenchmarkValidationExamples(examples, validationCount, validationSplit)
	training := make([]optimize.AgentExample, 0, len(examples)-len(validationSelections))
	validation := make([]optimize.AgentExample, 0, len(validationSelections))
	for _, example := range examples {
		if validationSelections[example.ID] > 0 {
			validation = append(validation, example)
			validationSelections[example.ID]--
			continue
		}
		training = append(training, example)
	}

	return training, validation, nil
}

func BenchmarkExampleLabel(example optimize.AgentExample) string {
	if label, ok := example.Outputs["label"].(string); ok && strings.TrimSpace(label) != "" {
		return label
	}
	return "_"
}

func benchmarkExampleRank(example optimize.AgentExample) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.TrimSpace(example.ID)))
	return h.Sum64()
}

func labelsEligibleForValidation(labelBuckets map[string][]optimize.AgentExample, labels []string) []string {
	eligible := make([]string, 0, len(labels))
	for _, label := range labels {
		if len(labelBuckets[label]) > 1 {
			eligible = append(eligible, label)
		}
	}
	return eligible
}

func selectBenchmarkValidationExamples(examples []optimize.AgentExample, validationCount int, validationSplit float64) map[string]int {
	labelBuckets := make(map[string][]optimize.AgentExample)
	labels := make([]string, 0, 2)
	for _, example := range examples {
		label := BenchmarkExampleLabel(example)
		if _, ok := labelBuckets[label]; !ok {
			labels = append(labels, label)
		}
		labelBuckets[label] = append(labelBuckets[label], example)
	}
	for _, label := range labels {
		sort.SliceStable(labelBuckets[label], func(i, j int) bool {
			return benchmarkExampleRank(labelBuckets[label][i]) < benchmarkExampleRank(labelBuckets[label][j])
		})
	}

	allocations := make(map[string]int, len(labels))
	if labelsWithRoom := labelsEligibleForValidation(labelBuckets, labels); len(labelsWithRoom) > 1 && validationCount >= len(labelsWithRoom) {
		for _, label := range labelsWithRoom {
			allocations[label] = 1
		}
	}

	type bucketRemainder struct {
		label string
		frac  float64
		size  int
	}
	remainders := make([]bucketRemainder, 0, len(labels))
	allocated := 0
	for _, label := range labels {
		bucketSize := len(labelBuckets[label])
		quota := int(math.Floor(float64(bucketSize) * validationSplit))
		if quota < allocations[label] {
			quota = allocations[label]
		}
		maxQuota := maxBenchmarkInt(0, bucketSize-1)
		if quota > maxQuota {
			quota = maxQuota
		}
		allocations[label] = quota
		allocated += quota
		remainders = append(remainders, bucketRemainder{
			label: label,
			frac:  (float64(bucketSize) * validationSplit) - float64(quota),
			size:  bucketSize,
		})
	}

	remaining := validationCount - allocated
	if remaining > 0 {
		sort.SliceStable(remainders, func(i, j int) bool {
			if remainders[i].frac == remainders[j].frac {
				if remainders[i].size == remainders[j].size {
					return remainders[i].label < remainders[j].label
				}
				return remainders[i].size > remainders[j].size
			}
			return remainders[i].frac > remainders[j].frac
		})
		for _, bucket := range remainders {
			if remaining == 0 {
				break
			}
			maxQuota := maxBenchmarkInt(0, len(labelBuckets[bucket.label])-1)
			if maxQuota == 0 || allocations[bucket.label] >= maxQuota {
				continue
			}
			allocations[bucket.label]++
			remaining--
		}
	}

	for remaining > 0 {
		progress := false
		for _, label := range labels {
			if remaining == 0 {
				break
			}
			maxQuota := maxBenchmarkInt(0, len(labelBuckets[label])-1)
			if maxQuota == 0 || allocations[label] >= maxQuota {
				continue
			}
			allocations[label]++
			remaining--
			progress = true
		}
		if !progress {
			break
		}
	}

	selected := make(map[string]int, validationCount)
	for _, label := range labels {
		for _, example := range labelBuckets[label][:allocations[label]] {
			selected[example.ID]++
		}
	}
	return selected
}

func maxBenchmarkInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

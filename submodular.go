package main

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// SubmodularOptimizer implements submodular optimization for text selection and passage reranking.
type SubmodularOptimizer struct {
	log *logging.Logger
}

// NewSubmodularOptimizer creates a new submodular optimizer instance.
func NewSubmodularOptimizer(logger *logging.Logger) *SubmodularOptimizer {
	return &SubmodularOptimizer{
		log: logger,
	}
}

// Element represents a selectable item (guideline, chunk, or passage).
type Element struct {
	ID        string
	Content   *Content
	Relevance float64   // Query relevance score (r_qi)
	Embedding []float32 // Element's embedding vector
	Metadata  map[string]string
}

// SubmodularFunction defines the interface for submodular functions.
type SubmodularFunction interface {
	Evaluate(ctx context.Context, selected []int, elements []Element) float64
	MarginalGain(ctx context.Context, selected []int, candidate int, elements []Element) float64
}

// f_FL(S) = ∑_{q=1}^Q ∑_{i=1}^P max_{j∈S} r_{qj} * s_{ij}.
type FacilityLocationFunction struct {
	similarityMatrix [][]float64 // Precomputed similarity matrix s_ij
	numQueries       int         // Number of queries (typically 1 for single-query scenarios)
}

// NewFacilityLocationFunction creates a facility location function with precomputed similarities.
func NewFacilityLocationFunction(elements []Element, numQueries int) *FacilityLocationFunction {
	n := len(elements)
	similarityMatrix := make([][]float64, n)
	for i := range similarityMatrix {
		similarityMatrix[i] = make([]float64, n)
		for j := range similarityMatrix[i] {
			if i == j {
				similarityMatrix[i][j] = 1.0 // Perfect self-similarity
			} else {
				similarityMatrix[i][j] = cosineSimilarity(elements[i].Embedding, elements[j].Embedding)
			}
		}
	}

	return &FacilityLocationFunction{
		similarityMatrix: similarityMatrix,
		numQueries:       numQueries,
	}
}

// Evaluate computes the facility location function value for a given selection.
func (f *FacilityLocationFunction) Evaluate(ctx context.Context, selected []int, elements []Element) float64 {
	if len(selected) == 0 {
		return 0.0
	}

	totalScore := 0.0
	n := len(elements)

	// For each element i, find the best coverage from selected elements
	for i := 0; i < n; i++ {
		maxCoverage := 0.0
		for _, j := range selected {
			// r_qj * s_ij (query relevance * element similarity)
			coverage := elements[j].Relevance * f.similarityMatrix[i][j]
			if coverage > maxCoverage {
				maxCoverage = coverage
			}
		}
		totalScore += maxCoverage
	}

	return totalScore * float64(f.numQueries)
}

// MarginalGain computes the marginal gain of adding a candidate to the selection.
func (f *FacilityLocationFunction) MarginalGain(ctx context.Context, selected []int, candidate int, elements []Element) float64 {
	// Calculate current score
	currentScore := f.Evaluate(ctx, selected, elements)

	// Calculate score with candidate added
	newSelected := append(selected, candidate)
	newScore := f.Evaluate(ctx, newSelected, elements)

	return newScore - currentScore
}

// f_SC(S) = ∑_{q=1}^Q ∑_{i=1}^P min(r_{qi}, max_{j∈S} s_{ij}).
type SaturatedCoverageFunction struct {
	similarityMatrix [][]float64 // Precomputed similarity matrix s_ij
	numQueries       int         // Number of queries
}

// NewSaturatedCoverageFunction creates a saturated coverage function.
func NewSaturatedCoverageFunction(elements []Element, numQueries int) *SaturatedCoverageFunction {
	n := len(elements)
	similarityMatrix := make([][]float64, n)
	for i := range similarityMatrix {
		similarityMatrix[i] = make([]float64, n)
		for j := range similarityMatrix[i] {
			if i == j {
				similarityMatrix[i][j] = 1.0 // Perfect self-similarity
			} else {
				similarityMatrix[i][j] = cosineSimilarity(elements[i].Embedding, elements[j].Embedding)
			}
		}
	}

	return &SaturatedCoverageFunction{
		similarityMatrix: similarityMatrix,
		numQueries:       numQueries,
	}
}

// Evaluate computes the saturated coverage function value for a given selection.
func (f *SaturatedCoverageFunction) Evaluate(ctx context.Context, selected []int, elements []Element) float64 {
	if len(selected) == 0 {
		return 0.0
	}

	totalScore := 0.0
	n := len(elements)

	// For each element i, find min(r_qi, max_{j∈S} s_ij)
	for i := 0; i < n; i++ {
		maxSimilarity := 0.0
		for _, j := range selected {
			if f.similarityMatrix[i][j] > maxSimilarity {
				maxSimilarity = f.similarityMatrix[i][j]
			}
		}
		// min(relevance, best_coverage)
		score := math.Min(elements[i].Relevance, maxSimilarity)
		totalScore += score
	}

	return totalScore * float64(f.numQueries)
}

// MarginalGain computes the marginal gain of adding a candidate to the selection.
func (f *SaturatedCoverageFunction) MarginalGain(ctx context.Context, selected []int, candidate int, elements []Element) float64 {
	// Calculate current score
	currentScore := f.Evaluate(ctx, selected, elements)

	// Calculate score with candidate added
	newSelected := append(selected, candidate)
	newScore := f.Evaluate(ctx, newSelected, elements)

	return newScore - currentScore
}

// PriorityQueueItem represents an element in the lazy greedy priority queue.
type PriorityQueueItem struct {
	ElementIndex int     // Index of the element
	MarginalGain float64 // Cached marginal gain
	Iteration    int     // Iteration when this gain was computed
}

// LazyGreedyOptimizer implements the lazy greedy algorithm with (1-1/e) approximation guarantee.
type LazyGreedyOptimizer struct {
	function SubmodularFunction
	log      *logging.Logger
}

// NewLazyGreedyOptimizer creates a new lazy greedy optimizer.
func NewLazyGreedyOptimizer(function SubmodularFunction, logger *logging.Logger) *LazyGreedyOptimizer {
	return &LazyGreedyOptimizer{
		function: function,
		log:      logger,
	}
}

// Optimize performs lazy greedy optimization to select k elements.
func (opt *LazyGreedyOptimizer) Optimize(ctx context.Context, elements []Element, k int) ([]int, []float64, error) {
	if k <= 0 || k > len(elements) {
		return nil, nil, fmt.Errorf("invalid k: %d, must be between 1 and %d", k, len(elements))
	}

	opt.log.Debug(ctx, "Starting lazy greedy optimization for %d elements, selecting %d", len(elements), k)

	var selected []int
	functionValues := make([]float64, 0, k)

	// Initialize priority queue with all elements
	queue := make([]PriorityQueueItem, len(elements))
	for i := 0; i < len(elements); i++ {
		queue[i] = PriorityQueueItem{
			ElementIndex: i,
			MarginalGain: opt.function.MarginalGain(ctx, selected, i, elements),
			Iteration:    0,
		}
	}

	// Sort queue by marginal gain (descending)
	sort.Slice(queue, func(i, j int) bool {
		return queue[i].MarginalGain > queue[j].MarginalGain
	})

	for iteration := 0; iteration < k; iteration++ {
		opt.log.Debug(ctx, "Lazy greedy iteration %d/%d", iteration+1, k)

		var bestCandidate int
		var bestGain float64

		// Lazy evaluation: keep checking top of queue until we find the true best
		for len(queue) > 0 {
			// Get the top candidate
			top := queue[0]

			// If this gain was computed in the current iteration, it's valid
			if top.Iteration == iteration {
				bestCandidate = top.ElementIndex
				bestGain = top.MarginalGain
				// Remove from queue
				queue = queue[1:]
				break
			}

			// Otherwise, recompute the gain and reinsert
			newGain := opt.function.MarginalGain(ctx, selected, top.ElementIndex, elements)
			queue[0] = PriorityQueueItem{
				ElementIndex: top.ElementIndex,
				MarginalGain: newGain,
				Iteration:    iteration,
			}

			// Re-sort to maintain priority order
			sort.Slice(queue, func(i, j int) bool {
				return queue[i].MarginalGain > queue[j].MarginalGain
			})
		}

		// Add the best candidate to our selection
		selected = append(selected, bestCandidate)
		currentValue := opt.function.Evaluate(ctx, selected, elements)
		functionValues = append(functionValues, currentValue)

		opt.log.Debug(ctx, "Selected element %d with gain %.4f, total function value: %.4f",
			bestCandidate, bestGain, currentValue)

		// Check for saturation (diminishing returns)
		if iteration > 0 && bestGain < 0.001 {
			opt.log.Debug(ctx, "Early stopping due to saturation (gain < 0.001)")
			break
		}
	}

	opt.log.Debug(ctx, "Lazy greedy optimization completed, selected %d elements", len(selected))
	return selected, functionValues, nil
}

// OptimizeWithSaturationDetection performs optimization with automatic stopping when gains become negligible.
func (opt *LazyGreedyOptimizer) OptimizeWithSaturationDetection(ctx context.Context, elements []Element, maxK int, saturationThreshold float64) ([]int, []float64, error) {
	selected, functionValues, err := opt.Optimize(ctx, elements, maxK)
	if err != nil {
		return nil, nil, err
	}

	// Find the saturation point
	saturationPoint := len(selected)
	for i := 1; i < len(functionValues); i++ {
		marginalGain := functionValues[i] - functionValues[i-1]
		if marginalGain < saturationThreshold {
			saturationPoint = i
			opt.log.Debug(ctx, "Saturation detected at k=%d with marginal gain %.4f", i, marginalGain)
			break
		}
	}

	// Return only up to the saturation point
	return selected[:saturationPoint], functionValues[:saturationPoint], nil
}

// cosineSimilarity computes cosine similarity between two embedding vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0.0
	}

	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}

	if normA == 0 || normB == 0 {
		return 0.0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// SelectGuidelinesSubmodular uses submodular optimization to select diverse and relevant guidelines.
func (s *SubmodularOptimizer) SelectGuidelinesSubmodular(ctx context.Context, query []float32, guidelines []*Content, k int, useFL bool) ([]*Content, error) {
	if len(guidelines) == 0 {
		return nil, fmt.Errorf("no guidelines provided")
	}

	s.log.Debug(ctx, "Starting submodular guideline selection: %d candidates, selecting %d", len(guidelines), k)

	// Convert guidelines to elements with relevance scores
	elements := make([]Element, len(guidelines))
	for i, guideline := range guidelines {
		// Calculate relevance as cosine similarity to query
		relevance := cosineSimilarity(query, guideline.Embedding)
		elements[i] = Element{
			ID:        guideline.ID,
			Content:   guideline,
			Relevance: relevance,
			Embedding: guideline.Embedding,
			Metadata:  guideline.Metadata,
		}
	}

	// Choose submodular function
	var function SubmodularFunction
	if useFL {
		function = NewFacilityLocationFunction(elements, 1) // Single query
		s.log.Debug(ctx, "Using Facility Location formulation")
	} else {
		function = NewSaturatedCoverageFunction(elements, 1) // Single query
		s.log.Debug(ctx, "Using Saturated Coverage formulation")
	}

	// Run lazy greedy optimization
	optimizer := NewLazyGreedyOptimizer(function, s.log)
	selectedIndices, functionValues, err := optimizer.OptimizeWithSaturationDetection(ctx, elements, k, 0.001)
	if err != nil {
		return nil, fmt.Errorf("optimization failed: %w", err)
	}

	// Convert back to Content objects
	selectedGuidelines := make([]*Content, len(selectedIndices))
	for i, idx := range selectedIndices {
		selectedGuidelines[i] = elements[idx].Content
	}

	s.log.Debug(ctx, "Submodular selection completed: selected %d guidelines with final function value %.4f",
		len(selectedGuidelines), functionValues[len(functionValues)-1])

	return selectedGuidelines, nil
}

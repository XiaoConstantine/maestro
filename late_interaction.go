package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// LateInteractionProcessor implements late interaction patterns for refined guideline selection.
type LateInteractionProcessor struct {
	log              *logging.Logger
	qwenModel        string
	refinementStages int
}

// NewLateInteractionProcessor creates a new late interaction processor with Qwen3 0.6B.
func NewLateInteractionProcessor(logger *logging.Logger) *LateInteractionProcessor {
	return &LateInteractionProcessor{
		log:              logger,
		qwenModel:        "qwen3:0.6b", // Qwen3 0.6B model
		refinementStages: 2,            // Two-stage refinement
	}
}

// RefinementStage represents a stage in the late interaction process.
type RefinementStage struct {
	Name        string
	Description string
	Prompt      string
	Model       string
}

// GuidelineRefinementRequest contains the request for guideline refinement.
type GuidelineRefinementRequest struct {
	CodeContext       string          // The code being reviewed
	InitialGuidelines []*Content      // Initially selected guidelines
	QueryContext      string          // Original query/review context
	Stage             RefinementStage // Current refinement stage
}

// GuidelineRefinementResponse contains the refined guideline selection.
type GuidelineRefinementResponse struct {
	RefinedGuidelines []*Content `json:"refined_guidelines"`
	RelevanceScores   []float64  `json:"relevance_scores"`
	Reasoning         string     `json:"reasoning"`
	Confidence        float64    `json:"confidence"`
	Stage             string     `json:"stage"`
}

// RefinementResult contains the complete refinement process result.
type RefinementResult struct {
	OriginalCount   int                           `json:"original_count"`
	FinalCount      int                           `json:"final_count"`
	ProcessingTime  time.Duration                 `json:"processing_time"`
	StageResults    []GuidelineRefinementResponse `json:"stage_results"`
	FinalGuidelines []*Content                    `json:"final_guidelines"`
	QualityMetrics  RefinementQualityMetrics      `json:"quality_metrics"`
}

// RefinementQualityMetrics tracks the quality of the refinement process.
type RefinementQualityMetrics struct {
	DiversityScore   float64 `json:"diversity_score"`   // How diverse are the selected guidelines
	RelevanceScore   float64 `json:"relevance_score"`   // How relevant to the code context
	CoverageScore    float64 `json:"coverage_score"`    // How well do they cover different aspects
	ConfidenceScore  float64 `json:"confidence_score"`  // Qwen's confidence in the selection
	ImprovementRatio float64 `json:"improvement_ratio"` // How much better than initial selection
}

// ProcessLateInteraction performs multi-stage guideline refinement using Qwen3 0.6B.
func (lip *LateInteractionProcessor) ProcessLateInteraction(ctx context.Context,
	codeContext string,
	initialGuidelines []*Content,
	queryContext string) (*RefinementResult, error) {

	startTime := time.Now()
	lip.log.Debug(ctx, "Starting late interaction processing with %d initial guidelines", len(initialGuidelines))

	if len(initialGuidelines) == 0 {
		return nil, fmt.Errorf("no initial guidelines provided")
	}

	// Define refinement stages
	stages := []RefinementStage{
		{
			Name:        "relevance_filtering",
			Description: "Filter guidelines based on direct relevance to code patterns",
			Model:       lip.qwenModel,
			Prompt: `You are an expert code reviewer analyzing Go code. Your task is to select the most relevant coding guidelines for the given code context.

INSTRUCTIONS:
1. Analyze the provided code context carefully
2. Review each guideline and assess its relevance to the specific code patterns, issues, or improvements needed
3. Select guidelines that are directly applicable and would provide actionable feedback
4. Prioritize guidelines that address actual issues or potential improvements in the code
5. Remove guidelines that are not relevant to this specific code context

Return your response as valid JSON with this exact structure:
{
  "refined_guidelines": [list of guideline IDs that are most relevant],
  "relevance_scores": [numerical relevance scores 0.0-1.0 for each selected guideline],
  "reasoning": "Brief explanation of why these guidelines were selected",
  "confidence": numerical_confidence_score_0.0_to_1.0
}

CODE CONTEXT:
%s

QUERY CONTEXT:
%s

AVAILABLE GUIDELINES:
%s`,
		},
		{
			Name:        "diversity_optimization",
			Description: "Optimize for diversity while maintaining relevance",
			Model:       lip.qwenModel,
			Prompt: `You are optimizing guideline selection for maximum coverage and diversity. Your goal is to select guidelines that cover different aspects of code quality while avoiding redundancy.

INSTRUCTIONS:
1. Review the filtered guidelines from the previous stage
2. Ensure the selection covers diverse aspects: security, performance, maintainability, style, error handling
3. Remove redundant guidelines that cover the same issues
4. Maintain the most impactful guidelines from each category
5. Aim for comprehensive coverage with minimal overlap

Return your response as valid JSON with this exact structure:
{
  "refined_guidelines": [list of guideline IDs for optimal diversity],
  "relevance_scores": [numerical relevance scores 0.0-1.0 for each selected guideline],
  "reasoning": "Brief explanation of diversity optimization decisions",
  "confidence": numerical_confidence_score_0.0_to_1.0
}

CODE CONTEXT:
%s

PREVIOUS STAGE GUIDELINES:
%s`,
		},
	}

	result := &RefinementResult{
		OriginalCount: len(initialGuidelines),
		StageResults:  make([]GuidelineRefinementResponse, 0, len(stages)),
	}

	currentGuidelines := initialGuidelines

	// Process each refinement stage
	for i, stage := range stages {
		lip.log.Debug(ctx, "Processing refinement stage %d: %s", i+1, stage.Name)

		stageResult, err := lip.processRefinementStage(ctx, codeContext, currentGuidelines, queryContext, stage)
		if err != nil {
			lip.log.Warn(ctx, "Stage %s failed: %v, using previous guidelines", stage.Name, err)
			// Continue with current guidelines if stage fails
			continue
		}

		result.StageResults = append(result.StageResults, *stageResult)
		currentGuidelines = stageResult.RefinedGuidelines

		lip.log.Debug(ctx, "Stage %s completed: %d guidelines selected", stage.Name, len(currentGuidelines))
	}

	// Calculate final metrics
	result.FinalCount = len(currentGuidelines)
	result.FinalGuidelines = currentGuidelines
	result.ProcessingTime = time.Since(startTime)
	result.QualityMetrics = lip.calculateQualityMetrics(ctx, initialGuidelines, currentGuidelines, result.StageResults)

	lip.log.Debug(ctx, "Late interaction processing completed: %d → %d guidelines in %v",
		result.OriginalCount, result.FinalCount, result.ProcessingTime)

	return result, nil
}

// processRefinementStage handles a single refinement stage.
func (lip *LateInteractionProcessor) processRefinementStage(ctx context.Context,
	codeContext string,
	guidelines []*Content,
	queryContext string,
	stage RefinementStage) (*GuidelineRefinementResponse, error) {

	// For now, use heuristic-based refinement instead of LLM calls
	// TODO: Implement actual LLM integration when API is available
	lip.log.Debug(ctx, "Using heuristic-based refinement for stage: %s", stage.Name)

	// Use fallback parsing which implements rule-based refinement
	return lip.fallbackParseResponse(ctx, "", guidelines, stage.Name)
}



// fallbackParseResponse provides fallback parsing when JSON parsing fails.
func (lip *LateInteractionProcessor) fallbackParseResponse(ctx context.Context, response string, guidelines []*Content, stageName string) (*GuidelineRefinementResponse, error) {
	lip.log.Debug(ctx, "Using fallback parsing for stage %s", stageName)

	// Simple fallback: return top 3-5 guidelines based on heuristics
	maxGuidelines := min(5, len(guidelines))

	// Sort by category priority for fallback
	sorted := make([]*Content, len(guidelines))
	copy(sorted, guidelines)

	sort.Slice(sorted, func(i, j int) bool {
		// Prioritize by category importance
		priorityMap := map[string]int{
			"security":        5,
			"error handling":  4,
			"performance":     3,
			"maintainability": 2,
			"style":           1,
		}

		catI := strings.ToLower(sorted[i].Metadata["category"])
		catJ := strings.ToLower(sorted[j].Metadata["category"])

		return priorityMap[catI] > priorityMap[catJ]
	})

	selected := sorted[:maxGuidelines]
	scores := make([]float64, len(selected))
	for i := range scores {
		scores[i] = 0.7 // Default fallback score
	}

	return &GuidelineRefinementResponse{
		RefinedGuidelines: selected,
		RelevanceScores:   scores,
		Reasoning:         fmt.Sprintf("Fallback selection for stage %s: selected top %d guidelines by category priority", stageName, maxGuidelines),
		Confidence:        0.6, // Lower confidence for fallback
		Stage:             stageName,
	}, nil
}

// calculateQualityMetrics computes quality metrics for the refinement process.
func (lip *LateInteractionProcessor) calculateQualityMetrics(ctx context.Context,
	initial []*Content,
	final []*Content,
	stageResults []GuidelineRefinementResponse) RefinementQualityMetrics {

	metrics := RefinementQualityMetrics{}

	if len(final) == 0 {
		return metrics
	}

	// Calculate diversity score based on category distribution
	categories := make(map[string]int)
	for _, guideline := range final {
		category := guideline.Metadata["category"]
		categories[category]++
	}

	// Higher diversity = more categories represented
	metrics.DiversityScore = float64(len(categories)) / float64(len(final))

	// Calculate average relevance score from stages
	totalRelevance := 0.0
	totalCount := 0
	for _, stage := range stageResults {
		for _, score := range stage.RelevanceScores {
			totalRelevance += score
			totalCount++
		}
	}
	if totalCount > 0 {
		metrics.RelevanceScore = totalRelevance / float64(totalCount)
	}

	// Calculate coverage score based on dimension distribution
	dimensions := make(map[string]int)
	for _, guideline := range final {
		dimension := guideline.Metadata["dimension"]
		dimensions[dimension]++
	}
	metrics.CoverageScore = float64(len(dimensions)) / 4.0 // Assuming 4 main dimensions

	// Calculate average confidence from stages
	totalConfidence := 0.0
	for _, stage := range stageResults {
		totalConfidence += stage.Confidence
	}
	if len(stageResults) > 0 {
		metrics.ConfidenceScore = totalConfidence / float64(len(stageResults))
	}

	// Calculate improvement ratio (reduction in guidelines while maintaining quality)
	if len(initial) > 0 {
		reductionRatio := 1.0 - (float64(len(final)) / float64(len(initial)))
		qualityScore := (metrics.RelevanceScore + metrics.DiversityScore + metrics.CoverageScore) / 3.0
		metrics.ImprovementRatio = reductionRatio * qualityScore
	}

	lip.log.Debug(ctx, "Quality metrics calculated: diversity=%.3f, relevance=%.3f, coverage=%.3f, confidence=%.3f",
		metrics.DiversityScore, metrics.RelevanceScore, metrics.CoverageScore, metrics.ConfidenceScore)

	return metrics
}


// ProcessGuidelinesWithLateInteraction combines submodular optimization with late interaction.
func (s *SubmodularOptimizer) ProcessGuidelinesWithLateInteraction(ctx context.Context,
	query []float32,
	guidelines []*Content,
	codeContext string,
	queryContext string,
	k int) ([]*Content, *RefinementResult, error) {

	s.log.Debug(ctx, "Starting combined submodular + late interaction processing")

	// Stage 1: Submodular optimization for initial selection (select more than needed)
	initialK := minInt(k*2, len(guidelines))                                                      // Select twice as many for refinement
	initialSelection, err := s.SelectGuidelinesSubmodular(ctx, query, guidelines, initialK, true) // Use Facility Location
	if err != nil {
		return nil, nil, fmt.Errorf("submodular optimization failed: %w", err)
	}

	s.log.Debug(ctx, "Submodular optimization selected %d guidelines for refinement", len(initialSelection))

	// Stage 2: Late interaction refinement with Qwen3 0.6B
	processor := NewLateInteractionProcessor(s.log)
	refinementResult, err := processor.ProcessLateInteraction(ctx, codeContext, initialSelection, queryContext)
	if err != nil {
		s.log.Warn(ctx, "Late interaction failed, falling back to submodular selection: %v", err)
		// Fallback to just the submodular selection, truncated to k
		finalK := minInt(k, len(initialSelection))
		return initialSelection[:finalK], nil, nil
	}

	// Ensure we don't exceed the requested k
	finalGuidelines := refinementResult.FinalGuidelines
	if len(finalGuidelines) > k {
		finalGuidelines = finalGuidelines[:k]
		refinementResult.FinalGuidelines = finalGuidelines
		refinementResult.FinalCount = k
	}

	s.log.Debug(ctx, "Combined processing completed: %d final guidelines selected", len(finalGuidelines))

	return finalGuidelines, refinementResult, nil
}

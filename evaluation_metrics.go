package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// GuidelineSelectionEvaluator provides comprehensive evaluation metrics for guideline selection quality.
type GuidelineSelectionEvaluator struct {
	log *logging.Logger
}

// NewGuidelineSelectionEvaluator creates a new evaluator instance.
func NewGuidelineSelectionEvaluator(logger *logging.Logger) *GuidelineSelectionEvaluator {
	return &GuidelineSelectionEvaluator{
		log: logger,
	}
}

// SelectionQualityMetrics contains comprehensive quality metrics for guideline selection.
type SelectionQualityMetrics struct {
	// Coverage Metrics
	DiversityScore    float64 `json:"diversity_score"`    // How diverse are the selected guidelines
	CategoryCoverage  float64 `json:"category_coverage"`  // Percentage of important categories covered
	DimensionCoverage float64 `json:"dimension_coverage"` // Percentage of review dimensions covered
	SemanticCoverage  float64 `json:"semantic_coverage"`  // How well they cover semantic space

	// Relevance Metrics
	AverageRelevance  float64 `json:"average_relevance"`  // Average relevance to query
	TopKRelevance     float64 `json:"top_k_relevance"`    // Relevance of top selected items
	RelevanceVariance float64 `json:"relevance_variance"` // Variance in relevance scores

	// Quality Metrics
	ContentQuality     float64 `json:"content_quality"`     // Quality of guideline content
	ActionabilityScore float64 `json:"actionability_score"` // How actionable are the guidelines
	ExampleQuality     float64 `json:"example_quality"`     // Quality of code examples

	// Efficiency Metrics
	SelectionTime    time.Duration `json:"selection_time"`    // Time taken for selection
	OptimizationGain float64       `json:"optimization_gain"` // Improvement over baseline
	SaturationPoint  int           `json:"saturation_point"`  // Point where gains diminish

	// Comparison Metrics
	BaselineImprovement float64 `json:"baseline_improvement"`  // % improvement over random selection
	SubmodularGain      float64 `json:"submodular_gain"`       // Gain from submodular optimization
	LateInteractionGain float64 `json:"late_interaction_gain"` // Gain from late interaction

	// Metadata
	TotalCandidates int           `json:"total_candidates"` // Total guidelines available
	SelectedCount   int           `json:"selected_count"`   // Number selected
	EvaluationTime  time.Duration `json:"evaluation_time"`  // Time for evaluation
	Timestamp       time.Time     `json:"timestamp"`        // When evaluation was performed
}

// EvaluationReport contains detailed evaluation results.
type EvaluationReport struct {
	Metrics               SelectionQualityMetrics `json:"metrics"`
	CategoryBreakdown     map[string]int          `json:"category_breakdown"`
	DimensionBreakdown    map[string]int          `json:"dimension_breakdown"`
	QualityDistribution   map[string]int          `json:"quality_distribution"`
	RelevanceDistribution []float64               `json:"relevance_distribution"`
	Recommendations       []string                `json:"recommendations"`
	DetailedAnalysis      string                  `json:"detailed_analysis"`
	EvaluationTime        time.Duration           `json:"evaluation_time"`
}

// EvaluateSelection performs comprehensive evaluation of guideline selection.
func (gse *GuidelineSelectionEvaluator) EvaluateSelection(ctx context.Context,
	query []float32,
	allCandidates []*Content,
	selectedGuidelines []*Content,
	selectionTime time.Duration) (*EvaluationReport, error) {

	startTime := time.Now()
	gse.log.Debug(ctx, "Starting comprehensive evaluation of guideline selection")

	if len(selectedGuidelines) == 0 {
		return nil, fmt.Errorf("no guidelines selected for evaluation")
	}

	report := &EvaluationReport{
		CategoryBreakdown:     make(map[string]int),
		DimensionBreakdown:    make(map[string]int),
		QualityDistribution:   make(map[string]int),
		RelevanceDistribution: make([]float64, 0),
		Recommendations:       make([]string, 0),
	}

	// Calculate all metrics
	metrics := SelectionQualityMetrics{
		TotalCandidates: len(allCandidates),
		SelectedCount:   len(selectedGuidelines),
		SelectionTime:   selectionTime,
		Timestamp:       time.Now(),
	}

	// Coverage Metrics
	metrics.DiversityScore = gse.calculateDiversityScore(selectedGuidelines)
	metrics.CategoryCoverage = gse.calculateCategoryCoverage(selectedGuidelines, allCandidates)
	metrics.DimensionCoverage = gse.calculateDimensionCoverage(selectedGuidelines)
	metrics.SemanticCoverage = gse.calculateSemanticCoverage(query, selectedGuidelines)

	// Relevance Metrics
	relevanceScores := gse.calculateRelevanceScores(query, selectedGuidelines)
	metrics.AverageRelevance = gse.calculateAverageRelevance(relevanceScores)
	metrics.TopKRelevance = gse.calculateTopKRelevance(relevanceScores, 3)
	metrics.RelevanceVariance = gse.calculateVariance(relevanceScores)

	// Quality Metrics
	metrics.ContentQuality = gse.calculateContentQuality(selectedGuidelines)
	metrics.ActionabilityScore = gse.calculateActionabilityScore(selectedGuidelines)
	metrics.ExampleQuality = gse.calculateExampleQuality(selectedGuidelines)

	// Efficiency Metrics
	metrics.OptimizationGain = gse.calculateOptimizationGain(query, allCandidates, selectedGuidelines)
	metrics.SaturationPoint = gse.findSaturationPoint(query, selectedGuidelines)

	// Comparison Metrics
	metrics.BaselineImprovement = gse.calculateBaselineImprovement(query, allCandidates, selectedGuidelines)

	report.Metrics = metrics
	report.EvaluationTime = time.Since(startTime)

	// Generate breakdowns
	gse.generateCategoryBreakdown(selectedGuidelines, report.CategoryBreakdown)
	gse.generateDimensionBreakdown(selectedGuidelines, report.DimensionBreakdown)
	gse.generateQualityDistribution(selectedGuidelines, report.QualityDistribution)
	report.RelevanceDistribution = relevanceScores

	// Generate recommendations
	report.Recommendations = gse.generateRecommendations(ctx, &metrics, selectedGuidelines)
	report.DetailedAnalysis = gse.generateDetailedAnalysis(ctx, &metrics, selectedGuidelines)

	gse.log.Debug(ctx, "Evaluation completed in %v: diversity=%.3f, relevance=%.3f, quality=%.3f",
		report.EvaluationTime, metrics.DiversityScore, metrics.AverageRelevance, metrics.ContentQuality)

	return report, nil
}

// calculateDiversityScore measures how diverse the selected guidelines are.
func (gse *GuidelineSelectionEvaluator) calculateDiversityScore(guidelines []*Content) float64 {
	if len(guidelines) <= 1 {
		return 0.0
	}

	categories := make(map[string]bool)
	dimensions := make(map[string]bool)

	for _, guideline := range guidelines {
		if category := guideline.Metadata["category"]; category != "" {
			categories[category] = true
		}
		if dimension := guideline.Metadata["dimension"]; dimension != "" {
			dimensions[dimension] = true
		}
	}

	// Diversity based on category and dimension spread
	categoryDiversity := float64(len(categories)) / float64(len(guidelines))
	dimensionDiversity := float64(len(dimensions)) / float64(len(guidelines))

	// Also consider semantic diversity (embeddings)
	semanticDiversity := gse.calculateSemanticDiversity(guidelines)

	// Weighted combination
	return 0.4*categoryDiversity + 0.3*dimensionDiversity + 0.3*semanticDiversity
}

// calculateSemanticDiversity measures semantic diversity using embeddings.
func (gse *GuidelineSelectionEvaluator) calculateSemanticDiversity(guidelines []*Content) float64 {
	if len(guidelines) <= 1 {
		return 1.0
	}

	// Calculate average pairwise similarity
	totalSimilarity := 0.0
	pairCount := 0

	for i := 0; i < len(guidelines); i++ {
		for j := i + 1; j < len(guidelines); j++ {
			similarity := cosineSimilarity(guidelines[i].Embedding, guidelines[j].Embedding)
			totalSimilarity += similarity
			pairCount++
		}
	}

	if pairCount == 0 {
		return 1.0
	}

	averageSimilarity := totalSimilarity / float64(pairCount)
	// Diversity is inverse of similarity
	return 1.0 - averageSimilarity
}

// calculateCategoryCoverage measures how well we cover important categories.
func (gse *GuidelineSelectionEvaluator) calculateCategoryCoverage(selected, all []*Content) float64 {
	// Get all available categories
	allCategories := make(map[string]bool)
	for _, guideline := range all {
		if category := guideline.Metadata["category"]; category != "" {
			allCategories[category] = true
		}
	}

	// Get covered categories
	coveredCategories := make(map[string]bool)
	for _, guideline := range selected {
		if category := guideline.Metadata["category"]; category != "" {
			coveredCategories[category] = true
		}
	}

	if len(allCategories) == 0 {
		return 1.0
	}

	return float64(len(coveredCategories)) / float64(len(allCategories))
}

// calculateDimensionCoverage measures coverage of review dimensions.
func (gse *GuidelineSelectionEvaluator) calculateDimensionCoverage(guidelines []*Content) float64 {
	dimensions := make(map[string]bool)
	for _, guideline := range guidelines {
		if dimension := guideline.Metadata["dimension"]; dimension != "" {
			dimensions[dimension] = true
		}
	}

	// Assume there are 4 main dimensions
	expectedDimensions := 4.0
	return float64(len(dimensions)) / expectedDimensions
}

// calculateSemanticCoverage measures how well selected guidelines cover query semantic space.
func (gse *GuidelineSelectionEvaluator) calculateSemanticCoverage(query []float32, guidelines []*Content) float64 {
	if len(guidelines) == 0 {
		return 0.0
	}

	// Find maximum similarity to query among selected guidelines
	maxSimilarity := 0.0
	for _, guideline := range guidelines {
		similarity := cosineSimilarity(query, guideline.Embedding)
		if similarity > maxSimilarity {
			maxSimilarity = similarity
		}
	}

	return maxSimilarity
}

// calculateRelevanceScores computes relevance scores for all selected guidelines.
func (gse *GuidelineSelectionEvaluator) calculateRelevanceScores(query []float32, guidelines []*Content) []float64 {
	scores := make([]float64, len(guidelines))
	for i, guideline := range guidelines {
		scores[i] = cosineSimilarity(query, guideline.Embedding)
	}
	return scores
}

// calculateAverageRelevance computes average relevance score.
func (gse *GuidelineSelectionEvaluator) calculateAverageRelevance(scores []float64) float64 {
	if len(scores) == 0 {
		return 0.0
	}

	sum := 0.0
	for _, score := range scores {
		sum += score
	}
	return sum / float64(len(scores))
}

// calculateTopKRelevance computes average relevance of top K items.
func (gse *GuidelineSelectionEvaluator) calculateTopKRelevance(scores []float64, k int) float64 {
	if len(scores) == 0 {
		return 0.0
	}

	// Sort scores in descending order
	sortedScores := make([]float64, len(scores))
	copy(sortedScores, scores)
	sort.Float64s(sortedScores)

	// Reverse to get descending order
	for i := 0; i < len(sortedScores)/2; i++ {
		j := len(sortedScores) - 1 - i
		sortedScores[i], sortedScores[j] = sortedScores[j], sortedScores[i]
	}

	// Calculate average of top K
	topK := k
	if topK > len(sortedScores) {
		topK = len(sortedScores)
	}

	sum := 0.0
	for i := 0; i < topK; i++ {
		sum += sortedScores[i]
	}

	return sum / float64(topK)
}

// calculateVariance computes variance in relevance scores.
func (gse *GuidelineSelectionEvaluator) calculateVariance(scores []float64) float64 {
	if len(scores) <= 1 {
		return 0.0
	}

	mean := gse.calculateAverageRelevance(scores)
	sumSquaredDiff := 0.0

	for _, score := range scores {
		diff := score - mean
		sumSquaredDiff += diff * diff
	}

	return sumSquaredDiff / float64(len(scores))
}

// calculateContentQuality evaluates the quality of guideline content.
func (gse *GuidelineSelectionEvaluator) calculateContentQuality(guidelines []*Content) float64 {
	if len(guidelines) == 0 {
		return 0.0
	}

	totalQuality := 0.0
	for _, guideline := range guidelines {
		quality := 0.0

		// Length factor (reasonable content length)
		textLen := len(guideline.Text)
		if textLen > 100 && textLen < 2000 {
			quality += 0.3
		}

		// Actionable content factor
		if gse.isActionableContent(guideline.Text) {
			quality += 0.4
		}

		// Metadata completeness
		if category := guideline.Metadata["category"]; category != "" {
			quality += 0.1
		}
		if dimension := guideline.Metadata["dimension"]; dimension != "" {
			quality += 0.1
		}
		if impact := guideline.Metadata["impact"]; impact != "" {
			quality += 0.1
		}

		totalQuality += quality
	}

	return totalQuality / float64(len(guidelines))
}

// calculateActionabilityScore measures how actionable the guidelines are.
func (gse *GuidelineSelectionEvaluator) calculateActionabilityScore(guidelines []*Content) float64 {
	if len(guidelines) == 0 {
		return 0.0
	}

	actionableCount := 0
	for _, guideline := range guidelines {
		if gse.isActionableContent(guideline.Text) {
			actionableCount++
		}
	}

	return float64(actionableCount) / float64(len(guidelines))
}

// calculateExampleQuality evaluates the quality of code examples.
func (gse *GuidelineSelectionEvaluator) calculateExampleQuality(guidelines []*Content) float64 {
	// This would need access to the original GuidelineContent with examples
	// For now, return a heuristic based on content
	totalQuality := 0.0
	count := 0

	for _, guideline := range guidelines {
		if strings.Contains(guideline.Text, "example") ||
			strings.Contains(guideline.Text, "Good:") ||
			strings.Contains(guideline.Text, "Bad:") {
			totalQuality += 1.0
			count++
		}
	}

	if count == 0 {
		return 0.0
	}

	return totalQuality / float64(count)
}

// calculateOptimizationGain measures improvement from optimization.
func (gse *GuidelineSelectionEvaluator) calculateOptimizationGain(query []float32, all, selected []*Content) float64 {
	if len(selected) == 0 || len(all) == 0 {
		return 0.0
	}

	// Calculate average relevance of selected
	selectedRelevance := 0.0
	for _, guideline := range selected {
		selectedRelevance += cosineSimilarity(query, guideline.Embedding)
	}
	selectedRelevance /= float64(len(selected))

	// Calculate average relevance of all candidates
	allRelevance := 0.0
	for _, guideline := range all {
		allRelevance += cosineSimilarity(query, guideline.Embedding)
	}
	allRelevance /= float64(len(all))

	if allRelevance == 0 {
		return 0.0
	}

	return (selectedRelevance - allRelevance) / allRelevance
}

// findSaturationPoint finds where marginal gains become minimal.
func (gse *GuidelineSelectionEvaluator) findSaturationPoint(query []float32, guidelines []*Content) int {
	if len(guidelines) <= 1 {
		return len(guidelines)
	}

	// Calculate cumulative relevance scores
	cumulativeScore := 0.0
	threshold := 0.05 // 5% marginal gain threshold

	for i, guideline := range guidelines {
		score := cosineSimilarity(query, guideline.Embedding)
		newCumulative := cumulativeScore + score

		if i > 0 {
			marginalGain := score / cumulativeScore
			if marginalGain < threshold {
				return i
			}
		}

		cumulativeScore = newCumulative
	}

	return len(guidelines)
}

// calculateBaselineImprovement measures improvement over random selection.
func (gse *GuidelineSelectionEvaluator) calculateBaselineImprovement(query []float32, all, selected []*Content) float64 {
	if len(selected) == 0 || len(all) < len(selected) {
		return 0.0
	}

	// Calculate selected average relevance
	selectedAvg := 0.0
	for _, guideline := range selected {
		selectedAvg += cosineSimilarity(query, guideline.Embedding)
	}
	selectedAvg /= float64(len(selected))

	// Calculate average of all candidates (approximates random selection)
	allAvg := 0.0
	for _, guideline := range all {
		allAvg += cosineSimilarity(query, guideline.Embedding)
	}
	allAvg /= float64(len(all))

	if allAvg == 0 {
		return 0.0
	}

	return (selectedAvg - allAvg) / allAvg
}

// isActionableContent checks if content provides actionable guidance.
func (gse *GuidelineSelectionEvaluator) isActionableContent(content string) bool {
	lowerContent := strings.ToLower(content)

	actionableKeywords := []string{
		"should", "must", "avoid", "prefer", "use", "don't", "do not",
		"always", "never", "ensure", "recommended", "best practice",
	}

	count := 0
	for _, keyword := range actionableKeywords {
		if strings.Contains(lowerContent, keyword) {
			count++
		}
	}

	return count >= 2 && len(content) > 50
}

// generateCategoryBreakdown creates category distribution.
func (gse *GuidelineSelectionEvaluator) generateCategoryBreakdown(guidelines []*Content, breakdown map[string]int) {
	for _, guideline := range guidelines {
		if category := guideline.Metadata["category"]; category != "" {
			breakdown[category]++
		}
	}
}

// generateDimensionBreakdown creates dimension distribution.
func (gse *GuidelineSelectionEvaluator) generateDimensionBreakdown(guidelines []*Content, breakdown map[string]int) {
	for _, guideline := range guidelines {
		if dimension := guideline.Metadata["dimension"]; dimension != "" {
			breakdown[dimension]++
		}
	}
}

// generateQualityDistribution creates quality score distribution.
func (gse *GuidelineSelectionEvaluator) generateQualityDistribution(guidelines []*Content, distribution map[string]int) {
	for _, guideline := range guidelines {
		quality := gse.assessGuidelineQuality(guideline)
		distribution[quality]++
	}
}

// assessGuidelineQuality assigns quality category to a guideline.
func (gse *GuidelineSelectionEvaluator) assessGuidelineQuality(guideline *Content) string {
	score := 0

	if len(guideline.Text) > 100 {
		score++
	}
	if gse.isActionableContent(guideline.Text) {
		score++
	}
	if guideline.Metadata["category"] != "" {
		score++
	}
	if guideline.Metadata["impact"] == "high" {
		score++
	}

	switch score {
	case 4:
		return "excellent"
	case 3:
		return "good"
	case 2:
		return "fair"
	default:
		return "poor"
	}
}

// generateRecommendations creates actionable recommendations.
func (gse *GuidelineSelectionEvaluator) generateRecommendations(ctx context.Context, metrics *SelectionQualityMetrics, guidelines []*Content) []string {
	var recommendations []string

	// Diversity recommendations
	if metrics.DiversityScore < 0.5 {
		recommendations = append(recommendations, "Consider selecting guidelines from more diverse categories to improve coverage")
	}

	// Relevance recommendations
	if metrics.AverageRelevance < 0.7 {
		recommendations = append(recommendations, "Average relevance is low - consider refining query embedding or selection criteria")
	}

	// Quality recommendations
	if metrics.ContentQuality < 0.6 {
		recommendations = append(recommendations, "Content quality could be improved - filter out low-quality guidelines")
	}

	// Coverage recommendations
	if metrics.CategoryCoverage < 0.5 {
		recommendations = append(recommendations, "Category coverage is incomplete - ensure important areas are represented")
	}

	// Efficiency recommendations
	if metrics.SelectionTime > 5*time.Second {
		recommendations = append(recommendations, "Selection time is high - consider optimizing the selection algorithm")
	}

	if len(recommendations) == 0 {
		recommendations = append(recommendations, "Selection quality is good across all metrics")
	}

	return recommendations
}

// generateDetailedAnalysis creates a detailed text analysis.
func (gse *GuidelineSelectionEvaluator) generateDetailedAnalysis(ctx context.Context, metrics *SelectionQualityMetrics, guidelines []*Content) string {
	var analysis strings.Builder

	analysis.WriteString(fmt.Sprintf("Selection Analysis for %d guidelines:\n\n", len(guidelines)))

	analysis.WriteString(fmt.Sprintf("Diversity Score: %.3f - ", metrics.DiversityScore))
	if metrics.DiversityScore > 0.7 {
		analysis.WriteString("Excellent diversity across categories and semantic space\n")
	} else if metrics.DiversityScore > 0.5 {
		analysis.WriteString("Good diversity, some room for improvement\n")
	} else {
		analysis.WriteString("Low diversity, consider broader selection criteria\n")
	}

	analysis.WriteString(fmt.Sprintf("Average Relevance: %.3f - ", metrics.AverageRelevance))
	if metrics.AverageRelevance > 0.8 {
		analysis.WriteString("Highly relevant to query context\n")
	} else if metrics.AverageRelevance > 0.6 {
		analysis.WriteString("Moderately relevant, acceptable quality\n")
	} else {
		analysis.WriteString("Low relevance, may need query refinement\n")
	}

	analysis.WriteString(fmt.Sprintf("Content Quality: %.3f - ", metrics.ContentQuality))
	if metrics.ContentQuality > 0.7 {
		analysis.WriteString("High-quality, actionable content\n")
	} else {
		analysis.WriteString("Quality could be improved through better filtering\n")
	}

	analysis.WriteString(fmt.Sprintf("\nSelection completed in %v with %.1f%% improvement over baseline",
		metrics.SelectionTime, metrics.BaselineImprovement*100))

	return analysis.String()
}

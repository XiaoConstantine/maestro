package main

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
)

// IntelligentFileProcessor provides advanced parallel processing with adaptive strategies
type IntelligentFileProcessor struct {
	chunkProcessor    core.Module
	adaptiveStrategy  *AdaptiveStrategy
	resourceMonitor   *ResourceMonitor
	loadBalancer      *LoadBalancer
	circuitBreaker    *CircuitBreaker
	metrics          *ParallelProcessingMetrics
	logger           logging.Logger
	config           IntelligentProcessingConfig
}

// IntelligentProcessingConfig configures the intelligent processor
type IntelligentProcessingConfig struct {
	MaxConcurrent     int
	AdaptiveThreshold float64
	ResourceLimits    ResourceLimits
	FailureThreshold  int
	RecoveryTime      time.Duration
	EnablePrioritization bool
	EnableLoadBalancing  bool
	EnableCircuitBreaker bool
}

// ResourceLimits defines system resource constraints
type ResourceLimits struct {
	MaxMemoryMB    int64
	MaxCPUPercent  float64
	MaxGoroutines  int
	MemoryWarning  int64
	CPUWarning     float64
}

// AdaptiveStrategy manages dynamic optimization of processing
type AdaptiveStrategy struct {
	currentStrategy  ProcessingStrategy
	performance     *PerformanceTracker
	adaptationRules []AdaptationRule
	mu             sync.RWMutex
}

type ProcessingStrategy int

const (
	ConservativeStrategy ProcessingStrategy = iota
	BalancedStrategy
	AggressiveStrategy
	CustomStrategy
)

// AdaptationRule defines when and how to adapt processing strategy
type AdaptationRule struct {
	Name      string
	Condition func(*PerformanceMetrics) bool
	Action    func(*AdaptiveStrategy) ProcessingStrategy
	Priority  int
}

// PerformanceTracker monitors processing performance
type PerformanceTracker struct {
	metrics     []PerformanceMetrics
	windowSize  int
	currentIdx  int
	mu         sync.RWMutex
}

// PerformanceMetrics captures processing performance data
type PerformanceMetrics struct {
	Timestamp         time.Time
	ThroughputMBPS    float64
	AverageLatency    time.Duration
	ErrorRate         float64
	ResourceUsage     ResourceUsage
	ConcurrencyLevel  int
	QueueDepth       int
}

// ResourceUsage tracks system resource consumption
type ResourceUsage struct {
	MemoryMB      int64
	CPUPercent    float64
	GoroutineCount int
	GCPauses      time.Duration
}

// ResourceMonitor tracks system resources in real-time
type ResourceMonitor struct {
	usage           ResourceUsage
	updateInterval  time.Duration
	alertThresholds ResourceLimits
	alerts         []ResourceAlert
	mu             sync.RWMutex
	stopCh         chan struct{}
}

// ResourceAlert represents a resource threshold violation
type ResourceAlert struct {
	Type      AlertType
	Message   string
	Severity  AlertSeverity
	Timestamp time.Time
}

type AlertType int
type AlertSeverity int

const (
	MemoryAlert AlertType = iota
	CPUAlert
	GoroutineAlert
)

const (
	Warning AlertSeverity = iota
	Critical
	Emergency
)

// LoadBalancer distributes work across available workers
type LoadBalancer struct {
	workers     []*Worker
	strategy    LoadBalancingStrategy
	metrics     *LoadBalancingMetrics
	mu         sync.RWMutex
}

type LoadBalancingStrategy int

const (
	RoundRobin LoadBalancingStrategy = iota
	LeastConnections
	WeightedRoundRobin
	LeastResponseTime
	ResourceAware
)

// Worker represents a processing worker with capabilities tracking
type Worker struct {
	ID              int
	Capacity        int
	CurrentLoad     int
	AverageLatency  time.Duration
	ErrorRate       float64
	LastUsed        time.Time
	IsHealthy       bool
	Specialization  WorkerSpecialization
	mu             sync.RWMutex
}

type WorkerSpecialization int

const (
	GeneralPurpose WorkerSpecialization = iota
	LargeFiles
	ComplexAnalysis
	FastProcessing
)

// LoadBalancingMetrics tracks load balancing performance
type LoadBalancingMetrics struct {
	TotalRequests     int64
	AverageLatency    time.Duration
	LoadDistribution  map[int]int64
	WorkerUtilization map[int]float64
}

// CircuitBreaker prevents system overload
type CircuitBreaker struct {
	state           CircuitState
	failureCount    int
	threshold       int
	timeout         time.Duration
	lastFailureTime time.Time
	mu             sync.RWMutex
}

type CircuitState int

const (
	CircuitClosed CircuitState = iota
	CircuitOpen
	CircuitHalfOpen
)

// ParallelProcessingMetrics tracks overall parallel processing performance
type ParallelProcessingMetrics struct {
	TotalTasks       int64
	CompletedTasks   int64
	FailedTasks      int64
	AverageLatency   time.Duration
	ThroughputMBPS   float64
	PeakConcurrency  int
	AdaptationEvents int
	mu              sync.RWMutex
}

// NewIntelligentFileProcessor creates an advanced parallel processing system
func NewIntelligentFileProcessor(config IntelligentProcessingConfig) *IntelligentFileProcessor {
	logger := logging.GetLogger()
	
	// Create chunk processing module with advanced reasoning
	chunkProcessor := modules.NewChainOfThought(
		core.NewSignature(
			[]core.InputField{
				{Field: core.Field{Name: "chunk_content", Description: "Code chunk to analyze"}},
				{Field: core.Field{Name: "file_context", Description: "File-level context"}},
				{Field: core.Field{Name: "complexity_hint", Description: "Estimated complexity level"}},
				{Field: core.Field{Name: "processing_priority", Description: "Processing priority level"}},
			},
			[]core.OutputField{
				{Field: core.Field{Name: "embedding", Description: "Semantic embedding of the chunk"}},
				{Field: core.Field{Name: "patterns", Description: "Identified code patterns"}},
				{Field: core.Field{Name: "quality_score", Description: "Code quality assessment"}},
				{Field: core.Field{Name: "processing_time", Description: "Time taken for processing"}},
			},
		).WithInstruction(`
		Analyze this code chunk intelligently:
		
		1. Generate meaningful semantic embeddings
		2. Identify architectural and design patterns
		3. Assess code quality and potential issues
		4. Consider the complexity hint for depth of analysis
		5. Respect processing priority for resource allocation
		
		Adapt your analysis depth based on complexity and priority.
		`),
	)
	
	// Initialize adaptive strategy
	adaptiveStrategy := NewAdaptiveStrategy()
	
	// Initialize resource monitor
	resourceMonitor := NewResourceMonitor(config.ResourceLimits)
	
	// Initialize load balancer
	loadBalancer := NewLoadBalancer(config.MaxConcurrent)
	
	// Initialize circuit breaker
	circuitBreaker := NewCircuitBreaker(config.FailureThreshold, config.RecoveryTime)
	
	return &IntelligentFileProcessor{
		chunkProcessor:   chunkProcessor,
		adaptiveStrategy: adaptiveStrategy,
		resourceMonitor:  resourceMonitor,
		loadBalancer:     loadBalancer,
		circuitBreaker:   circuitBreaker,
		metrics:         NewParallelProcessingMetrics(),
		logger:          *logger,
		config:          config,
	}
}

// ProcessFiles intelligently processes multiple files with adaptive optimization
func (p *IntelligentFileProcessor) ProcessFiles(ctx context.Context, tasks []PRReviewTask) (map[string]interface{}, error) {
	p.logger.Info(ctx, "🚀 Starting intelligent parallel processing for %d files", len(tasks))
	
	// Start resource monitoring
	p.resourceMonitor.Start()
	defer p.resourceMonitor.Stop()
	
	// Check circuit breaker
	if !p.circuitBreaker.AllowRequest() {
		return nil, fmt.Errorf("circuit breaker is open, system overloaded")
	}
	
	// Analyze and prepare adaptive chunks
	adaptiveChunks := p.createAdaptiveChunks(ctx, tasks)
	
	// Create processing plan with prioritization
	processingPlan := p.createProcessingPlan(ctx, adaptiveChunks)
	
	// Execute with intelligent coordination
	results, err := p.executeIntelligentProcessing(ctx, processingPlan)
	
	// Update circuit breaker
	if err != nil {
		p.circuitBreaker.RecordFailure()
	} else {
		p.circuitBreaker.RecordSuccess()
	}
	
	if err != nil {
		p.logger.Error(ctx, "❌ Intelligent processing failed: %v", err)
		return nil, err
	}
	
	p.logger.Info(ctx, "✅ Intelligent parallel processing completed successfully")
	return results, nil
}

// createAdaptiveChunks creates intelligent chunks based on multiple factors
func (p *IntelligentFileProcessor) createAdaptiveChunks(ctx context.Context, tasks []PRReviewTask) map[string][]AdaptiveChunk {
	p.logger.Debug(ctx, "🔍 Creating adaptive chunks for %d files", len(tasks))
	
	adaptiveChunks := make(map[string][]AdaptiveChunk)
	
	for _, task := range tasks {
		// Comprehensive analysis of the file
		analysis := p.analyzeFile(task)
		
		// Create chunks based on analysis
		chunks := p.createChunksForFile(task, analysis)
		adaptiveChunks[task.FilePath] = chunks
		
		p.logger.Debug(ctx, "📄 File %s: %d chunks, complexity %.2f, priority %d", 
			task.FilePath, len(chunks), analysis.Complexity, analysis.Priority)
	}
	
	return adaptiveChunks
}

// AdaptiveChunk represents an intelligently created chunk
type AdaptiveChunk struct {
	Content       string
	StartLine     int
	EndLine       int
	Complexity    float64
	Priority      int
	EstimatedTime time.Duration
	Dependencies  []string
	Metadata      map[string]interface{}
}

// FileAnalysis contains comprehensive file analysis
type FileAnalysis struct {
	FilePath           string
	Language           string
	LineCount          int
	Complexity         float64
	Priority           int
	ChangeImpact       float64
	ProcessingHint     ProcessingHint
	OptimalChunkSize   int
	EstimatedDuration  time.Duration
}

type ProcessingHint int

const (
	QuickScan ProcessingHint = iota
	StandardAnalysis
	DeepAnalysis
	ExpertReview
)

// analyzeFile performs comprehensive file analysis
func (p *IntelligentFileProcessor) analyzeFile(task PRReviewTask) FileAnalysis {
	content := task.FileContent
	lines := strings.Split(content, "\n")
	
	analysis := FileAnalysis{
		FilePath:  task.FilePath,
		Language:  detectLanguage(task.FilePath),
		LineCount: len(lines),
	}
	
	// Calculate complexity using multiple heuristics
	analysis.Complexity = p.calculateComplexity(content, lines)
	
	// Determine priority based on various factors
	analysis.Priority = p.calculatePriority(task, analysis.Complexity)
	
	// Assess change impact
	analysis.ChangeImpact = p.assessChangeImpact(task.Changes)
	
	// Determine processing hint
	analysis.ProcessingHint = p.determineProcessingHint(analysis)
	
	// Calculate optimal chunk size
	analysis.OptimalChunkSize = p.calculateOptimalChunkSize(analysis)
	
	// Estimate processing duration
	analysis.EstimatedDuration = p.estimateProcessingDuration(analysis)
	
	return analysis
}

// calculateComplexity uses sophisticated complexity analysis
func (p *IntelligentFileProcessor) calculateComplexity(content string, lines []string) float64 {
	complexity := 0.0
	
	// Cyclomatic complexity factors
	complexity += float64(strings.Count(content, "if ")) * 1.0
	complexity += float64(strings.Count(content, "for ")) * 1.5
	complexity += float64(strings.Count(content, "while ")) * 1.5
	complexity += float64(strings.Count(content, "switch ")) * 2.0
	complexity += float64(strings.Count(content, "case ")) * 0.5
	
	// Function complexity
	funcCount := strings.Count(content, "func ")
	complexity += float64(funcCount) * 2.0
	
	// Interface/struct complexity
	complexity += float64(strings.Count(content, "interface")) * 3.0
	complexity += float64(strings.Count(content, "struct")) * 2.0
	
	// Generic/template complexity
	complexity += float64(strings.Count(content, "<")) * 0.5
	complexity += float64(strings.Count(content, "[]")) * 0.3
	
	// Nesting level analysis
	maxNesting := 0
	currentNesting := 0
	for _, line := range lines {
		openBraces := strings.Count(line, "{")
		closeBraces := strings.Count(line, "}")
		currentNesting += openBraces - closeBraces
		if currentNesting > maxNesting {
			maxNesting = currentNesting
		}
	}
	complexity += float64(maxNesting) * 2.0
	
	// Normalize by line count
	if len(lines) > 0 {
		complexity = complexity / float64(len(lines)) * 100
	}
	
	// Apply logarithmic scaling and cap
	complexity = math.Log1p(complexity) / math.Log1p(10) // Log base 10
	return math.Min(complexity, 1.0)
}

// calculatePriority determines processing priority
func (p *IntelligentFileProcessor) calculatePriority(task PRReviewTask, complexity float64) int {
	priority := 5 // Base priority
	
	// Complexity factor
	if complexity > 0.8 {
		priority += 3
	} else if complexity > 0.6 {
		priority += 2
	} else if complexity > 0.4 {
		priority += 1
	}
	
	// File type priority
	if strings.Contains(task.FilePath, "main.go") || strings.Contains(task.FilePath, "server.go") {
		priority += 2
	}
	if strings.Contains(task.FilePath, "_test.go") {
		priority -= 1
	}
	if strings.Contains(task.FilePath, "vendor/") || strings.Contains(task.FilePath, "node_modules/") {
		priority -= 3
	}
	
	// Change size factor
	changeLines := len(strings.Split(task.Changes, "\n"))
	if changeLines > 100 {
		priority += 2
	} else if changeLines > 50 {
		priority += 1
	}
	
	return maxInt(1, minInt(priority, 10))
}

// createChunksForFile creates optimal chunks for a file
func (p *IntelligentFileProcessor) createChunksForFile(task PRReviewTask, analysis FileAnalysis) []AdaptiveChunk {
	lines := strings.Split(task.FileContent, "\n")
	chunks := []AdaptiveChunk{}
	
	chunkSize := analysis.OptimalChunkSize
	
	// Smart chunking that respects function boundaries
	currentChunk := AdaptiveChunk{
		Priority:   analysis.Priority,
		Metadata:   make(map[string]interface{}),
	}
	
	functionStart := -1
	bracesCount := 0
	
	for i, line := range lines {
		// Track function boundaries
		if strings.Contains(line, "func ") && functionStart == -1 {
			functionStart = i
		}
		
		bracesCount += strings.Count(line, "{") - strings.Count(line, "}")
		
		// Add line to current chunk
		if currentChunk.Content == "" {
			currentChunk.StartLine = i + 1
			currentChunk.Content = line
		} else {
			currentChunk.Content += "\n" + line
		}
		
		// Check if we should end this chunk
		shouldEndChunk := false
		
		// Size-based chunking
		currentLines := i - currentChunk.StartLine + 1
		if currentLines >= chunkSize {
			shouldEndChunk = true
		}
		
		// Function boundary chunking (prefer to end at function end)
		if functionStart >= 0 && bracesCount == 0 {
			shouldEndChunk = true
			functionStart = -1
		}
		
		// Force chunk end if too large
		if currentLines >= chunkSize*2 {
			shouldEndChunk = true
		}
		
		if shouldEndChunk || i == len(lines)-1 {
			currentChunk.EndLine = i + 1
			currentChunk.Complexity = p.calculateChunkComplexity(currentChunk.Content)
			currentChunk.EstimatedTime = p.estimateChunkProcessingTime(currentChunk)
			
			// Add metadata
			currentChunk.Metadata["line_count"] = currentChunk.EndLine - currentChunk.StartLine + 1
			currentChunk.Metadata["has_functions"] = strings.Contains(currentChunk.Content, "func ")
			currentChunk.Metadata["processing_hint"] = analysis.ProcessingHint
			
			chunks = append(chunks, currentChunk)
			
			// Reset for next chunk
			currentChunk = AdaptiveChunk{
				Priority: analysis.Priority,
				Metadata: make(map[string]interface{}),
			}
			functionStart = -1
		}
	}
	
	return chunks
}

// ProcessingPlan contains the execution plan for parallel processing
type ProcessingPlan struct {
	TotalChunks      int
	PriorityGroups   map[int][]ChunkTask
	ConcurrencyLevel int
	Strategy         ProcessingStrategy
	EstimatedDuration time.Duration
}

// ChunkTask represents a chunk processing task
type ChunkTask struct {
	ID           string
	FilePath     string
	Chunk        AdaptiveChunk
	Dependencies []string
	Worker       *Worker
}

// createProcessingPlan creates an intelligent processing plan
func (p *IntelligentFileProcessor) createProcessingPlan(ctx context.Context, adaptiveChunks map[string][]AdaptiveChunk) *ProcessingPlan {
	p.logger.Debug(ctx, "📋 Creating intelligent processing plan")
	
	// Get current performance metrics
	currentMetrics := p.adaptiveStrategy.GetCurrentMetrics()
	
	// Adapt strategy based on current conditions
	strategy := p.adaptiveStrategy.AdaptStrategy(currentMetrics)
	
	// Calculate optimal concurrency
	concurrency := p.calculateOptimalConcurrency(strategy)
	
	// Group chunks by priority
	priorityGroups := make(map[int][]ChunkTask)
	totalChunks := 0
	
	for filePath, chunks := range adaptiveChunks {
		for i, chunk := range chunks {
			task := ChunkTask{
				ID:       fmt.Sprintf("%s_chunk_%d", filePath, i),
				FilePath: filePath,
				Chunk:    chunk,
			}
			
			priority := chunk.Priority
			if _, exists := priorityGroups[priority]; !exists {
				priorityGroups[priority] = []ChunkTask{}
			}
			priorityGroups[priority] = append(priorityGroups[priority], task)
			totalChunks++
		}
	}
	
	// Estimate total duration
	estimatedDuration := p.estimatePlanDuration(priorityGroups, concurrency)
	
	plan := &ProcessingPlan{
		TotalChunks:       totalChunks,
		PriorityGroups:    priorityGroups,
		ConcurrencyLevel:  concurrency,
		Strategy:          strategy,
		EstimatedDuration: estimatedDuration,
	}
	
	p.logger.Info(ctx, "📋 Processing plan: %d chunks, concurrency %d, strategy %v, estimated duration %v",
		totalChunks, concurrency, strategy, estimatedDuration)
	
	return plan
}

// executeIntelligentProcessing executes the processing plan with full intelligence
func (p *IntelligentFileProcessor) executeIntelligentProcessing(ctx context.Context, plan *ProcessingPlan) (map[string]interface{}, error) {
	p.logger.Info(ctx, "⚡ Executing intelligent processing plan")
	
	// Create result aggregator
	results := make(map[string]interface{})
	var resultMu sync.Mutex
	
	// Sort priorities (higher priority first)
	priorities := make([]int, 0, len(plan.PriorityGroups))
	for priority := range plan.PriorityGroups {
		priorities = append(priorities, priority)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(priorities)))
	
	// Process groups by priority
	for _, priority := range priorities {
		tasks := plan.PriorityGroups[priority]
		p.logger.Debug(ctx, "🔄 Processing priority group %d with %d tasks", priority, len(tasks))
		
		// Execute priority group with load balancing
		groupResults, err := p.executeTaskGroup(ctx, tasks, plan.ConcurrencyLevel)
		if err != nil {
			return nil, fmt.Errorf("failed to process priority group %d: %w", priority, err)
		}
		
		// Merge results
		resultMu.Lock()
		for k, v := range groupResults {
			results[k] = v
		}
		resultMu.Unlock()
		
		// Adaptive optimization between groups
		p.adaptBetweenGroups(ctx)
	}
	
	// Add processing metadata
	results["processing_metadata"] = map[string]interface{}{
		"strategy":           plan.Strategy,
		"concurrency_level":  plan.ConcurrencyLevel,
		"total_chunks":       plan.TotalChunks,
		"processing_type":    "intelligent_parallel",
	}
	
	return results, nil
}

// executeTaskGroup executes a group of tasks with intelligent coordination
func (p *IntelligentFileProcessor) executeTaskGroup(ctx context.Context, tasks []ChunkTask, concurrency int) (map[string]interface{}, error) {
	// Create worker pool
	taskCh := make(chan ChunkTask, len(tasks))
	resultCh := make(chan TaskResult, len(tasks))
	
	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			p.processWorker(ctx, workerID, taskCh, resultCh)
		}(i)
	}
	
	// Send tasks
	for _, task := range tasks {
		taskCh <- task
	}
	close(taskCh)
	
	// Collect results
	results := make(map[string]interface{})
	for i := 0; i < len(tasks); i++ {
		result := <-resultCh
		if result.Error != nil {
			p.logger.Warn(ctx, "⚠️ Task %s failed: %v", result.TaskID, result.Error)
			p.metrics.RecordFailure()
		} else {
			results[result.TaskID] = result.Data
			p.metrics.RecordSuccess(result.Duration)
		}
	}
	
	// Wait for all workers to finish
	wg.Wait()
	close(resultCh)
	
	return results, nil
}

// TaskResult represents the result of a task execution
type TaskResult struct {
	TaskID   string
	Data     interface{}
	Error    error
	Duration time.Duration
}

// processWorker processes tasks with intelligent optimization
func (p *IntelligentFileProcessor) processWorker(ctx context.Context, workerID int, taskCh <-chan ChunkTask, resultCh chan<- TaskResult) {
	worker := p.loadBalancer.GetWorker(workerID)
	
	for task := range taskCh {
		startTime := time.Now()
		
		// Update worker load
		worker.IncreaseLoad()
		
		// Process the chunk
		result, err := p.processChunk(ctx, task)
		duration := time.Since(startTime)
		
		// Update worker metrics
		worker.UpdateMetrics(duration, err)
		worker.DecreaseLoad()
		
		// Send result
		resultCh <- TaskResult{
			TaskID:   task.ID,
			Data:     result,
			Error:    err,
			Duration: duration,
		}
		
		// Check for adaptive triggers
		if p.shouldAdapt(worker, duration, err) {
			p.triggerAdaptation(ctx)
		}
	}
}

// processChunk processes a single chunk with intelligence
func (p *IntelligentFileProcessor) processChunk(ctx context.Context, task ChunkTask) (interface{}, error) {
	// Prepare inputs based on chunk metadata
	inputs := map[string]interface{}{
		"chunk_content":      task.Chunk.Content,
		"file_context":       fmt.Sprintf("File: %s, Lines: %d-%d", task.FilePath, task.Chunk.StartLine, task.Chunk.EndLine),
		"complexity_hint":    task.Chunk.Complexity,
		"processing_priority": task.Chunk.Priority,
	}
	
	// Add metadata
	for k, v := range task.Chunk.Metadata {
		inputs[k] = v
	}
	
	// Process with chain of thought
	result, err := p.chunkProcessor.Process(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("chunk processing failed: %w", err)
	}
	
	return result, nil
}

// Helper functions for intelligent processing

func (p *IntelligentFileProcessor) calculateOptimalConcurrency(strategy ProcessingStrategy) int {
	cpuCount := runtime.NumCPU()
	
	switch strategy {
	case ConservativeStrategy:
		return maxInt(1, cpuCount/2)
	case BalancedStrategy:
		return cpuCount
	case AggressiveStrategy:
		return cpuCount * 2
	default:
		return cpuCount
	}
}

func (p *IntelligentFileProcessor) shouldAdapt(worker *Worker, duration time.Duration, err error) bool {
	if err != nil {
		return true
	}
	if duration > worker.AverageLatency*2 {
		return true
	}
	return false
}

func (p *IntelligentFileProcessor) triggerAdaptation(ctx context.Context) {
	currentMetrics := p.adaptiveStrategy.GetCurrentMetrics()
	newStrategy := p.adaptiveStrategy.AdaptStrategy(currentMetrics)
	p.logger.Debug(ctx, "🔄 Triggered adaptation to strategy: %v", newStrategy)
}

func (p *IntelligentFileProcessor) adaptBetweenGroups(ctx context.Context) {
	// Check resource usage and adapt
	usage := p.resourceMonitor.GetCurrentUsage()
	if usage.MemoryMB > p.config.ResourceLimits.MemoryWarning {
		p.logger.Warn(ctx, "⚠️ High memory usage detected: %d MB", usage.MemoryMB)
		// Trigger garbage collection
		runtime.GC()
	}
}

// Utility functions

func detectLanguage(filePath string) string {
	ext := strings.ToLower(filePath[strings.LastIndex(filePath, ".")+1:])
	switch ext {
	case "go":
		return "Go"
	case "js", "ts":
		return "JavaScript"
	case "py":
		return "Python"
	case "java":
		return "Java"
	case "cpp", "cc", "cxx":
		return "C++"
	case "c":
		return "C"
	default:
		return "Unknown"
	}
}

func (p *IntelligentFileProcessor) calculateChunkComplexity(content string) float64 {
	return p.calculateComplexity(content, strings.Split(content, "\n"))
}

func (p *IntelligentFileProcessor) estimateChunkProcessingTime(chunk AdaptiveChunk) time.Duration {
	baseTime := 100 * time.Millisecond
	complexityFactor := 1 + chunk.Complexity
	sizeFactor := 1 + float64(len(chunk.Content))/10000
	
	return time.Duration(float64(baseTime) * complexityFactor * sizeFactor)
}

func (p *IntelligentFileProcessor) assessChangeImpact(changes string) float64 {
	addedLines := strings.Count(changes, "+")
	removedLines := strings.Count(changes, "-")
	totalLines := addedLines + removedLines
	
	if totalLines == 0 {
		return 0.0
	}
	
	// Higher ratio of additions vs removals indicates higher impact
	impactRatio := float64(addedLines) / float64(totalLines)
	
	// Scale by total change size
	sizeFactor := math.Log1p(float64(totalLines)) / 10
	
	return math.Min(impactRatio*sizeFactor, 1.0)
}

func (p *IntelligentFileProcessor) determineProcessingHint(analysis FileAnalysis) ProcessingHint {
	if analysis.Complexity < 0.3 && analysis.LineCount < 100 {
		return QuickScan
	} else if analysis.Complexity < 0.6 && analysis.LineCount < 500 {
		return StandardAnalysis
	} else if analysis.Complexity < 0.8 && analysis.LineCount < 1000 {
		return DeepAnalysis
	} else {
		return ExpertReview
	}
}

func (p *IntelligentFileProcessor) calculateOptimalChunkSize(analysis FileAnalysis) int {
	baseSize := 50 // lines
	
	// Adjust based on complexity
	if analysis.Complexity > 0.7 {
		baseSize = 30 // Smaller chunks for complex code
	} else if analysis.Complexity < 0.3 {
		baseSize = 100 // Larger chunks for simple code
	}
	
	// Adjust based on language
	switch analysis.Language {
	case "Go":
		baseSize = int(float64(baseSize) * 1.2) // Go functions tend to be longer
	case "JavaScript":
		baseSize = int(float64(baseSize) * 0.8) // JS can be more condensed
	}
	
	return maxInt(20, minInt(baseSize, 200))
}

func (p *IntelligentFileProcessor) estimateProcessingDuration(analysis FileAnalysis) time.Duration {
	baseTime := 500 * time.Millisecond
	
	// Factor in complexity
	complexityFactor := 1 + analysis.Complexity*2
	
	// Factor in size
	sizeFactor := 1 + float64(analysis.LineCount)/1000
	
	// Factor in change impact
	impactFactor := 1 + analysis.ChangeImpact
	
	totalFactor := complexityFactor * sizeFactor * impactFactor
	
	return time.Duration(float64(baseTime) * totalFactor)
}

func (p *IntelligentFileProcessor) estimatePlanDuration(priorityGroups map[int][]ChunkTask, concurrency int) time.Duration {
	totalTime := time.Duration(0)
	
	for _, tasks := range priorityGroups {
		// Estimate time for this priority group
		groupTime := time.Duration(0)
		for _, task := range tasks {
			groupTime += task.Chunk.EstimatedTime
		}
		
		// Account for parallelism
		parallelTime := groupTime / time.Duration(concurrency)
		totalTime += parallelTime
	}
	
	return totalTime
}

// Constructor functions for helper components

func NewAdaptiveStrategy() *AdaptiveStrategy {
	return &AdaptiveStrategy{
		currentStrategy: BalancedStrategy,
		performance:    NewPerformanceTracker(100),
		adaptationRules: []AdaptationRule{
			{
				Name:     "high_error_rate",
				Condition: func(m *PerformanceMetrics) bool { return m.ErrorRate > 0.1 },
				Action:   func(s *AdaptiveStrategy) ProcessingStrategy { return ConservativeStrategy },
				Priority: 1,
			},
			{
				Name:     "high_latency",
				Condition: func(m *PerformanceMetrics) bool { return m.AverageLatency > 5*time.Second },
				Action:   func(s *AdaptiveStrategy) ProcessingStrategy { return ConservativeStrategy },
				Priority: 2,
			},
			{
				Name:     "low_resource_usage",
				Condition: func(m *PerformanceMetrics) bool { return m.ResourceUsage.CPUPercent < 50 },
				Action:   func(s *AdaptiveStrategy) ProcessingStrategy { return AggressiveStrategy },
				Priority: 3,
			},
		},
	}
}

func (as *AdaptiveStrategy) GetCurrentMetrics() *PerformanceMetrics {
	as.mu.RLock()
	defer as.mu.RUnlock()
	return as.performance.GetLatest()
}

func (as *AdaptiveStrategy) AdaptStrategy(metrics *PerformanceMetrics) ProcessingStrategy {
	as.mu.Lock()
	defer as.mu.Unlock()
	
	// Apply adaptation rules
	for _, rule := range as.adaptationRules {
		if rule.Condition(metrics) {
			newStrategy := rule.Action(as)
			if newStrategy != as.currentStrategy {
				as.currentStrategy = newStrategy
				break
			}
		}
	}
	
	return as.currentStrategy
}

func NewPerformanceTracker(windowSize int) *PerformanceTracker {
	return &PerformanceTracker{
		metrics:    make([]PerformanceMetrics, windowSize),
		windowSize: windowSize,
	}
}

func (pt *PerformanceTracker) GetLatest() *PerformanceMetrics {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return &pt.metrics[pt.currentIdx]
}

func NewResourceMonitor(limits ResourceLimits) *ResourceMonitor {
	return &ResourceMonitor{
		updateInterval:  time.Second,
		alertThresholds: limits,
		stopCh:         make(chan struct{}),
	}
}

func (rm *ResourceMonitor) Start() {
	go rm.monitor()
}

func (rm *ResourceMonitor) Stop() {
	close(rm.stopCh)
}

func (rm *ResourceMonitor) GetCurrentUsage() ResourceUsage {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.usage
}

func (rm *ResourceMonitor) monitor() {
	ticker := time.NewTicker(rm.updateInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-rm.stopCh:
			return
		case <-ticker.C:
			rm.updateUsage()
		}
	}
}

func (rm *ResourceMonitor) updateUsage() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	rm.mu.Lock()
	rm.usage = ResourceUsage{
		MemoryMB:       int64(m.Sys / 1024 / 1024),
		GoroutineCount: runtime.NumGoroutine(),
		GCPauses:       time.Duration(m.PauseNs[(m.NumGC+255)%256]),
	}
	rm.mu.Unlock()
}

func NewLoadBalancer(workerCount int) *LoadBalancer {
	workers := make([]*Worker, workerCount)
	for i := 0; i < workerCount; i++ {
		workers[i] = &Worker{
			ID:             i,
			Capacity:       10,
			IsHealthy:      true,
			Specialization: GeneralPurpose,
		}
	}
	
	return &LoadBalancer{
		workers:  workers,
		strategy: ResourceAware,
		metrics:  &LoadBalancingMetrics{
			LoadDistribution:  make(map[int]int64),
			WorkerUtilization: make(map[int]float64),
		},
	}
}

func (lb *LoadBalancer) GetWorker(id int) *Worker {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	if id < len(lb.workers) {
		return lb.workers[id]
	}
	return lb.workers[0] // Fallback
}

func (w *Worker) IncreaseLoad() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.CurrentLoad++
	w.LastUsed = time.Now()
}

func (w *Worker) DecreaseLoad() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.CurrentLoad > 0 {
		w.CurrentLoad--
	}
}

func (w *Worker) UpdateMetrics(latency time.Duration, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	
	// Simple exponential moving average
	alpha := 0.1
	w.AverageLatency = time.Duration(float64(w.AverageLatency)*(1-alpha) + float64(latency)*alpha)
	
	if err != nil {
		w.ErrorRate = w.ErrorRate*(1-alpha) + alpha
	} else {
		w.ErrorRate = w.ErrorRate * (1 - alpha)
	}
}

func NewCircuitBreaker(threshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:     CircuitClosed,
		threshold: threshold,
		timeout:   timeout,
	}
}

func (cb *CircuitBreaker) AllowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.state = CircuitHalfOpen
			return true
		}
		return false
	case CircuitHalfOpen:
		return true
	default:
		return false
	}
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.failureCount = 0
	if cb.state == CircuitHalfOpen {
		cb.state = CircuitClosed
	}
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.failureCount++
	cb.lastFailureTime = time.Now()
	
	if cb.failureCount >= cb.threshold {
		cb.state = CircuitOpen
	}
}

func NewParallelProcessingMetrics() *ParallelProcessingMetrics {
	return &ParallelProcessingMetrics{}
}

func (m *ParallelProcessingMetrics) RecordSuccess(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.CompletedTasks++
	m.TotalTasks++
	
	// Simple moving average for latency
	alpha := 0.1
	m.AverageLatency = time.Duration(float64(m.AverageLatency)*(1-alpha) + float64(duration)*alpha)
}

func (m *ParallelProcessingMetrics) RecordFailure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.FailedTasks++
	m.TotalTasks++
}

// Utility functions
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
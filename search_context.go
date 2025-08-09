package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// TokenEstimator estimates token counts for text
type TokenEstimator interface {
	EstimateTokens(text string) int
}

// SimpleTokenEstimator provides basic token estimation
type SimpleTokenEstimator struct{}

func (e *SimpleTokenEstimator) EstimateTokens(text string) int {
	// Rough approximation: 1 token ≈ 4 characters (as mentioned in tweet)
	return len(text) / 4
}

// ContextWindow manages a fixed-size context window for agents
type ContextWindow struct {
	maxTokens      int
	currentTokens  int
	items          []*ContextItem
	estimator      TokenEstimator
	logger         *logging.Logger
	mu             sync.RWMutex
	priorityQueue  *PriorityQueue
}

// ContextItem represents an item in the context window
type ContextItem struct {
	ID         string
	Content    string
	Tokens     int
	Priority   float64 // Higher priority items are kept when space is limited
	Timestamp  time.Time
	Source     string // Which sub-agent or search produced this
	ItemType   string // "code", "guideline", "summary", etc.
	Metadata   map[string]interface{}
}

// NewContextWindow creates a new context window
func NewContextWindow(maxTokens int, logger *logging.Logger) *ContextWindow {
	return &ContextWindow{
		maxTokens:     maxTokens,
		items:         make([]*ContextItem, 0),
		estimator:     &SimpleTokenEstimator{},
		logger:        logger,
		priorityQueue: NewPriorityQueue(),
	}
}

// Add adds an item to the context window
func (cw *ContextWindow) Add(ctx context.Context, item *ContextItem) error {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	
	// Estimate tokens if not provided
	if item.Tokens == 0 {
		item.Tokens = cw.estimator.EstimateTokens(item.Content)
	}
	
	// Check if item fits
	if item.Tokens > cw.maxTokens {
		return fmt.Errorf("item too large (%d tokens) for context window (%d tokens)", 
			item.Tokens, cw.maxTokens)
	}
	
	// Make room if necessary
	if cw.currentTokens+item.Tokens > cw.maxTokens {
		cw.logger.Debug(ctx, "Context window full, making room for new item")
		cw.makeRoom(ctx, item.Tokens)
	}
	
	// Add item
	item.Timestamp = time.Now()
	cw.items = append(cw.items, item)
	cw.priorityQueue.Push(item)
	cw.currentTokens += item.Tokens
	
	cw.logger.Debug(ctx, "Added item to context: %s (%d tokens, %.2f priority)", 
		item.ID, item.Tokens, item.Priority)
	
	return nil
}

// makeRoom removes lower priority items to make space
func (cw *ContextWindow) makeRoom(ctx context.Context, neededTokens int) {
	targetTokens := cw.maxTokens - neededTokens
	
	// Sort items by priority (ascending, so lowest priority first)
	sort.Slice(cw.items, func(i, j int) bool {
		return cw.items[i].Priority < cw.items[j].Priority
	})
	
	// Remove items until we have enough space
	var kept []*ContextItem
	currentTotal := 0
	
	for i := len(cw.items) - 1; i >= 0; i-- {
		item := cw.items[i]
		if currentTotal+item.Tokens <= targetTokens {
			kept = append([]*ContextItem{item}, kept...)
			currentTotal += item.Tokens
		} else {
			cw.logger.Debug(ctx, "Evicting item from context: %s (priority: %.2f)", 
				item.ID, item.Priority)
		}
	}
	
	cw.items = kept
	cw.currentTokens = currentTotal
}

// GetContent returns all content in the window as a single string
func (cw *ContextWindow) GetContent() string {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	
	var parts []string
	for _, item := range cw.items {
		parts = append(parts, item.Content)
	}
	
	return strings.Join(parts, "\n\n---\n\n")
}

// GetStructuredContent returns content organized by type
func (cw *ContextWindow) GetStructuredContent() map[string][]string {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	
	structured := make(map[string][]string)
	for _, item := range cw.items {
		structured[item.ItemType] = append(structured[item.ItemType], item.Content)
	}
	
	return structured
}

// GetTokenUsage returns current and max token counts
func (cw *ContextWindow) GetTokenUsage() (current, max int) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cw.currentTokens, cw.maxTokens
}

// Clear removes all items from the context window
func (cw *ContextWindow) Clear() {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	
	cw.items = make([]*ContextItem, 0)
	cw.currentTokens = 0
	cw.priorityQueue = NewPriorityQueue()
}

// Compress intelligently compresses the context to fit more information
func (cw *ContextWindow) Compress(ctx context.Context, targetRatio float64) string {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	
	cw.logger.Debug(ctx, "Compressing context with ratio %.2f", targetRatio)
	
	// Group items by type
	grouped := cw.GetStructuredContent()
	
	var compressed []string
	
	// Compress each type differently
	for itemType, contents := range grouped {
		switch itemType {
		case "code":
			// Keep function signatures and key logic
			compressed = append(compressed, cw.compressCode(contents, targetRatio))
		case "guideline":
			// Keep rule names and key points
			compressed = append(compressed, cw.compressGuidelines(contents, targetRatio))
		case "summary":
			// Summaries are already compressed, keep as is
			compressed = append(compressed, contents...)
		default:
			// Generic compression
			compressed = append(compressed, cw.genericCompress(contents, targetRatio))
		}
	}
	
	return strings.Join(compressed, "\n\n")
}

// compressCode compresses code content
func (cw *ContextWindow) compressCode(contents []string, ratio float64) string {
	var compressed []string
	
	for _, content := range contents {
		lines := strings.Split(content, "\n")
		targetLines := int(float64(len(lines)) * ratio)
		
		// Keep function signatures and important lines
		var kept []string
		for _, line := range lines {
			if strings.Contains(line, "func ") ||
				strings.Contains(line, "type ") ||
				strings.Contains(line, "interface ") ||
				strings.Contains(line, "struct ") ||
				strings.Contains(line, "return ") ||
				strings.Contains(line, "error") {
				kept = append(kept, line)
				if len(kept) >= targetLines {
					break
				}
			}
		}
		
		compressed = append(compressed, strings.Join(kept, "\n"))
	}
	
	return fmt.Sprintf("=== Compressed Code (%d items) ===\n%s", 
		len(contents), strings.Join(compressed, "\n---\n"))
}

// compressGuidelines compresses guideline content
func (cw *ContextWindow) compressGuidelines(contents []string, ratio float64) string {
	var compressed []string
	
	for _, content := range contents {
		// Extract key points (lines starting with bullets or numbers)
		lines := strings.Split(content, "\n")
		var keyPoints []string
		
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "-") ||
				strings.HasPrefix(trimmed, "*") ||
				strings.HasPrefix(trimmed, "•") ||
				(len(trimmed) > 0 && trimmed[0] >= '1' && trimmed[0] <= '9') {
				keyPoints = append(keyPoints, line)
			}
		}
		
		targetPoints := int(float64(len(keyPoints)) * ratio)
		if targetPoints > 0 && len(keyPoints) > targetPoints {
			keyPoints = keyPoints[:targetPoints]
		}
		
		compressed = append(compressed, strings.Join(keyPoints, "\n"))
	}
	
	return fmt.Sprintf("=== Compressed Guidelines (%d items) ===\n%s",
		len(contents), strings.Join(compressed, "\n---\n"))
}

// genericCompress performs generic text compression
func (cw *ContextWindow) genericCompress(contents []string, ratio float64) string {
	var compressed []string
	
	for _, content := range contents {
		targetLen := int(float64(len(content)) * ratio)
		if len(content) > targetLen {
			compressed = append(compressed, content[:targetLen]+"...")
		} else {
			compressed = append(compressed, content)
		}
	}
	
	return strings.Join(compressed, "\n")
}

// PriorityQueue manages items by priority
type PriorityQueue struct {
	items []*ContextItem
	mu    sync.RWMutex
}

func NewPriorityQueue() *PriorityQueue {
	return &PriorityQueue{
		items: make([]*ContextItem, 0),
	}
}

func (pq *PriorityQueue) Push(item *ContextItem) {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	
	pq.items = append(pq.items, item)
	// Keep sorted by priority
	sort.Slice(pq.items, func(i, j int) bool {
		return pq.items[i].Priority > pq.items[j].Priority
	})
}

func (pq *PriorityQueue) Pop() *ContextItem {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	
	if len(pq.items) == 0 {
		return nil
	}
	
	item := pq.items[0]
	pq.items = pq.items[1:]
	return item
}

func (pq *PriorityQueue) Len() int {
	pq.mu.RLock()
	defer pq.mu.RUnlock()
	return len(pq.items)
}

// ContextSynthesizer synthesizes multiple contexts into a coherent summary
type ContextSynthesizer struct {
	logger *logging.Logger
}

func NewContextSynthesizer(logger *logging.Logger) *ContextSynthesizer {
	return &ContextSynthesizer{logger: logger}
}

// Synthesize combines multiple context windows into a single coherent context
func (cs *ContextSynthesizer) Synthesize(ctx context.Context, windows []*ContextWindow, maxTokens int) *ContextWindow {
	cs.logger.Debug(ctx, "Synthesizing %d context windows into %d tokens", len(windows), maxTokens)
	
	synthesized := NewContextWindow(maxTokens, cs.logger)
	
	// Collect all items from all windows
	var allItems []*ContextItem
	for _, window := range windows {
		window.mu.RLock()
		allItems = append(allItems, window.items...)
		window.mu.RUnlock()
	}
	
	// Sort by priority
	sort.Slice(allItems, func(i, j int) bool {
		return allItems[i].Priority > allItems[j].Priority
	})
	
	// Add items to synthesized window until full
	for _, item := range allItems {
		err := synthesized.Add(ctx, item)
		if err != nil {
			cs.logger.Debug(ctx, "Cannot add item to synthesized context: %v", err)
			break
		}
	}
	
	return synthesized
}
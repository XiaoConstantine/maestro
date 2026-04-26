package budget

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

const (
	DefaultCacheReadInputTokenWeight = 0.10
	DefaultWarnThreshold             = 0.80
)

type Config struct {
	MaxBudgetUSD              float64
	WarnThreshold             float64
	CacheReadInputTokenWeight float64
}

type UsageDelta struct {
	PromptTokens                int64
	CompletionTokens            int64
	TotalTokens                 int64
	CacheReadInputTokens        int64
	CacheCreationInputTokens    int64
	CacheTokenWeightUnavailable bool
	CostUSD                     float64
}

type AgentUsage struct {
	Agent                       string    `json:"agent"`
	Calls                       int64     `json:"calls"`
	PromptTokens                int64     `json:"prompt_tokens"`
	CompletionTokens            int64     `json:"completion_tokens"`
	TotalTokens                 int64     `json:"total_tokens"`
	CacheReadInputTokens        int64     `json:"cache_read_input_tokens"`
	CacheCreationInputTokens    int64     `json:"cache_creation_input_tokens"`
	CacheTokenWeightUnavailable bool      `json:"cache_token_weight_unavailable"`
	WeightedPromptTokens        float64   `json:"weighted_prompt_tokens"`
	WeightedTotalTokens         float64   `json:"weighted_total_tokens"`
	CostUSD                     float64   `json:"cost_usd"`
	LastUsedAt                  time.Time `json:"last_used_at"`
}

type BudgetStatus struct {
	MaxBudgetUSD                float64               `json:"max_budget_usd"`
	TotalSpentUSD               float64               `json:"total_spent_usd"`
	RemainingUSD                float64               `json:"remaining_usd"`
	PercentUsed                 float64               `json:"percent_used"`
	WarnThreshold               float64               `json:"warn_threshold"`
	Warning                     bool                  `json:"warning"`
	Exceeded                    bool                  `json:"exceeded"`
	PromptTokens                int64                 `json:"prompt_tokens"`
	CompletionTokens            int64                 `json:"completion_tokens"`
	TotalTokens                 int64                 `json:"total_tokens"`
	CacheReadInputTokens        int64                 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens    int64                 `json:"cache_creation_input_tokens"`
	CacheTokenWeightUnavailable bool                  `json:"cache_token_weight_unavailable"`
	WeightedPromptTokens        float64               `json:"weighted_prompt_tokens"`
	WeightedTotalTokens         float64               `json:"weighted_total_tokens"`
	Calls                       int64                 `json:"calls"`
	ByAgent                     map[string]AgentUsage `json:"by_agent"`
}

type BudgetManager struct {
	mu     sync.RWMutex
	cfg    Config
	totals AgentUsage
	agents map[string]AgentUsage
}

type TokenTrackerSnapshot struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	CostUSD          float64
}

type CacheTokenUsage struct {
	CacheReadInputTokens     int64
	CacheCreationInputTokens int64
}

var (
	defaultMu      sync.RWMutex
	defaultManager = NewBudgetManager(DefaultConfig())
)

func DefaultConfig() Config {
	return Config{
		WarnThreshold:             DefaultWarnThreshold,
		CacheReadInputTokenWeight: DefaultCacheReadInputTokenWeight,
	}
}

func NewBudgetManager(cfg Config) *BudgetManager {
	return &BudgetManager{
		cfg:    normalizeConfig(cfg),
		agents: make(map[string]AgentUsage),
	}
}

func DefaultManager() *BudgetManager {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultManager
}

func SetDefaultManager(manager *BudgetManager) {
	if manager == nil {
		manager = NewBudgetManager(DefaultConfig())
	}
	defaultMu.Lock()
	defaultManager = manager
	defaultMu.Unlock()
}

func ResetDefaultManagerForTest(manager *BudgetManager) func() {
	defaultMu.Lock()
	previous := defaultManager
	if manager == nil {
		manager = NewBudgetManager(DefaultConfig())
	}
	defaultManager = manager
	defaultMu.Unlock()
	return func() {
		defaultMu.Lock()
		defaultManager = previous
		defaultMu.Unlock()
	}
}

func (m *BudgetManager) Config() Config {
	if m == nil {
		return DefaultConfig()
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *BudgetManager) RecordUsage(agent string, promptTokens, completionTokens int, costUSD float64) error {
	return m.RecordUsageDelta(agent, UsageDelta{
		PromptTokens:     int64(promptTokens),
		CompletionTokens: int64(completionTokens),
		CostUSD:          costUSD,
	})
}

func (m *BudgetManager) RecordUsageDelta(agent string, delta UsageDelta) error {
	if m == nil {
		return nil
	}
	agent = strings.TrimSpace(agent)
	if agent == "" {
		agent = "unknown"
	}
	delta = normalizeUsageDelta(delta)
	if delta.Empty() {
		return nil
	}

	weightedPrompt := weightedPromptTokens(delta, m.Config().CacheReadInputTokenWeight)
	weightedTotal := weightedPrompt + float64(delta.CompletionTokens)
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.totals.Calls++
	m.totals.PromptTokens += delta.PromptTokens
	m.totals.CompletionTokens += delta.CompletionTokens
	m.totals.TotalTokens += delta.TotalTokens
	m.totals.CacheReadInputTokens += delta.CacheReadInputTokens
	m.totals.CacheCreationInputTokens += delta.CacheCreationInputTokens
	m.totals.CacheTokenWeightUnavailable = m.totals.CacheTokenWeightUnavailable || delta.CacheTokenWeightUnavailable
	m.totals.WeightedPromptTokens += weightedPrompt
	m.totals.WeightedTotalTokens += weightedTotal
	m.totals.CostUSD += delta.CostUSD
	m.totals.LastUsedAt = now

	record := m.agents[agent]
	record.Agent = agent
	record.Calls++
	record.PromptTokens += delta.PromptTokens
	record.CompletionTokens += delta.CompletionTokens
	record.TotalTokens += delta.TotalTokens
	record.CacheReadInputTokens += delta.CacheReadInputTokens
	record.CacheCreationInputTokens += delta.CacheCreationInputTokens
	record.CacheTokenWeightUnavailable = record.CacheTokenWeightUnavailable || delta.CacheTokenWeightUnavailable
	record.WeightedPromptTokens += weightedPrompt
	record.WeightedTotalTokens += weightedTotal
	record.CostUSD += delta.CostUSD
	record.LastUsedAt = now
	m.agents[agent] = record

	return nil
}

func (m *BudgetManager) RecordTokenTrackerDelta(agent string, tracker *modrlm.TokenTracker, previous TokenTrackerSnapshot, cache CacheTokenUsage) (TokenTrackerSnapshot, error) {
	current := SnapshotTokenTracker(tracker)
	delta := UsageDelta{
		PromptTokens:             current.PromptTokens - previous.PromptTokens,
		CompletionTokens:         current.CompletionTokens - previous.CompletionTokens,
		TotalTokens:              current.TotalTokens - previous.TotalTokens,
		CostUSD:                  current.CostUSD - previous.CostUSD,
		CacheReadInputTokens:     cache.CacheReadInputTokens,
		CacheCreationInputTokens: cache.CacheCreationInputTokens,
	}
	return current, m.RecordUsageDelta(agent, delta)
}

func (m *BudgetManager) Status() BudgetStatus {
	if m == nil {
		return BudgetStatus{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := BudgetStatus{
		MaxBudgetUSD:                m.cfg.MaxBudgetUSD,
		TotalSpentUSD:               m.totals.CostUSD,
		WarnThreshold:               m.cfg.WarnThreshold,
		PromptTokens:                m.totals.PromptTokens,
		CompletionTokens:            m.totals.CompletionTokens,
		TotalTokens:                 m.totals.TotalTokens,
		CacheReadInputTokens:        m.totals.CacheReadInputTokens,
		CacheCreationInputTokens:    m.totals.CacheCreationInputTokens,
		CacheTokenWeightUnavailable: m.totals.CacheTokenWeightUnavailable,
		WeightedPromptTokens:        m.totals.WeightedPromptTokens,
		WeightedTotalTokens:         m.totals.WeightedTotalTokens,
		Calls:                       m.totals.Calls,
		ByAgent:                     make(map[string]AgentUsage, len(m.agents)),
	}
	if status.MaxBudgetUSD > 0 {
		status.RemainingUSD = math.Max(0, status.MaxBudgetUSD-status.TotalSpentUSD)
		status.PercentUsed = status.TotalSpentUSD / status.MaxBudgetUSD
		status.Warning = status.PercentUsed >= status.WarnThreshold
		status.Exceeded = status.TotalSpentUSD > status.MaxBudgetUSD
	}
	for agent, usage := range m.agents {
		status.ByAgent[agent] = usage
	}
	return status
}

func UsageDeltaFromTokenUsage(usage core.TokenUsage) UsageDelta {
	return UsageDelta{
		PromptTokens:     int64(usage.PromptTokens),
		CompletionTokens: int64(usage.CompletionTokens),
		TotalTokens:      int64(usage.TotalTokens),
		CostUSD:          usage.Cost,
	}
}

func UsageDeltaFromRLMTrace(trace *modrlm.RLMTrace) UsageDelta {
	if trace == nil {
		return UsageDelta{}
	}
	// dspy-go RLMTrace.Usage is the aggregate root+sub+subRLM token count.
	// The component fallback keeps older or partially-populated traces usable.
	delta := UsageDeltaFromTokenUsage(trace.Usage)
	if delta.PromptTokens == 0 && delta.CompletionTokens == 0 && delta.TotalTokens == 0 {
		delta = UsageDelta{
			PromptTokens: int64(trace.RootUsage.PromptTokens + trace.SubUsage.PromptTokens + trace.SubRLMUsage.PromptTokens),
			CompletionTokens: int64(trace.RootUsage.CompletionTokens +
				trace.SubUsage.CompletionTokens + trace.SubRLMUsage.CompletionTokens),
			TotalTokens: int64(trace.RootUsage.TotalTokens + trace.SubUsage.TotalTokens + trace.SubRLMUsage.TotalTokens),
			CostUSD:     trace.RootUsage.Cost + trace.SubUsage.Cost + trace.SubRLMUsage.Cost,
		}
	}
	if delta.CostUSD == 0 {
		delta.CostUSD = trace.RootUsage.Cost + trace.SubUsage.Cost + trace.SubRLMUsage.Cost
	}
	// RLMTrace carries core.TokenUsage, which does not expose cache-read fields.
	// Consumers should treat weighted totals for this source as raw-token totals.
	delta.CacheTokenWeightUnavailable = true
	return normalizeUsageDelta(delta)
}

func UsageDeltaFromExecutionTrace(trace *agents.ExecutionTrace) UsageDelta {
	if trace == nil {
		return UsageDelta{}
	}
	return UsageDeltaFromTokenMap(trace.TokenUsage, trace.ContextMetadata)
}

func UsageDeltaFromTokenMap(usage map[string]int64, metadata map[string]interface{}) UsageDelta {
	if usage == nil {
		usage = map[string]int64{}
	}
	prompt := firstInt64(usage, "prompt_tokens", "input_tokens")
	completion := firstInt64(usage, "completion_tokens", "output_tokens")
	if prompt == 0 {
		prompt = usage["root_prompt_tokens"] + usage["sub_prompt_tokens"] + usage["subrlm_prompt_tokens"]
	}
	if completion == 0 {
		completion = usage["root_completion_tokens"] + usage["sub_completion_tokens"] + usage["subrlm_completion_tokens"]
	}
	total := firstInt64(usage, "total_tokens")
	if total == 0 {
		total = prompt + completion
	}
	return normalizeUsageDelta(UsageDelta{
		PromptTokens:                prompt,
		CompletionTokens:            completion,
		TotalTokens:                 total,
		CacheReadInputTokens:        firstInt64(usage, "cache_read_input_tokens"),
		CacheCreationInputTokens:    firstInt64(usage, "cache_creation_input_tokens"),
		CacheTokenWeightUnavailable: !hasCacheTokenFields(usage),
		CostUSD:                     costFromTraceMetadata(metadata),
	})
}

func SnapshotTokenTracker(tracker *modrlm.TokenTracker) TokenTrackerSnapshot {
	if tracker == nil {
		return TokenTrackerSnapshot{}
	}
	usage := tracker.GetTotalUsage()
	return TokenTrackerSnapshot{
		PromptTokens:     int64(usage.PromptTokens),
		CompletionTokens: int64(usage.CompletionTokens),
		TotalTokens:      int64(usage.TotalTokens),
		CostUSD:          usage.Cost,
	}
}

func (d UsageDelta) Empty() bool {
	return d.PromptTokens == 0 &&
		d.CompletionTokens == 0 &&
		d.TotalTokens == 0 &&
		d.CacheReadInputTokens == 0 &&
		d.CacheCreationInputTokens == 0 &&
		d.CostUSD == 0
}

func normalizeConfig(cfg Config) Config {
	if cfg.WarnThreshold <= 0 || cfg.WarnThreshold > 1 {
		cfg.WarnThreshold = DefaultWarnThreshold
	}
	if cfg.CacheReadInputTokenWeight <= 0 || cfg.CacheReadInputTokenWeight > 1 {
		cfg.CacheReadInputTokenWeight = DefaultCacheReadInputTokenWeight
	}
	if cfg.MaxBudgetUSD < 0 {
		cfg.MaxBudgetUSD = 0
	}
	return cfg
}

func normalizeUsageDelta(delta UsageDelta) UsageDelta {
	delta.PromptTokens = maxInt64(0, delta.PromptTokens)
	delta.CompletionTokens = maxInt64(0, delta.CompletionTokens)
	delta.CacheReadInputTokens = maxInt64(0, delta.CacheReadInputTokens)
	delta.CacheCreationInputTokens = maxInt64(0, delta.CacheCreationInputTokens)
	if delta.TotalTokens <= 0 {
		delta.TotalTokens = delta.PromptTokens + delta.CompletionTokens
	}
	delta.TotalTokens = maxInt64(0, delta.TotalTokens)
	if delta.CostUSD < 0 {
		delta.CostUSD = 0
	}
	return delta
}

func weightedPromptTokens(delta UsageDelta, cacheReadWeight float64) float64 {
	delta = normalizeUsageDelta(delta)
	cacheRead := minInt64(delta.PromptTokens, delta.CacheReadInputTokens)
	regularPrompt := delta.PromptTokens - cacheRead
	return float64(regularPrompt) + float64(cacheRead)*cacheReadWeight
}

func firstInt64(values map[string]int64, keys ...string) int64 {
	for _, key := range keys {
		if value := values[key]; value != 0 {
			return value
		}
	}
	return 0
}

func hasCacheTokenFields(values map[string]int64) bool {
	if values == nil {
		return false
	}
	_, hasRead := values["cache_read_input_tokens"]
	_, hasCreation := values["cache_creation_input_tokens"]
	return hasRead || hasCreation
}

func costFromTraceMetadata(metadata map[string]interface{}) float64 {
	for _, key := range []string{"cost_usd", "cost", "total_cost_usd"} {
		if value, ok := metadata[key]; ok {
			return floatFromValue(value)
		}
	}
	return 0
}

func floatFromValue(value interface{}) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	case jsonNumber:
		result, _ := typed.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return result
	default:
		return 0
	}
}

type jsonNumber interface {
	Float64() (float64, error)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func (s BudgetStatus) String() string {
	if s.MaxBudgetUSD > 0 {
		return fmt.Sprintf("$%.4f spent (%.1f%%), %d raw tokens, %.0f weighted tokens", s.TotalSpentUSD, s.PercentUsed*100, s.TotalTokens, s.WeightedTotalTokens)
	}
	return fmt.Sprintf("$%.4f spent, %d raw tokens, %.0f weighted tokens", s.TotalSpentUSD, s.TotalTokens, s.WeightedTotalTokens)
}

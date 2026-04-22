package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

const (
	defaultEvolutionMaxRuntime          = 30 * time.Minute
	defaultEvolutionRetentionRuns       = 20
	defaultEvolutionRetentionDays       = 30
	defaultEvolutionFailureCutoff       = 3
	defaultEvolutionValidationSplit     = 0.25
	defaultEvolutionRegressionTolerance = 0.015
	defaultProtectedRegressionTolerance = 0.0
)

type EvolveReviewConfig struct {
	Logger                       *logging.Logger
	StateDir                     string
	SuitePaths                   []string
	SearchSuitePaths             []string
	ProtectedSuitePaths          []string
	InboxDir                     string
	ArchiveDir                   string
	BaseReviewArtifactsPath      string
	SkillStorePath               string
	SkillDomain                  string
	StudentLLM                   core.LLM
	TeacherLLM                   core.LLM
	StudentModelID               core.ModelID
	TeacherModelID               core.ModelID
	MinNewExamples               int
	MaxRuntime                   time.Duration
	RetentionRuns                int
	RetentionDays                int
	FailureThreshold             int
	ValidationSplit              float64
	MaxCasesPerRun               int
	MaxSearchCasesPerSuite       int
	MaxChunksPerCase             int
	TeacherTracePath             string
	TeacherDemoCount             int
	OptimizeDemos                bool
	DryRun                       bool
	PopulationSize               int
	MaxGenerations               int
	ReflectionFreq               int
	EvalConcurrency              int
	SearchBatchSize              int
	StagnationLimit              int
	ValidationFrequency          int
	MaxMetricCalls               int
	ScoreThreshold               float64
	PassThreshold                float64
	AcceptedCaseWeight           float64
	MatchedScoreFloor            float64
	RegressionTolerance          float64
	ProtectedRegressionTolerance float64
}

type EvolveReviewResult struct {
	RunID                 string
	Decision              string
	Promoted              bool
	CircuitOpen           bool
	NewExampleCount       int
	BaselineValidation    float64
	ReplayValidation      float64
	CurrentArtifactPath   string
	CandidateArtifactPath string
	ReportPath            string
}

type evolutionState struct {
	UpdatedAt           time.Time `json:"updated_at"`
	LastRunID           string    `json:"last_run_id,omitempty"`
	LastPromotedRunID   string    `json:"last_promoted_run_id,omitempty"`
	LastDecision        string    `json:"last_decision,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
}

type evolutionCircuit struct {
	OpenedAt            time.Time `json:"opened_at"`
	Reason              string    `json:"reason"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
}

type evolutionCurrentManifest struct {
	RunID               string    `json:"run_id"`
	PromotedAt          time.Time `json:"promoted_at"`
	StudentModelID      string    `json:"student_model_id,omitempty"`
	TeacherModelID      string    `json:"teacher_model_id,omitempty"`
	BaselineValidation  float64   `json:"baseline_validation"`
	ReplayValidation    float64   `json:"replay_validation"`
	CandidateArtifact   string    `json:"candidate_artifact"`
	CurrentArtifactHash string    `json:"current_artifact_hash"`
	PublishedVersion    int       `json:"published_version,omitempty"`
}

type evolutionRunReport struct {
	RunID                        string                           `json:"run_id"`
	StartedAt                    time.Time                        `json:"started_at"`
	FinishedAt                   time.Time                        `json:"finished_at,omitempty"`
	Decision                     string                           `json:"decision"`
	FailureReason                string                           `json:"failure_reason,omitempty"`
	Promoted                     bool                             `json:"promoted"`
	CircuitOpen                  bool                             `json:"circuit_open,omitempty"`
	NewExampleCount              int                              `json:"new_example_count"`
	InboxFileCount               int                              `json:"inbox_file_count"`
	CandidateArtifactPath        string                           `json:"candidate_artifact_path,omitempty"`
	CurrentArtifactPath          string                           `json:"current_artifact_path,omitempty"`
	CurrentArtifactHash          string                           `json:"current_artifact_hash,omitempty"`
	CandidateArtifactHash        string                           `json:"candidate_artifact_hash,omitempty"`
	StudentModelID               string                           `json:"student_model_id,omitempty"`
	TeacherModelID               string                           `json:"teacher_model_id,omitempty"`
	SeedSkillVersion             int                              `json:"seed_skill_version"`
	PublishedVersion             int                              `json:"published_version,omitempty"`
	BaselineValidation           float64                          `json:"baseline_validation_score"`
	ReplayValidation             float64                          `json:"replay_validation_score"`
	GateBaselineScore            float64                          `json:"gate_baseline_score,omitempty"`
	GateReplayScore              float64                          `json:"gate_replay_score,omitempty"`
	RegressionTolerance          float64                          `json:"regression_tolerance,omitempty"`
	ProtectedRegressionTolerance float64                          `json:"protected_regression_tolerance,omitempty"`
	ValidationBySuite            map[string]evolutionSuiteMetrics `json:"validation_by_suite,omitempty"`
	ProtectedBySuite             map[string]evolutionSuiteMetrics `json:"protected_by_suite,omitempty"`
	CaseReports                  map[string]evolutionCaseReports  `json:"case_reports,omitempty"`
	ProtectedCaseReports         map[string]evolutionCaseReports  `json:"protected_case_reports,omitempty"`
	BestCandidateID              string                           `json:"best_candidate_id,omitempty"`
}

type evolutionSuiteMetrics struct {
	TrainingExampleCount   int     `json:"training_example_count"`
	ValidationExampleCount int     `json:"validation_example_count"`
	BaselineValidation     float64 `json:"baseline_validation_score"`
	ReplayValidation       float64 `json:"replay_validation_score"`
}

type evolutionCaseReports struct {
	Baseline []EvolutionValidationCaseReport `json:"baseline,omitempty"`
	Replay   []EvolutionValidationCaseReport `json:"replay,omitempty"`
}

type EvolutionValidationCaseReport struct {
	ID             string  `json:"id"`
	Label          string  `json:"label,omitempty"`
	FilePath       string  `json:"file_path,omitempty"`
	Line           int     `json:"line,omitempty"`
	Score          float64 `json:"score"`
	RawScore       float64 `json:"raw_score,omitempty"`
	CaseWeight     float64 `json:"case_weight,omitempty"`
	CommentCount   int     `json:"comment_count,omitempty"`
	Matched        bool    `json:"matched,omitempty"`
	MatchedComment string  `json:"matched_comment,omitempty"`
}

type evolutionPaths struct {
	stateDir            string
	currentDir          string
	currentArtifactPath string
	currentManifestPath string
	previousDir         string
	candidatesDir       string
	runsDir             string
	inboxDir            string
	archiveDir          string
	logsDir             string
	lockDir             string
	lockPath            string
	statePath           string
	circuitPath         string
}

type evolutionLock struct {
	path string
}

func (l *evolutionLock) Release() {
	if l == nil || l.path == "" {
		return
	}
	_ = os.RemoveAll(l.path)
}

func RunEvolveReview(ctx context.Context, cfg EvolveReviewConfig) (*EvolveReviewResult, error) {
	cfg = normalizeEvolveReviewConfig(cfg)
	if err := validateEvolveReviewConfig(cfg); err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = logging.GetLogger()
	}

	paths, err := evolutionPathsForStateDir(cfg.StateDir, cfg.InboxDir, cfg.ArchiveDir)
	if err != nil {
		return nil, err
	}
	if err := ensureEvolutionBaseDirs(paths); err != nil {
		return nil, err
	}

	if circuitOpen, _, err := loadEvolutionCircuit(paths.circuitPath); err != nil {
		return nil, err
	} else if circuitOpen {
		return &EvolveReviewResult{
			Decision:            "skip_circuit_open",
			CircuitOpen:         true,
			CurrentArtifactPath: paths.currentArtifactPath,
		}, nil
	}

	inboxCases, inboxFiles, err := loadInboxReviewCases(paths.inboxDir)
	if err != nil {
		return nil, err
	}
	newExamples := ReviewBenchmarkExamples(inboxCases)
	if len(newExamples) < cfg.MinNewExamples {
		return &EvolveReviewResult{
			Decision:            "skip_min_new_examples",
			NewExampleCount:     len(newExamples),
			CurrentArtifactPath: paths.currentArtifactPath,
		}, nil
	}

	lock, err := acquireEvolutionLock(paths.lockPath)
	if err != nil {
		if os.IsExist(err) {
			return &EvolveReviewResult{
				Decision:            "skip_locked",
				NewExampleCount:     len(newExamples),
				CurrentArtifactPath: paths.currentArtifactPath,
			}, nil
		}
		return nil, err
	}
	defer lock.Release()

	runCtx, cancel := context.WithTimeout(ctx, cfg.MaxRuntime)
	defer cancel()

	runID := time.Now().UTC().Format("20060102-150405")
	runDir := filepath.Join(paths.runsDir, runID)
	candidateDir := filepath.Join(paths.candidatesDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, fmt.Errorf("create run directory: %w", err)
	}
	if err := os.MkdirAll(candidateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create candidate directory: %w", err)
	}

	report := evolutionRunReport{
		RunID:               runID,
		StartedAt:           time.Now().UTC(),
		Decision:            "failed_runtime",
		NewExampleCount:     len(newExamples),
		InboxFileCount:      len(inboxFiles),
		CurrentArtifactPath: paths.currentArtifactPath,
		StudentModelID:      string(cfg.StudentModelID),
		TeacherModelID:      string(cfg.TeacherModelID),
	}
	reportPath := filepath.Join(runDir, "report.json")

	result := &EvolveReviewResult{
		RunID:               runID,
		Decision:            report.Decision,
		NewExampleCount:     len(newExamples),
		CurrentArtifactPath: paths.currentArtifactPath,
		ReportPath:          reportPath,
	}

	state, err := loadEvolutionState(paths.statePath)
	if err != nil {
		return nil, err
	}

	finishRun := func(promoted bool, failure bool) error {
		report.FinishedAt = time.Now().UTC()
		report.Promoted = promoted
		result.Decision = report.Decision
		result.Promoted = promoted
		result.CircuitOpen = report.CircuitOpen
		result.BaselineValidation = report.BaselineValidation
		result.ReplayValidation = report.ReplayValidation
		result.CandidateArtifactPath = report.CandidateArtifactPath

		switch {
		case promoted:
			state.ConsecutiveFailures = 0
			state.LastPromotedRunID = runID
		case failure:
			state.ConsecutiveFailures++
		}
		state.UpdatedAt = time.Now().UTC()
		state.LastRunID = runID
		state.LastDecision = report.Decision

		if state.ConsecutiveFailures >= cfg.FailureThreshold {
			report.CircuitOpen = true
			result.CircuitOpen = true
			if err := writeJSONFile(paths.circuitPath, evolutionCircuit{
				OpenedAt:            time.Now().UTC(),
				Reason:              report.Decision,
				ConsecutiveFailures: state.ConsecutiveFailures,
			}); err != nil {
				return err
			}
		}
		if !report.CircuitOpen {
			_ = os.Remove(paths.circuitPath)
		}
		if err := writeJSONFile(reportPath, report); err != nil {
			return err
		}
		if err := writeJSONFile(paths.statePath, state); err != nil {
			return err
		}
		pruneRetention(paths.runsDir, cfg.RetentionRuns, cfg.RetentionDays)
		pruneRetention(paths.candidatesDir, cfg.RetentionRuns, cfg.RetentionDays)
		return nil
	}

	if err := writeJSONFile(filepath.Join(runDir, "inbox_cases.json"), inboxCases); err != nil {
		return nil, err
	}

	replaySuitePaths := replaySuitePathsForConfig(cfg)
	baseSuites, _, _, err := loadBenchmarkSuitesIfPresent(replaySuitePaths, cfg.ValidationSplit, cfg.MaxCasesPerRun)
	if err != nil {
		return nil, err
	}
	inboxSuite, _, _, err := buildInboxBenchmarkSuite(paths.inboxDir, cfg.ValidationSplit, cfg.MaxCasesPerRun)
	if err != nil {
		return nil, err
	}
	if inboxSuite != nil {
		baseSuites = append(baseSuites, *inboxSuite)
	}

	searchSuitePaths := searchSuitePathsForConfig(cfg)
	searchCaseCap := searchCaseCapForConfig(cfg)
	searchSuites, trainingExamples, validationExamples, err := loadBenchmarkSuitesIfPresent(searchSuitePaths, cfg.ValidationSplit, searchCaseCap)
	if err != nil {
		return nil, err
	}
	inboxSearchSuite, inboxTraining, inboxValidation, err := buildInboxBenchmarkSuite(paths.inboxDir, cfg.ValidationSplit, searchCaseCap)
	if err != nil {
		return nil, err
	}
	if inboxSearchSuite != nil {
		searchSuites = append(searchSuites, *inboxSearchSuite)
		trainingExamples = append(trainingExamples, inboxTraining...)
		validationExamples = append(validationExamples, inboxValidation...)
	}
	if len(trainingExamples) == 0 || len(validationExamples) == 0 {
		report.Decision = "failed_insufficient_examples"
		report.FailureReason = "at least one training and validation example is required after corpus split"
		if err := finishRun(false, true); err != nil {
			return nil, err
		}
		return result, nil
	}

	protectedSuites, err := loadProtectedSuites(cfg, baseSuites, inboxSuite)
	if err != nil {
		return nil, err
	}

	seedArtifacts, currentSkill, seedSkillVersion, err := loadEvolutionSeedArtifacts(runCtx, cfg, paths.currentArtifactPath)
	if err != nil {
		return nil, err
	}
	report.SeedSkillVersion = seedSkillVersion

	if strings.TrimSpace(cfg.TeacherTracePath) != "" {
		traceReport, err := LoadReviewTeacherTraceReport(cfg.TeacherTracePath)
		if err != nil {
			return nil, fmt.Errorf("load teacher traces: %w", err)
		}
		if demos := SelectTopTeacherDemos(traceReport.Traces, cfg.TeacherDemoCount); demos != "" {
			if seedArtifacts.Text == nil {
				seedArtifacts.Text = make(map[optimize.ArtifactKey]string)
			}
			seedArtifacts.Text[ArtifactFewShotDemos] = demos
		}
	}

	evaluatorCfg := DefaultReviewBenchmarkEvaluatorConfig()
	evaluatorCfg.AcceptedCaseWeight = cfg.AcceptedCaseWeight
	evaluatorCfg.MatchedScoreFloor = cfg.MatchedScoreFloor
	evaluator := NewReviewBenchmarkEvaluator(evaluatorCfg)

	baselineBySuite, baselineScore, baselineCaseReports, err := evaluateBenchmarkSuites(runCtx, evaluator, NewReviewBenchmarkAgent(cfg.StudentLLM, logger, seedArtifacts, cfg.MaxChunksPerCase), baseSuites, cfg.EvalConcurrency)
	if err != nil {
		report.Decision = "failed_baseline"
		report.FailureReason = err.Error()
		if err := finishRun(false, true); err != nil {
			return nil, err
		}
		return result, nil
	}
	report.BaselineValidation = baselineScore

	artifactKeys := []optimize.ArtifactKey{optimize.ArtifactSkillPack}
	if cfg.OptimizeDemos && strings.TrimSpace(seedArtifacts.Text[ArtifactFewShotDemos]) != "" {
		artifactKeys = append(artifactKeys, ArtifactFewShotDemos)
	}

	candidateArtifactPath := filepath.Join(candidateDir, "optimized_program.json")
	report.CandidateArtifactPath = candidateArtifactPath
	result.CandidateArtifactPath = candidateArtifactPath

	workflow, err := optimize.RunGEPAWorkflow(runCtx, NewReviewBenchmarkAgent(cfg.StudentLLM, logger, seedArtifacts, cfg.MaxChunksPerCase), optimize.GEPAWorkflowRequest{
		Evaluator:          evaluator,
		TrainingExamples:   trainingExamples,
		ValidationExamples: validationExamples,
		BaselineExamples:   validationExamples,
		ReplayExamples:     validationExamples,
		PassThreshold:      cfg.PassThreshold,
		ApplyBest:          false,
		ArtifactPath:       candidateArtifactPath,
		Config: optimize.GEPAAdapterConfig{
			PopulationSize:      cfg.PopulationSize,
			MaxGenerations:      cfg.MaxGenerations,
			ReflectionFreq:      cfg.ReflectionFreq,
			SearchBatchSize:     cfg.SearchBatchSize,
			StagnationLimit:     cfg.StagnationLimit,
			ValidationSplit:     0,
			ValidationFrequency: cfg.ValidationFrequency,
			EvalConcurrency:     cfg.EvalConcurrency,
			PassThreshold:       cfg.PassThreshold,
			ArtifactKeys:        artifactKeys,
			PrimaryArtifact:     optimize.ArtifactSkillPack,
			MaxMetricCalls:      cfg.MaxMetricCalls,
			ScoreThreshold:      cfg.ScoreThreshold,
			MaxRuntime:          cfg.MaxRuntime,
		},
	})
	if err != nil {
		report.Decision = classifyEvolutionFailure(runCtx, "failed_optimize")
		report.FailureReason = err.Error()
		if err := finishRun(false, true); err != nil {
			return nil, err
		}
		return result, nil
	}
	if workflow.Optimization != nil && workflow.Optimization.BestCandidate != nil {
		report.BestCandidateID = workflow.Optimization.BestCandidate.ID
	}

	bestArtifacts := seedArtifacts.Clone()
	if workflow.Optimization != nil {
		bestArtifacts = workflow.Optimization.BestArtifacts.Clone()
	}

	replayBySuite, replayScore, replayCaseReports, err := evaluateBenchmarkSuites(runCtx, evaluator, NewReviewBenchmarkAgent(cfg.StudentLLM, logger, bestArtifacts, cfg.MaxChunksPerCase), baseSuites, cfg.EvalConcurrency)
	if err != nil {
		report.Decision = classifyEvolutionFailure(runCtx, "failed_replay")
		report.FailureReason = err.Error()
		if err := finishRun(false, true); err != nil {
			return nil, err
		}
		return result, nil
	}
	report.ReplayValidation = replayScore

	report.ValidationBySuite = buildEvolutionSuiteMetrics(baseSuites, baselineBySuite, replayBySuite)
	report.CaseReports = buildEvolutionCaseReports(baseSuites, baselineCaseReports, replayCaseReports)

	currentHash, _ := fileSHA256(paths.currentArtifactPath)
	candidateHash, err := fileSHA256(candidateArtifactPath)
	if err != nil {
		report.Decision = "failed_candidate_hash"
		report.FailureReason = err.Error()
		if err := finishRun(false, true); err != nil {
			return nil, err
		}
		return result, nil
	}
	report.CurrentArtifactHash = currentHash
	report.CandidateArtifactHash = candidateHash

	program, err := optimize.ReadOptimizedAgentProgram(candidateArtifactPath)
	if err != nil {
		report.Decision = "failed_candidate_read"
		report.FailureReason = err.Error()
		if err := finishRun(false, true); err != nil {
			return nil, err
		}
		return result, nil
	}
	if err := program.Validate(); err != nil {
		report.Decision = "failed_candidate_validate"
		report.FailureReason = err.Error()
		if err := finishRun(false, true); err != nil {
			return nil, err
		}
		return result, nil
	}

	replayRunScore := replayScore
	baselineRunScore := baselineScore
	report.GateBaselineScore = baselineRunScore
	report.GateReplayScore = replayRunScore
	report.RegressionTolerance = cfg.RegressionTolerance
	report.ProtectedRegressionTolerance = cfg.ProtectedRegressionTolerance

	switch {
	case replayRegressedBeyondTolerance(baselineRunScore, replayRunScore, cfg.RegressionTolerance):
		report.Decision = "failed_replay_regression"
		report.FailureReason = fmt.Sprintf("replay score %.6f regressed below baseline %.6f beyond tolerance %.6f", replayRunScore, baselineRunScore, cfg.RegressionTolerance)
		if err := finishRun(false, true); err != nil {
			return nil, err
		}
		return result, nil
	case cfg.RegressionTolerance <= 0 && replayScore <= baselineScore+1e-9:
		report.Decision = "skip_no_improvement"
		if err := archiveInboxFiles(paths.archiveDir, runID, inboxFiles); err != nil {
			return nil, err
		}
		if err := finishRun(false, false); err != nil {
			return nil, err
		}
		return result, nil
	case currentHash != "" && currentHash == candidateHash:
		report.Decision = "skip_unchanged"
		if err := archiveInboxFiles(paths.archiveDir, runID, inboxFiles); err != nil {
			return nil, err
		}
		if err := finishRun(false, false); err != nil {
			return nil, err
		}
		return result, nil
	case cfg.DryRun:
		report.Decision = "skip_dry_run"
		if err := archiveInboxFiles(paths.archiveDir, runID, inboxFiles); err != nil {
			return nil, err
		}
		if err := finishRun(false, false); err != nil {
			return nil, err
		}
		return result, nil
	}

	protectedBaseline := make(map[string]float64)
	protectedBaselineReports := make(map[string][]EvolutionValidationCaseReport)
	protectedReplay := make(map[string]float64)
	protectedReplayReports := make(map[string][]EvolutionValidationCaseReport)
	if len(protectedSuites) > 0 {
		protectedBaseline, _, protectedBaselineReports, err = evaluateBenchmarkSuites(runCtx, evaluator, NewReviewBenchmarkAgent(cfg.StudentLLM, logger, seedArtifacts, cfg.MaxChunksPerCase), protectedSuites, cfg.EvalConcurrency)
		if err != nil {
			report.Decision = classifyEvolutionFailure(runCtx, "failed_protected_baseline")
			report.FailureReason = err.Error()
			if err := finishRun(false, true); err != nil {
				return nil, err
			}
			return result, nil
		}
		protectedReplay, _, protectedReplayReports, err = evaluateBenchmarkSuites(runCtx, evaluator, NewReviewBenchmarkAgent(cfg.StudentLLM, logger, bestArtifacts, cfg.MaxChunksPerCase), protectedSuites, cfg.EvalConcurrency)
		if err != nil {
			report.Decision = classifyEvolutionFailure(runCtx, "failed_protected_replay")
			report.FailureReason = err.Error()
			if err := finishRun(false, true); err != nil {
				return nil, err
			}
			return result, nil
		}
		report.ProtectedBySuite = buildEvolutionSuiteMetrics(protectedSuites, protectedBaseline, protectedReplay)
		report.ProtectedCaseReports = buildEvolutionCaseReports(protectedSuites, protectedBaselineReports, protectedReplayReports)
		if protectedSuitesRegressed(protectedBaseline, protectedReplay, cfg.ProtectedRegressionTolerance) {
			report.Decision = "failed_protected_regression"
			report.FailureReason = fmt.Sprintf("one or more protected suites regressed beyond tolerance %.6f", cfg.ProtectedRegressionTolerance)
			if err := finishRun(false, true); err != nil {
				return nil, err
			}
			return result, nil
		}
	}

	bestOverlay := strings.TrimSpace(bestArtifacts.Text[optimize.ArtifactSkillPack])
	publishedVersion, err := promoteEvolutionCandidate(runCtx, cfg, paths, runID, candidateArtifactPath, bestOverlay, currentSkill, seedSkillVersion, candidateHash, report)
	if err != nil {
		report.Decision = "failed_promote"
		report.FailureReason = err.Error()
		if err := finishRun(false, true); err != nil {
			return nil, err
		}
		return result, nil
	}
	report.PublishedVersion = publishedVersion
	report.Decision = "promoted"

	if err := archiveInboxFiles(paths.archiveDir, runID, inboxFiles); err != nil {
		return nil, err
	}
	if err := finishRun(true, false); err != nil {
		return nil, err
	}
	return result, nil
}

func ResetEvolveReviewCircuit(stateDir string) error {
	paths, err := evolutionPathsForStateDir(stateDir, "", "")
	if err != nil {
		return err
	}
	state, err := loadEvolutionState(paths.statePath)
	if err != nil {
		return err
	}
	state.ConsecutiveFailures = 0
	state.UpdatedAt = time.Now().UTC()
	if err := os.Remove(paths.circuitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return writeJSONFile(paths.statePath, state)
}

func normalizeEvolveReviewConfig(cfg EvolveReviewConfig) EvolveReviewConfig {
	if cfg.MaxRuntime <= 0 {
		cfg.MaxRuntime = defaultEvolutionMaxRuntime
	}
	if cfg.RetentionRuns <= 0 {
		cfg.RetentionRuns = defaultEvolutionRetentionRuns
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = defaultEvolutionRetentionDays
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = defaultEvolutionFailureCutoff
	}
	if cfg.ValidationSplit <= 0 || cfg.ValidationSplit >= 1 {
		cfg.ValidationSplit = defaultEvolutionValidationSplit
	}
	if cfg.RegressionTolerance < 0 {
		cfg.RegressionTolerance = defaultEvolutionRegressionTolerance
	}
	if cfg.ProtectedRegressionTolerance < 0 {
		cfg.ProtectedRegressionTolerance = defaultProtectedRegressionTolerance
	}
	if cfg.MinNewExamples < 0 {
		cfg.MinNewExamples = 0
	}
	if cfg.PassThreshold <= 0 || cfg.PassThreshold > 1 {
		cfg.PassThreshold = 0.75
	}
	defaults := DefaultReviewBenchmarkEvaluatorConfig()
	if cfg.AcceptedCaseWeight <= 0 {
		cfg.AcceptedCaseWeight = defaults.AcceptedCaseWeight
	}
	if cfg.MatchedScoreFloor < 0 || cfg.MatchedScoreFloor > 1 {
		cfg.MatchedScoreFloor = defaults.MatchedScoreFloor
	}
	return cfg
}

func replayRegressedBeyondTolerance(baseline, replay, tolerance float64) bool {
	if tolerance < 0 {
		tolerance = 0
	}
	return replay < baseline-tolerance
}

func validateEvolveReviewConfig(cfg EvolveReviewConfig) error {
	if strings.TrimSpace(cfg.StateDir) == "" {
		return fmt.Errorf("review evolution state dir is required")
	}
	if cfg.StudentLLM == nil {
		return fmt.Errorf("student LLM is required")
	}
	if cfg.TeacherLLM == nil {
		return fmt.Errorf("teacher LLM is required")
	}
	if cfg.PopulationSize <= 0 || cfg.MaxGenerations <= 0 {
		return fmt.Errorf("population and generations must be positive")
	}
	if cfg.EvalConcurrency <= 0 {
		return fmt.Errorf("eval concurrency must be positive")
	}
	return nil
}

func evolutionPathsForStateDir(stateDir, inboxDir, archiveDir string) (evolutionPaths, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return evolutionPaths{}, fmt.Errorf("state dir is required")
	}
	paths := evolutionPaths{
		stateDir:            filepath.Clean(stateDir),
		currentDir:          filepath.Join(stateDir, "current"),
		currentArtifactPath: filepath.Join(stateDir, "current", "optimized_program.json"),
		currentManifestPath: filepath.Join(stateDir, "current", "manifest.json"),
		previousDir:         filepath.Join(stateDir, "current", "previous"),
		candidatesDir:       filepath.Join(stateDir, "candidates"),
		runsDir:             filepath.Join(stateDir, "runs"),
		logsDir:             filepath.Join(stateDir, "logs"),
		lockDir:             filepath.Join(stateDir, "lock"),
		lockPath:            filepath.Join(stateDir, "lock", "run.lock"),
		statePath:           filepath.Join(stateDir, "state.json"),
		circuitPath:         filepath.Join(stateDir, "circuit_open"),
	}
	if strings.TrimSpace(inboxDir) == "" {
		paths.inboxDir = filepath.Join(stateDir, "datasets", "inbox")
	} else {
		paths.inboxDir = filepath.Clean(inboxDir)
	}
	if strings.TrimSpace(archiveDir) == "" {
		paths.archiveDir = filepath.Join(stateDir, "datasets", "archive")
	} else {
		paths.archiveDir = filepath.Clean(archiveDir)
	}
	return paths, nil
}

func ensureEvolutionBaseDirs(paths evolutionPaths) error {
	for _, dir := range []string{
		paths.stateDir,
		paths.currentDir,
		paths.previousDir,
		paths.candidatesDir,
		paths.runsDir,
		paths.inboxDir,
		paths.archiveDir,
		paths.logsDir,
		paths.lockDir,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

func acquireEvolutionLock(path string) (*evolutionLock, error) {
	if err := os.Mkdir(path, 0o755); err != nil {
		return nil, err
	}
	return &evolutionLock{path: path}, nil
}

func loadInboxReviewCases(dir string) ([]ReviewBenchmarkCase, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var cases []ReviewBenchmarkCase
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		fileCases, err := loadReviewCasesFromFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("load inbox review cases from %q: %w", path, err)
		}
		if len(fileCases) == 0 {
			continue
		}
		cases = append(cases, fileCases...)
		files = append(files, path)
	}
	return cases, files, nil
}

func loadReviewCasesFromFile(path string) ([]ReviewBenchmarkCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var suite ReviewBenchmarkSuite
	if err := json.Unmarshal(data, &suite); err == nil && len(suite.Cases) > 0 {
		return suite.Cases, nil
	}
	var cases []ReviewBenchmarkCase
	if err := json.Unmarshal(data, &cases); err == nil && len(cases) > 0 {
		return cases, nil
	}
	var single ReviewBenchmarkCase
	if err := json.Unmarshal(data, &single); err == nil && strings.TrimSpace(single.FilePath) != "" {
		return []ReviewBenchmarkCase{single}, nil
	}
	return nil, fmt.Errorf("unsupported review benchmark payload")
}

func replaySuitePathsForConfig(cfg EvolveReviewConfig) []string {
	if len(cfg.SuitePaths) > 0 {
		return append([]string(nil), cfg.SuitePaths...)
	}
	if len(cfg.SearchSuitePaths) > 0 {
		return append([]string(nil), cfg.SearchSuitePaths...)
	}
	return nil
}

func searchSuitePathsForConfig(cfg EvolveReviewConfig) []string {
	if len(cfg.SearchSuitePaths) > 0 {
		return append([]string(nil), cfg.SearchSuitePaths...)
	}
	return replaySuitePathsForConfig(cfg)
}

func searchCaseCapForConfig(cfg EvolveReviewConfig) int {
	if cfg.MaxSearchCasesPerSuite > 0 {
		return cfg.MaxSearchCasesPerSuite
	}
	return cfg.MaxCasesPerRun
}

func buildInboxBenchmarkSuite(inboxDir string, validationSplit float64, maxCasesPerRun int) (*BenchmarkSuite, []optimize.AgentExample, []optimize.AgentExample, error) {
	cases, _, err := loadInboxReviewCases(inboxDir)
	if err != nil {
		return nil, nil, nil, err
	}
	if maxCasesPerRun > 0 && len(cases) > maxCasesPerRun {
		cases = append([]ReviewBenchmarkCase(nil), cases[:maxCasesPerRun]...)
	}
	examples := ReviewBenchmarkExamples(cases)
	if len(examples) == 0 {
		return nil, nil, nil, nil
	}
	training, validation, err := SplitBenchmarkExamples(examples, validationSplit)
	if err != nil {
		return nil, nil, nil, err
	}
	suite := &BenchmarkSuite{
		Path:               "inbox:" + inboxDir,
		TrainingExamples:   training,
		ValidationExamples: validation,
	}
	return suite, training, validation, nil
}

func loadBenchmarkSuitesIfPresent(paths []string, validationSplit float64, maxCasesPerRun int) ([]BenchmarkSuite, []optimize.AgentExample, []optimize.AgentExample, error) {
	if len(paths) == 0 {
		return nil, nil, nil, nil
	}
	return LoadBenchmarkSuites(paths, validationSplit, maxCasesPerRun)
}

func loadProtectedSuites(cfg EvolveReviewConfig, baseSuites []BenchmarkSuite, inboxSuite *BenchmarkSuite) ([]BenchmarkSuite, error) {
	if len(cfg.ProtectedSuitePaths) > 0 {
		protected, _, _, err := LoadBenchmarkSuites(cfg.ProtectedSuitePaths, cfg.ValidationSplit, cfg.MaxCasesPerRun)
		if err != nil {
			return nil, err
		}
		return protected, nil
	}
	if len(baseSuites) > 0 {
		return append([]BenchmarkSuite(nil), baseSuites...), nil
	}
	if inboxSuite != nil {
		return []BenchmarkSuite{*inboxSuite}, nil
	}
	return nil, nil
}

func loadEvolutionSeedArtifacts(ctx context.Context, cfg EvolveReviewConfig, currentArtifactPath string) (optimize.AgentArtifacts, *skills.Skill, int, error) {
	baseArtifacts := defaultReviewArtifacts()
	var err error
	if _, statErr := os.Stat(currentArtifactPath); statErr == nil {
		baseArtifacts, err = LoadConfiguredReviewArtifacts(currentArtifactPath)
		if err != nil {
			return optimize.AgentArtifacts{}, nil, 0, err
		}
	} else if !os.IsNotExist(statErr) {
		return optimize.AgentArtifacts{}, nil, 0, statErr
	} else if strings.TrimSpace(cfg.BaseReviewArtifactsPath) != "" {
		baseArtifacts, err = LoadConfiguredReviewArtifacts(cfg.BaseReviewArtifactsPath)
		if err != nil {
			return optimize.AgentArtifacts{}, nil, 0, err
		}
	}

	storePath, err := ResolveReviewSkillStorePath(cfg.SkillStorePath, cfg.StateDir)
	if err != nil {
		return optimize.AgentArtifacts{}, nil, 0, err
	}
	domain := ResolveReviewSkillDomain(cfg.SkillDomain)
	store := skills.NewFileStore(storePath)
	bestSkill, err := store.Best(ctx, domain)
	if err != nil {
		return optimize.AgentArtifacts{}, nil, 0, err
	}

	seedArtifacts := baseArtifacts.Clone()
	if seedArtifacts.Text == nil {
		seedArtifacts.Text = make(map[optimize.ArtifactKey]string)
	}
	seedSkillVersion := 0
	if bestSkill != nil && strings.TrimSpace(bestSkill.Content) != "" {
		seedArtifacts.Text[optimize.ArtifactSkillPack] = strings.TrimSpace(bestSkill.Content)
		seedSkillVersion = bestSkill.Version
	}
	seedArtifacts = EnsureReviewOptimizationSeedArtifacts(seedArtifacts)
	return seedArtifacts, bestSkill, seedSkillVersion, nil
}

func evaluateBenchmarkSuites(ctx context.Context, evaluator optimize.AgentEvaluator, agent optimize.OptimizableAgent, suites []BenchmarkSuite, concurrency int) (map[string]float64, float64, map[string][]EvolutionValidationCaseReport, error) {
	if len(suites) == 0 {
		return nil, 0, nil, nil
	}
	scores := make(map[string]float64, len(suites))
	caseReports := make(map[string][]EvolutionValidationCaseReport, len(suites))
	var totalScore float64
	var totalExamples int
	for _, suite := range suites {
		score, reports, err := evaluateExamples(ctx, evaluator, agent, suite.ValidationExamples, concurrency)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("evaluate suite %q: %w", suite.Path, err)
		}
		scores[suite.Path] = score
		caseReports[suite.Path] = reports
		totalScore += score * float64(len(suite.ValidationExamples))
		totalExamples += len(suite.ValidationExamples)
	}
	if totalExamples == 0 {
		return scores, 0, caseReports, nil
	}
	return scores, totalScore / float64(totalExamples), caseReports, nil
}

func evaluateExamples(ctx context.Context, evaluator optimize.AgentEvaluator, agent optimize.OptimizableAgent, examples []optimize.AgentExample, concurrency int) (float64, []EvolutionValidationCaseReport, error) {
	if len(examples) == 0 {
		return 0, nil, nil
	}
	if concurrency <= 1 {
		var total float64
		reports := make([]EvolutionValidationCaseReport, 0, len(examples))
		for _, example := range examples {
			result, err := evaluator.Evaluate(ctx, agent, example)
			if err != nil {
				return 0, nil, err
			}
			total += result.Score
			reports = append(reports, evolutionValidationCaseReportFromEvalResult(example, result))
		}
		return total / float64(len(examples)), reports, nil
	}

	type evalResult struct {
		score  float64
		report EvolutionValidationCaseReport
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, concurrency)
	results := make([]evalResult, len(examples))
	errCh := make(chan error, 1)
	var wg sync.WaitGroup

	for i, example := range examples {
		i, example := i, example
		wg.Add(1)
		go func() {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			evalAgent, err := agent.Clone()
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				cancel()
				return
			}

			result, err := evaluator.Evaluate(ctx, evalAgent, example)
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				cancel()
				return
			}

			results[i] = evalResult{
				score:  result.Score,
				report: evolutionValidationCaseReportFromEvalResult(example, result),
			}
		}()
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return 0, nil, err
	default:
	}

	var total float64
	reports := make([]EvolutionValidationCaseReport, 0, len(results))
	for _, result := range results {
		total += result.score
		reports = append(reports, result.report)
	}
	return total / float64(len(examples)), reports, nil
}

func evolutionValidationCaseReportFromEvalResult(example optimize.AgentExample, result *optimize.EvalResult) EvolutionValidationCaseReport {
	report := EvolutionValidationCaseReport{
		ID:    example.ID,
		Label: BenchmarkExampleLabel(example),
	}
	if benchmarkCase, err := ReviewBenchmarkCaseFromAgentExample(example); err == nil {
		report.FilePath = strings.TrimSpace(benchmarkCase.FilePath)
		report.Line = benchmarkCase.Line
	}
	if result == nil {
		return report
	}
	report.Score = result.Score
	if result.SideInfo == nil {
		return report
	}
	diagnostics := result.SideInfo.Diagnostics
	report.RawScore = evolutionDiagnosticFloat(diagnostics, "raw_score")
	report.CaseWeight = evolutionDiagnosticFloatDefault(diagnostics, "case_weight", 1)
	report.CommentCount = evolutionDiagnosticInt(diagnostics, "comment_count")
	report.Matched = evolutionDiagnosticBool(diagnostics, "matched")
	report.MatchedComment = evolutionDiagnosticString(diagnostics, "matched_comment")
	return report
}

func buildEvolutionSuiteMetrics(suites []BenchmarkSuite, baseline, replay map[string]float64) map[string]evolutionSuiteMetrics {
	if len(suites) == 0 {
		return nil
	}
	out := make(map[string]evolutionSuiteMetrics, len(suites))
	for _, suite := range suites {
		out[suite.Path] = evolutionSuiteMetrics{
			TrainingExampleCount:   len(suite.TrainingExamples),
			ValidationExampleCount: len(suite.ValidationExamples),
			BaselineValidation:     baseline[suite.Path],
			ReplayValidation:       replay[suite.Path],
		}
	}
	return out
}

func buildEvolutionCaseReports(suites []BenchmarkSuite, baseline, replay map[string][]EvolutionValidationCaseReport) map[string]evolutionCaseReports {
	if len(suites) == 0 {
		return nil
	}
	out := make(map[string]evolutionCaseReports, len(suites))
	for _, suite := range suites {
		out[suite.Path] = evolutionCaseReports{
			Baseline: append([]EvolutionValidationCaseReport(nil), baseline[suite.Path]...),
			Replay:   append([]EvolutionValidationCaseReport(nil), replay[suite.Path]...),
		}
	}
	return out
}

func protectedSuitesRegressed(baseline, replay map[string]float64, tolerance float64) bool {
	for path, base := range baseline {
		if replayRegressedBeyondTolerance(base, replay[path], tolerance) {
			return true
		}
	}
	return false
}

func promoteEvolutionCandidate(ctx context.Context, cfg EvolveReviewConfig, paths evolutionPaths, runID, candidateArtifactPath, bestOverlay string, currentSkill *skills.Skill, seedSkillVersion int, candidateHash string, report evolutionRunReport) (int, error) {
	if err := os.MkdirAll(paths.currentDir, 0o755); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(paths.previousDir, 0o755); err != nil {
		return 0, err
	}

	currentExists := false
	if _, err := os.Stat(paths.currentArtifactPath); err == nil {
		currentExists = true
		previousPath := filepath.Join(paths.previousDir, "optimized_program.json")
		if err := copyFile(paths.currentArtifactPath, previousPath); err != nil {
			return 0, err
		}
	}
	if err := copyFile(candidateArtifactPath, paths.currentArtifactPath); err != nil {
		return 0, err
	}

	publishedVersion := 0
	if strings.TrimSpace(bestOverlay) != "" {
		storePath, err := ResolveReviewSkillStorePath(cfg.SkillStorePath, cfg.StateDir)
		if err != nil {
			return 0, err
		}
		domain := ResolveReviewSkillDomain(cfg.SkillDomain)
		store := skills.NewFileStore(storePath)
		nextVersion := seedSkillVersion + 1
		if err := store.Save(ctx, skills.Skill{
			Name:    "review-gepa",
			Domain:  domain,
			Content: bestOverlay,
			Version: nextVersion,
			Metadata: map[string]string{
				"run_id":                    runID,
				"baseline_validation_score": fmt.Sprintf("%.6f", report.BaselineValidation),
				"best_validation_score":     fmt.Sprintf("%.6f", report.ReplayValidation),
				"model_id":                  report.StudentModelID,
				"teacher_model_id":          report.TeacherModelID,
			},
		}); err != nil {
			if currentExists {
				_ = copyFile(filepath.Join(paths.previousDir, "optimized_program.json"), paths.currentArtifactPath)
			} else {
				_ = os.Remove(paths.currentArtifactPath)
			}
			if currentSkill != nil {
				_ = store.Save(ctx, *currentSkill)
			}
			return 0, err
		}
		publishedVersion = nextVersion
	}

	manifest := evolutionCurrentManifest{
		RunID:               runID,
		PromotedAt:          time.Now().UTC(),
		StudentModelID:      report.StudentModelID,
		TeacherModelID:      report.TeacherModelID,
		BaselineValidation:  report.BaselineValidation,
		ReplayValidation:    report.ReplayValidation,
		CandidateArtifact:   candidateArtifactPath,
		CurrentArtifactHash: candidateHash,
		PublishedVersion:    publishedVersion,
	}
	if err := writeJSONFile(paths.currentManifestPath, manifest); err != nil {
		return 0, err
	}
	return publishedVersion, nil
}

func archiveInboxFiles(archiveDir, runID string, files []string) error {
	if len(files) == 0 {
		return nil
	}
	runArchiveDir := filepath.Join(archiveDir, runID)
	if err := os.MkdirAll(runArchiveDir, 0o755); err != nil {
		return err
	}
	for _, path := range files {
		dst := filepath.Join(runArchiveDir, filepath.Base(path))
		for i := 1; ; i++ {
			if _, err := os.Stat(dst); os.IsNotExist(err) {
				break
			}
			dst = filepath.Join(runArchiveDir, fmt.Sprintf("%s.%d", filepath.Base(path), i))
		}
		if err := os.Rename(path, dst); err != nil {
			return err
		}
	}
	return nil
}

func pruneRetention(dir string, maxRuns, maxDays int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type candidate struct {
		path    string
		modTime time.Time
	}
	now := time.Now()
	items := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if maxDays > 0 && now.Sub(info.ModTime()) > time.Duration(maxDays)*24*time.Hour {
			_ = os.RemoveAll(path)
			continue
		}
		items = append(items, candidate{path: path, modTime: info.ModTime()})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].modTime.After(items[j].modTime)
	})
	for i := maxRuns; maxRuns > 0 && i < len(items); i++ {
		_ = os.RemoveAll(items[i].path)
	}
}

func loadEvolutionState(path string) (evolutionState, error) {
	var state evolutionState
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return evolutionState{}, err
	}
	return state, nil
}

func loadEvolutionCircuit(path string) (bool, evolutionCircuit, error) {
	var circuit evolutionCircuit
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, circuit, nil
		}
		return false, circuit, err
	}
	if err := json.Unmarshal(data, &circuit); err != nil {
		return false, evolutionCircuit{}, err
	}
	return true, circuit, nil
}

func writeJSONFile(path string, payload interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func classifyEvolutionFailure(ctx context.Context, fallback string) string {
	if err := ctx.Err(); err != nil {
		if err == context.DeadlineExceeded {
			return "failed_timeout"
		}
		return "failed_cancelled"
	}
	return fallback
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" {
		return fmt.Errorf("copy file requires source and destination")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func evolutionDiagnosticInt(diagnostics map[string]interface{}, key string) int {
	switch value := diagnostics[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func evolutionDiagnosticBool(diagnostics map[string]interface{}, key string) bool {
	value, _ := diagnostics[key].(bool)
	return value
}

func evolutionDiagnosticFloat(diagnostics map[string]interface{}, key string) float64 {
	switch value := diagnostics[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int32:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

func evolutionDiagnosticFloatDefault(diagnostics map[string]interface{}, key string, fallback float64) float64 {
	if value := evolutionDiagnosticFloat(diagnostics, key); value != 0 {
		return value
	}
	return fallback
}

func evolutionDiagnosticString(diagnostics map[string]interface{}, key string) string {
	value, _ := diagnostics[key].(string)
	return value
}

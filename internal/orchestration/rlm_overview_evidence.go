package orchestration

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	rlmOverviewFocusedEvidenceMaxChars       = 24000
	rlmOverviewFocusedEvidenceMaxEntries     = 160
	rlmOverviewFocusedEvidenceMaxRepoEntries = 20000
	rlmOverviewFocusedEvidenceMaxReadBytes   = 256000
)

var (
	overviewEvidenceWordPattern         = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_-]*`)
	overviewEvidenceFuncPattern         = regexp.MustCompile(`(?m)^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)`)
	overviewEvidenceTypePattern         = regexp.MustCompile(`(?m)^\s*type\s+([A-Za-z_][A-Za-z0-9_]*)`)
	overviewEvidenceTopLevelDeclPattern = regexp.MustCompile(`(?m)^\s*(?:const|var)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	overviewEvidenceAssignPattern       = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:[A-Za-z_][A-Za-z0-9_./*\[\]]+)?\s*=`)
)

type rlmOverviewFocusedEvidence struct {
	Text    string
	Sources []string
}

type overviewEvidenceCandidate struct {
	Kind   string
	Value  string
	Source string
	Score  int
}

type overviewManifestSection struct {
	Title   string
	Content string
}

func buildRLMOverviewFocusedEvidence(repoPath string, manifest rlmOverviewManifest, question string, maxChars int) rlmOverviewFocusedEvidence {
	repoPath = strings.TrimSpace(repoPath)
	question = strings.TrimSpace(question)
	if repoPath == "" || question == "" {
		return rlmOverviewFocusedEvidence{}
	}
	if maxChars <= 0 {
		maxChars = rlmOverviewFocusedEvidenceMaxChars
	}

	terms := overviewEvidenceTerms(question)
	expandedTerms := overviewEvidenceExpandedTerms(question, terms)
	broadQuestion := overviewEvidenceBroadQuestion(question)
	candidates := make([]overviewEvidenceCandidate, 0, 128)
	sources := make([]string, 0, 32)

	for _, section := range parseOverviewManifestSections(manifest.Context) {
		score := overviewEvidenceScore(section.Title+"\n"+section.Content, expandedTerms)
		if broadQuestion && overviewBroadManifestSection(section.Title) {
			score += 8
		}
		if score <= 0 {
			continue
		}
		excerpt := overviewEvidenceSectionExcerpt(section, expandedTerms, broadQuestion)
		if excerpt == "" {
			continue
		}
		candidates = append(candidates, overviewEvidenceCandidate{
			Kind:   "manifest",
			Value:  fmt.Sprintf("## %s\n%s", section.Title, excerpt),
			Source: overviewSourceFromManifestSection(section.Title),
			Score:  score,
		})
	}

	repoEntries, pathSources := overviewEvidenceRepositoryEntries(repoPath)
	for _, entry := range repoEntries {
		score := overviewEvidenceScore(entry, expandedTerms)
		if broadQuestion && overviewBroadRepoEntry(entry) {
			score += 6
		}
		if score <= 0 {
			continue
		}
		candidates = append(candidates, overviewEvidenceCandidate{
			Kind:   "path",
			Value:  entry,
			Source: strings.TrimSuffix(entry, "/"),
			Score:  score,
		})
	}

	for _, candidate := range overviewEvidenceSourceCandidates(repoPath, pathSources, expandedTerms, broadQuestion) {
		candidates = append(candidates, candidate)
	}
	for _, candidate := range overviewEvidenceAbsenceCandidates(question, repoEntries) {
		candidates = append(candidates, candidate)
	}

	candidates = dedupeOverviewEvidenceCandidates(candidates)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].Kind != candidates[j].Kind {
			return candidates[i].Kind < candidates[j].Kind
		}
		return candidates[i].Value < candidates[j].Value
	})
	if len(candidates) > rlmOverviewFocusedEvidenceMaxEntries {
		candidates = candidates[:rlmOverviewFocusedEvidenceMaxEntries]
	}

	var b strings.Builder
	b.WriteString("Focused repository evidence for the overview question.\n")
	b.WriteString("Use these candidates before inspecting broader context; mention the exact paths/symbols that answer the question.\n\n")
	overviewWriteEvidenceGroup(&b, "Candidate repo paths", candidates, "path", maxChars)
	overviewWriteEvidenceGroup(&b, "Candidate symbols and literals", candidates, "symbol", maxChars)
	overviewWriteEvidenceGroup(&b, "Absence checks", candidates, "absence", maxChars)
	overviewWriteEvidenceGroup(&b, "Relevant manifest excerpts", candidates, "manifest", maxChars)

	text := strings.TrimSpace(b.String())
	if maxChars > 0 && len(text) > maxChars {
		text = strings.TrimSpace(text[:maxChars]) + "\n...(focused evidence truncated)"
	}
	for _, candidate := range candidates {
		if candidate.Source != "" {
			sources = appendUniqueString(sources, filepath.ToSlash(strings.Trim(candidate.Source, "/")))
		}
	}
	return rlmOverviewFocusedEvidence{
		Text:    text,
		Sources: mergeStringLists(manifest.Sources, sources),
	}
}

func parseOverviewManifestSections(context string) []overviewManifestSection {
	lines := strings.Split(strings.ReplaceAll(context, "\r\n", "\n"), "\n")
	sections := make([]overviewManifestSection, 0, 16)
	currentTitle := ""
	currentLines := make([]string, 0)
	flush := func() {
		if strings.TrimSpace(currentTitle) == "" {
			currentLines = currentLines[:0]
			return
		}
		sections = append(sections, overviewManifestSection{
			Title:   strings.TrimSpace(currentTitle),
			Content: strings.TrimSpace(strings.Join(currentLines, "\n")),
		})
		currentLines = currentLines[:0]
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
			currentTitle = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}
		currentLines = append(currentLines, line)
	}
	flush()
	return sections
}

func overviewEvidenceTerms(question string) []string {
	lower := strings.ToLower(question)
	matches := overviewEvidenceWordPattern.FindAllString(lower, -1)
	terms := make([]string, 0, len(matches))
	for _, match := range matches {
		for _, part := range strings.FieldsFunc(match, func(r rune) bool { return r == '-' || r == '_' }) {
			part = strings.Trim(part, "-_")
			if part == "" || overviewEvidenceStopWord(part) {
				continue
			}
			if len(part) < 3 && part != "qa" && part != "ui" && part != "rlm" {
				continue
			}
			terms = appendUniqueString(terms, part)
			if strings.HasSuffix(part, "s") && len(part) > 4 {
				terms = appendUniqueString(terms, strings.TrimSuffix(part, "s"))
			}
		}
	}
	return terms
}

func overviewEvidenceStopWord(term string) bool {
	switch term {
	case "what", "where", "which", "how", "does", "this", "that", "with", "from", "into", "under", "about", "main", "major", "high", "level", "repo", "repository", "project", "package", "packages", "path", "paths", "area", "areas", "piece", "pieces", "support", "supports", "located", "implemented", "implementation", "contains", "contain", "organized", "organization", "architecture", "overview", "built", "built-in", "related", "helpers", "shared", "concerns":
		return true
	default:
		return false
	}
}

func overviewEvidenceExpandedTerms(question string, terms []string) []string {
	expanded := make([]string, 0, len(terms)*4)
	for _, term := range terms {
		expanded = appendUniqueString(expanded, term)
		for _, alias := range overviewEvidenceAliasesForTerm(term) {
			expanded = appendUniqueString(expanded, alias)
		}
	}

	lower := strings.ToLower(question)
	if strings.Contains(lower, "rlm overview") {
		for _, alias := range []string{"rlm_overview", "rlm_artifacts", "buildRLMOverviewManifest", "buildRLMOverviewQueryWithOverlay", "handleRLMOverview", "rlm_usage"} {
			expanded = appendUniqueString(expanded, alias)
		}
	}
	if strings.Contains(lower, "command-line") || strings.Contains(lower, "command line") {
		for _, alias := range []string{"cmd/dspy-cli", "internal/commands", "commands"} {
			expanded = appendUniqueString(expanded, alias)
		}
	}
	if strings.Contains(lower, "browser frontend") || strings.Contains(lower, "react or browser") || strings.Contains(lower, "frontend") {
		for _, alias := range []string{"go.mod", "main.go", "terminal", "cli", "tui", "package.json", "src/app.tsx", "react frontend"} {
			expanded = appendUniqueString(expanded, alias)
		}
	}
	if strings.Contains(lower, "maestro") && strings.Contains(lower, "terminal") {
		for _, alias := range []string{"go.mod", "README.md", "terminal", "maestro"} {
			expanded = appendUniqueString(expanded, alias)
		}
	}
	if overviewEvidenceBroadQuestion(question) {
		for _, alias := range []string{"go.mod", "README.md", "main.go", "cmd", "internal", "pkg", "terminal", "examples", "benchmarks", "tests", "orchestration", "review", "core", "modules", "agents", "optimizers", "llms", "tools"} {
			expanded = appendUniqueString(expanded, alias)
		}
	}
	return expanded
}

func overviewEvidenceAliasesForTerm(term string) []string {
	switch term {
	case "ask":
		return []string{"internal/orchestration", "service.go", "native_qa", "rlm_overview", "query_analysis"}
	case "rlm":
		return []string{"rlm", "pkg/modules/rlm", "pkg/agents/rlm", "examples/rlm", "examples/rlm_oolong_gepa", "ArtifactRLMIterationPrompt"}
	case "gepa":
		return []string{"gepa", "RunGEPAWorkflow", "ArtifactRLMIterationPrompt", "proposal", "reflection", "optimizers"}
	case "qa":
		return []string{"qa", "optimize-qa", "qa_benchmark", "qa_suite", "native_qa", "RunGEPAWorkflow"}
	case "optimization", "optimize", "optimizer":
		return []string{"optimize", "optimization", "optimizer", "optimizers", "RunGEPAWorkflow", "artifacts.go", "optimized_program.go", "harness.go", "workflow.go", "cmd/optimize-review", "cmd/evolve-review"}
	case "prompt", "artifact", "artifacts":
		return []string{"artifacts.go", "ArtifactRLMIterationPrompt", "optimized_program", "prompt"}
	case "review":
		return []string{"internal/review", "agent.go", "benchmark.go", "postprocess.go", "verifier.go", "evolution.go", "artifacts.go", "cmd/optimize-review", "cmd/evolve-review"}
	case "gerrit":
		return []string{"gerrit", "gerrit_corpus", "cmd/ingest-gerrit-review", "cmd/generate-review-traces", "teacher_traces"}
	case "training", "data":
		return []string{"corpus", "trace", "traces", "datasets"}
	case "terminal", "tui":
		return []string{"terminal", "app.go", "maestro_model.go", "review_model.go", "statusbar.go", "commands.go", "keybindings.go", "cli"}
	case "subagent", "subagents":
		return []string{"internal/subagent", "session.go", "sqlite_store.go", "claude.go", "gemini.go", "tool_agent.go"}
	case "github":
		return []string{"internal/github", "client.go", "mcp_review.go", "mcp_bash.go", "mcp"}
	case "search":
		return []string{"internal/search", "sgrep.go", "planner.go", "simple.go"}
	case "context":
		return []string{"internal/context", "loader.go", "internal/chunk", "pkg/agents/context", "manager.go", "cache"}
	case "chunk", "chunking":
		return []string{"internal/chunk", "chunk.go"}
	case "ace":
		return []string{"internal/ace", "pkg/agents/ace", "ace"}
	case "guideline", "guidelines":
		return []string{"internal/guideline", "guideline"}
	case "rule", "rules":
		return []string{"internal/rules", "store.go", "rules"}
	case "workflow", "workflows":
		return []string{"internal/workflow", "pkg/agents/workflows", "builder.go", "execution.go", "integration.go", "util.go", "workflow.go", "router.go", "parallel.go"}
	case "config", "configuration":
		return []string{"internal/config", "config.go"}
	case "util", "utility":
		return []string{"internal/util", "util.go"}
	case "integration", "tests", "test":
		return []string{"tests/integration", "_test.go", "internal", "terminal"}
	case "native":
		return []string{"pkg/agents/native", "agent.go", "session.go", "session_control.go"}
	case "agent", "agents":
		return []string{"pkg/agents", "internal/agent", "agent.go"}
	case "react":
		return []string{"pkg/agents/react", "react_agent.go", "planner.go", "reflection.go", "memory_optimizer.go", "react_agent"}
	case "tool", "tools", "tooling":
		return []string{"pkg/tools", "tool.go", "registry.go", "mcp.go", "defaults", "bash", "files"}
	case "interceptor", "interceptors":
		return []string{"pkg/interceptors", "standard.go", "security.go", "performance.go", "tool.go", "function_calling.go", "xml.go", "structured_output.go"}
	case "dataset", "datasets":
		return []string{"pkg/datasets", "gsm8k.go", "hotpot_qa.go", "oolong.go", "tblite.go"}
	case "metric", "metrics":
		return []string{"pkg/metrics", "accuracy.go"}
	case "cache":
		return []string{"pkg/cache", "sqlite_cache.go", "memory_cache.go", "pkg/agents/context"}
	case "provider", "providers", "llm":
		return []string{"pkg/llms", "openai.go", "anthropic.go", "gemini.go", "ollama.go", "llamacpp.go"}
	case "examples", "example":
		return []string{"examples", "examples/agents", "examples/react_agent", "examples/rlm", "examples/rlm_oolong_gepa", "examples/mcp_optimizer", "examples/multimodal", "examples/xml_adapter"}
	default:
		return nil
	}
}

func overviewEvidenceBroadQuestion(question string) bool {
	lower := strings.ToLower(question)
	return strings.Contains(lower, "high level") ||
		strings.Contains(lower, "organized") ||
		strings.Contains(lower, "organization") ||
		strings.Contains(lower, "repo structure") ||
		strings.Contains(lower, "repository structure") ||
		strings.Contains(lower, "project structure")
}

func overviewEvidenceScore(text string, terms []string) int {
	lower := strings.ToLower(text)
	score := 0
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		if lower == term {
			score += 12
			continue
		}
		if strings.Contains(lower, term) {
			score += 4
			if strings.Contains(lower, "/"+term) || strings.Contains(lower, term+"/") || strings.Contains(lower, "_"+term) || strings.Contains(lower, term+"_") || strings.Contains(lower, "-"+term) || strings.Contains(lower, term+"-") {
				score += 3
			}
			if strings.Contains(term, "/") || strings.Contains(term, ".go") || strings.Contains(term, "_") {
				score += 5
			}
		}
	}
	return score
}

func overviewBroadManifestSection(title string) bool {
	lower := strings.ToLower(title)
	return lower == "top-level entries" ||
		lower == "go.mod" ||
		lower == "repository readme" ||
		strings.HasSuffix(lower, " packages") ||
		strings.HasPrefix(lower, "file index: pkg") ||
		strings.HasPrefix(lower, "file index: internal") ||
		strings.HasPrefix(lower, "file index: terminal") ||
		strings.HasPrefix(lower, "file index: examples")
}

func overviewBroadRepoEntry(entry string) bool {
	entry = strings.Trim(entry, "/")
	if entry == "" {
		return false
	}
	depth := overviewRelativeDepth(entry)
	if depth <= 1 {
		return true
	}
	return depth == 2 && (strings.HasPrefix(entry, "pkg/") || strings.HasPrefix(entry, "internal/") || strings.HasPrefix(entry, "cmd/"))
}

func overviewEvidenceSectionExcerpt(section overviewManifestSection, terms []string, broad bool) string {
	lines := strings.Split(section.Content, "\n")
	if broad || overviewEvidenceScore(section.Title, terms) > 0 {
		return overviewEvidenceLimitLines(lines, 40)
	}
	selected := make([]string, 0, len(lines))
	for _, line := range lines {
		if overviewEvidenceScore(line, terms) > 0 {
			selected = append(selected, strings.TrimSpace(line))
		}
	}
	return overviewEvidenceLimitLines(selected, 40)
}

func overviewEvidenceLimitLines(lines []string, limit int) string {
	filtered := make([]string, 0, min(limit, len(lines)))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		filtered = append(filtered, line)
		if len(filtered) >= limit {
			break
		}
	}
	return strings.Join(filtered, "\n")
}

func overviewSourceFromManifestSection(title string) string {
	title = strings.TrimSpace(strings.TrimPrefix(title, "File Index: "))
	title = strings.TrimPrefix(title, "Package Metadata: ")
	if title == "Repository README" {
		return "README.md"
	}
	return filepath.ToSlash(title)
}

func overviewEvidenceRepositoryEntries(repoPath string) ([]string, []string) {
	entries := make([]string, 0, 1024)
	files := make([]string, 0, 1024)
	root := filepath.Clean(repoPath)
	errStop := fmt.Errorf("overview focused evidence file limit reached")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == root {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() && overviewIndexSkipDirs[name] {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			entries = append(entries, rel+"/")
		} else if overviewShouldIndexFile(name) {
			entries = append(entries, rel)
			files = append(files, rel)
		}
		if len(entries) >= rlmOverviewFocusedEvidenceMaxRepoEntries {
			return errStop
		}
		return nil
	})
	if err != nil && err != errStop {
		return nil, nil
	}
	sort.Strings(entries)
	sort.Strings(files)
	return entries, files
}

func overviewEvidenceSourceCandidates(repoPath string, files []string, terms []string, broad bool) []overviewEvidenceCandidate {
	candidates := make([]overviewEvidenceCandidate, 0, 64)
	for _, relPath := range files {
		if !overviewFocusedEvidenceReadableFile(relPath) {
			continue
		}
		pathScore := overviewEvidenceScore(relPath, terms)
		if broad && overviewBroadRepoEntry(relPath) {
			pathScore += 3
		}
		content, err := overviewReadEvidenceFile(repoPath, relPath)
		if err != nil || content == "" {
			continue
		}
		lineScore := overviewEvidenceScore(content, terms)
		if pathScore <= 0 && lineScore <= 0 {
			continue
		}
		if filepath.Ext(relPath) == ".go" {
			for _, symbol := range overviewGoSymbols(content) {
				score := pathScore + overviewEvidenceScore(symbol, terms)
				if score <= 0 {
					continue
				}
				candidates = append(candidates, overviewEvidenceCandidate{
					Kind:   "symbol",
					Value:  fmt.Sprintf("%s: %s", relPath, symbol),
					Source: relPath,
					Score:  score + 6,
				})
			}
		}
		for _, line := range overviewEvidenceMatchingLines(content, terms) {
			score := pathScore + overviewEvidenceScore(line, terms)
			if score <= 0 {
				continue
			}
			candidates = append(candidates, overviewEvidenceCandidate{
				Kind:   "symbol",
				Value:  fmt.Sprintf("%s: %s", relPath, line),
				Source: relPath,
				Score:  score,
			})
		}
	}
	return candidates
}

func overviewFocusedEvidenceReadableFile(relPath string) bool {
	ext := strings.ToLower(filepath.Ext(relPath))
	switch ext {
	case ".go", ".md", ".json", ".yaml", ".yml", ".toml", ".txt":
		return true
	default:
		return false
	}
}

func overviewReadEvidenceFile(repoPath, relPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoPath, filepath.FromSlash(relPath)))
	if err != nil {
		return "", err
	}
	if len(data) > rlmOverviewFocusedEvidenceMaxReadBytes {
		data = data[:rlmOverviewFocusedEvidenceMaxReadBytes]
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
}

func overviewGoSymbols(content string) []string {
	symbols := make([]string, 0, 16)
	for _, pattern := range []*regexp.Regexp{overviewEvidenceFuncPattern, overviewEvidenceTypePattern, overviewEvidenceTopLevelDeclPattern, overviewEvidenceAssignPattern} {
		for _, match := range pattern.FindAllStringSubmatch(content, -1) {
			if len(match) < 2 {
				continue
			}
			name := strings.TrimSpace(match[1])
			if name == "" || strings.EqualFold(name, "package") || strings.EqualFold(name, "import") {
				continue
			}
			symbols = appendUniqueString(symbols, name)
		}
	}
	return symbols
}

func overviewEvidenceMatchingLines(content string, terms []string) []string {
	lines := strings.Split(content, "\n")
	matches := make([]string, 0, 12)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || len(line) > 240 {
			continue
		}
		if overviewEvidenceScore(line, terms) <= 0 {
			continue
		}
		matches = appendUniqueString(matches, line)
		if len(matches) >= 16 {
			break
		}
	}
	return matches
}

func overviewEvidenceAbsenceCandidates(question string, repoEntries []string) []overviewEvidenceCandidate {
	lower := strings.ToLower(question)
	checks := make([]string, 0, 4)
	if strings.Contains(lower, "frontend") || strings.Contains(lower, "browser") || strings.Contains(lower, "react or browser") {
		checks = append(checks, "package.json", "src/app.tsx", "frontend", "browser")
	}
	if strings.Contains(lower, "maestro") && strings.Contains(lower, "terminal") {
		checks = append(checks, "maestro", "terminal")
	}
	if len(checks) == 0 {
		return nil
	}
	entryText := strings.ToLower(strings.Join(repoEntries, "\n"))
	candidates := make([]overviewEvidenceCandidate, 0, len(checks))
	for _, check := range checks {
		if strings.Contains(entryText, strings.ToLower(check)) {
			continue
		}
		candidates = append(candidates, overviewEvidenceCandidate{
			Kind:  "absence",
			Value: fmt.Sprintf("No indexed repo path contains %q.", check),
			Score: 100,
		})
	}
	return candidates
}

func dedupeOverviewEvidenceCandidates(candidates []overviewEvidenceCandidate) []overviewEvidenceCandidate {
	seen := make(map[string]int, len(candidates))
	deduped := make([]overviewEvidenceCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Value = strings.TrimSpace(candidate.Value)
		candidate.Source = filepath.ToSlash(strings.TrimSpace(candidate.Source))
		if candidate.Value == "" {
			continue
		}
		key := candidate.Kind + "\x00" + candidate.Value
		if idx, ok := seen[key]; ok {
			if candidate.Score > deduped[idx].Score {
				deduped[idx].Score = candidate.Score
			}
			if deduped[idx].Source == "" {
				deduped[idx].Source = candidate.Source
			}
			continue
		}
		seen[key] = len(deduped)
		deduped = append(deduped, candidate)
	}
	return deduped
}

func overviewWriteEvidenceGroup(b *strings.Builder, title string, candidates []overviewEvidenceCandidate, kind string, maxChars int) {
	lines := make([]string, 0, 16)
	currentLen := b.Len() + len(title) + len(":\n\n")
	for _, candidate := range candidates {
		if candidate.Kind != kind {
			continue
		}
		line := "- " + candidate.Value + "\n"
		if maxChars > 0 && currentLen+len(line) > maxChars {
			break
		}
		lines = append(lines, line)
		currentLen += len(line)
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString(title)
	b.WriteString(":\n")
	for _, line := range lines {
		b.WriteString(line)
	}
	b.WriteString("\n")
}

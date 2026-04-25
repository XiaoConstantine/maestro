`qa_suite.json` is a Maestro-owned benchmark corpus for `/ask` QA GEPA runs.
`rlm_overview_suite.json` is the frozen Stage 0 corpus for optimizing the
RLM overview route. It uses the same path resolution rules, but each case also
stores a gold overview answer, expected source paths, tags, and an optional
`protected` marker for zero-regression gates.

Path resolution rules:
- `repo_path` values are resolved relative to the suite file location.
- `~` and environment variables inside `repo_path` are expanded.
- Absolute `repo_path` values are used as-is.

Current expected local layout:
- this repository checked out at `.../maestro`
- `dspy-go` checked out as a sibling at `.../dspy-go`

That is why the current suite uses:
- `..` for Maestro cases
- `../../dspy-go` for `dspy-go` cases

If your checkout layout differs, either:
- edit the `repo_path` values in the suite, or
- use a separate suite file with paths that match your local workspace.

Benchmark case guidance:
- keep expected facts concrete and substring-friendly, such as file paths, package paths, or symbol names
- use forbidden facts to penalize cross-repo confusion and hallucinated paths
- include both positive lookups and negative/boundary cases

RLM overview benchmark guidance:
- optimize against deterministic fact/source/terseness scoring first; do not let GEPA chase freeform style only
- treat the committed Stage 0 suite as a manually distilled bootstrap from repository manifests, not as a teacher-generated session-log corpus
- keep gold answers concise and repo-grounded so future LLM-judge runs have stable reference answers
- mark a small qualitative subset as `protected`; artifact replay must not regress those cases
- expected sources may be returned as explicit source metadata or appear in the answer text

Review benchmark guidance:
- keep generated review corpora, raw Gerrit caches, and GEPA checkpoints local under `~/.maestro/review/`; do not commit them to the repository
- keep high-signal Go reviewer corpora separate at first, for example `rsc` and `iant`, instead of immediately mixing them
- validate optimizer lift per reviewer suite before publishing a merged review skill
- `cmd/ingest-gerrit-review` can synthesize reviewer-specific Gerrit queries via `--reviewer-email`

Optimized program artifacts:
- `cmd/optimize-qa` and `cmd/optimize-review` now write a separate optimized-program JSON artifact alongside the checkpoint JSON
- `--qa-artifacts` and `--review-artifacts` accept both legacy raw artifact payloads and the newer `dspy-go.optimized-agent-program` envelope
- restore is forward-compatible: obsolete target IDs in a saved optimized program are silently skipped when Maestro loads it against a newer agent shape

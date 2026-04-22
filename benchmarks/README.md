`qa_suite.json` is a Maestro-owned benchmark corpus for `/ask` QA GEPA runs.

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

Review benchmark guidance:
- keep generated review corpora, raw Gerrit caches, and GEPA checkpoints local under `~/.maestro/review/`; do not commit them to the repository
- keep high-signal Go reviewer corpora separate at first, for example `rsc` and `iant`, instead of immediately mixing them
- validate optimizer lift per reviewer suite before publishing a merged review skill
- `cmd/ingest-gerrit-review` can synthesize reviewer-specific Gerrit queries via `--reviewer-email`

Optimized program artifacts:
- `cmd/optimize-qa` and `cmd/optimize-review` now write a separate optimized-program JSON artifact alongside the checkpoint JSON
- `--qa-artifacts` and `--review-artifacts` accept both legacy raw artifact payloads and the newer `dspy-go.optimized-agent-program` envelope
- restore is forward-compatible: obsolete target IDs in a saved optimized program are silently skipped when Maestro loads it against a newer agent shape

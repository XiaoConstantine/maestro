# Gemini RLM Verification Handoff

Date: 2026-04-26

## Scope

This pass verified the Stage 0-5 RLM work by running Maestro paths against Gemini 2.5 models, not by relying on unit or integration tests.

Covered:

- Ask overview route through `cmd/maestro-probe` and `MaestroService.ProcessRequest`.
- Targeted ask route through `cmd/maestro-probe`.
- Overview benchmark and protected baseline path through `cmd/optimize-rlm-ask`.
- Budget accounting metadata on ask-side RLM routes.
- Artifact load/fallback behavior on the overview route.

Not covered:

- Review RLM happy path. `maestro-probe` is ask-only by design.
- `cmd/optimize-review-rlm`. The default generated review corpus was not available locally.
- A successful GEPA run. No clean Gemini baseline was writable.
- Valid optimized artifact application at runtime. The fallback path was exercised, but no Gemini artifact was produced to apply.
- Stage 6 work. CC-as-core.LLM and tiered routing were not started.

## Positive Signals

Ask-side integration plumbing is healthy independent of model quality:

- `maestro-probe` exercised the real ask path: `ProcessRequest(RequestAsk)` routed through `handleAsk`.
- Overview RLM routing worked with Gemini Pro. Metadata showed `strategy=rlm_overview`, non-empty sources including `go.mod` and `README.md`, non-zero `rlm_usage.total_tokens`, and budget attribution under `ask.rlm_overview`.
- Targeted RLM routing worked with Gemini Flash. Metadata showed `strategy=rlm_targeted` and budget attribution under `ask.rlm_targeted`.
- Budget metadata was populated on ask-side RLM routes. Gemini routes correctly reported cache-token weighting as unavailable.
- Route registration and runtime fallback worked: corrupt overview artifact input emitted a WARN, skipped the artifact, continued with baseline RLM, and did not crash.
- Stage 4.5 contamination protection worked: baseline writes were refused whenever an errored case was present. This prevented GEPA from optimizing against a baseline containing poisoned zero-score cases.

## Runtime Results

Overview benchmark suite: `benchmarks/rlm_overview_suite.json`, 32 cases, `--workers 2`, `--max-attempts 1`.

| Model/config | Avg score | Passed | Failed | Eval errors | Tokens | Baseline |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| `gemini-2.5-flash`, compact instructions, 60s RLM timeout | 0.2403 | 3 | 29 | 5 | 191,963 | refused |
| `gemini-2.5-pro`, compact instructions, 60s RLM timeout | 0.1930 | 1 | 31 | 12 | 132,717 | refused |
| `gemini-2.5-flash`, compact instructions, 120s RLM timeout | 0.2241 | 1 | 31 | 3 | 257,540 | refused |
| `gemini-2.5-flash`, full iteration instructions, 120s RLM timeout | 0.2992 | 2 | 30 | 1 | 415,068 | refused |
| `gemini-2.5-pro`, full iteration instructions, 120s RLM timeout | 0.3104 | 2 | 30 | 1 | 233,318 | refused |

The 120s timeout bump is already committed. The full iteration instruction toggle was tested locally across overview, targeted ask, and review RLM construction sites, but was not retained because it did not clear the baseline gate and materially increased token use on Flash.

## Error Categories

Before the full-instruction toggle:

- Pro explicit evaluation errors: 12 timeout, 0 REPL syntax, 0 JSON parse, 0 other.
- Flash explicit evaluation errors: 5 timeout, 0 REPL syntax, 0 JSON parse, 0 other.
- Non-error malformed or wrapper outputs: Pro 13, Flash 4.

With full iteration instructions:

- Pro explicit evaluation errors: 1 timeout.
- Flash explicit evaluation errors: 1 timeout.
- Both failed on the same case: `maestro-search-context`.

Representative failure mode:

- The model obtained a useful JSON answer from `Query`.
- It then tried to parse or reformat the fenced JSON result instead of calling `FINAL`.
- It repeatedly emitted nested fenced Go blocks inside the `Code` field, causing REPL parse errors.
- The loop continued until the Gemini request hit the context deadline.

This points to Gemini RLM finalization and REPL-output formatting behavior, not missing repository context or a labeled-data issue.

## Hand-Graded Samples

The low benchmark scores were not only rubric brittleness:

- `maestro-ask-architecture`: broad README-style answer, but missed concrete expected files such as `service.go`, `native_qa.go`, and `rlm_overview.go`.
- `maestro-rlm-overview-architecture`: broad and partially wrong implementation areas, missing `buildRLMOverviewManifest`, `buildRLMOverviewQueryWithOverlay`, `handleRLMOverview`, and `rlm_usage`.
- `maestro-qa-gepa-architecture`: execution timeout.
- `maestro-review-workflow-architecture`: plausible high-level summary, but missed the expected `internal/review` file-level facts.

## Targeted Ask Status

Targeted ask plumbing is partially verified:

- Flash reached the `rlm_targeted` strategy and budget attribution path.
- Pro at 120s still timed out and fell back to native QA.

Targeted ask happy-path quality remains unverified. Earlier Flash output showed malformed JSON and wrong content; Pro did not complete the RLM path.

## Decision

Stop Gemini prompt/config iteration in this pass.

Already tried:

- Gemini Flash and Gemini Pro.
- 60s to 120s RLM timeout bump.
- Compact to full dspy-go iteration instructions as a temporary local experiment.

Do not:

- Attempt a third prompt variant.
- Attempt smoke GEPA without a clean baseline.
- Start Stage 6 before the Gemini RLM finalization issue is understood.

## Recommended Next Session

1. Investigate dspy-go RLM output parsing/finalization with Gemini before making more Maestro-side changes.
2. Focus specifically on nested fenced `Code` blocks, failure to call `FINAL`, and whether the RLM parser can recover when a model returns fenced JSON from a `Query`.
3. Rerun `cmd/optimize-rlm-ask` baseline-only after that fix. The first success criterion is 0 evaluation errors and a writable baseline, not a higher average score.
4. Only after a clean baseline, run a smoke GEPA pass.
5. Separately verify targeted ask happy path and review RLM with real Maestro workflows.

## OpenAI Follow-Up

Follow-up run with `openai:gpt-5.4-mini` on the same overview path cleared the execution-error blocker that stopped Gemini:

- Overview probe completed with `termination_cause=final_answer`, 2 iterations, 1,898 tokens, and budget attribution under `ask.rlm_overview`.
- Full 32-case overview baseline completed with 0 evaluation errors and wrote `/tmp/maestro-overview-openai-gpt54mini-baseline.json`.
- Baseline quality was still poor: average score 0.1805, 0 passed, 32 failed, 93,058 tokens.
- Smoke GEPA completed with `--population 2 --generations 1`, wrote `/tmp/maestro-overview-openai-gpt54mini-artifact.json`, and passed the protected gate.
- The smoke artifact was not a quality improvement: baseline validation 0.1719, best validation 0.1656, replay validation 0.1641.
- Runtime probe with the smoke artifact applied successfully: `rlm_artifact_applied=true`, `termination_cause=final_answer`, 1 iteration, 961 tokens.

Interpretation: `gpt-5.4-mini` is viable for exercising the RLM optimization and artifact-apply pipeline, but the current overview benchmark quality is too low for a meaningful optimization claim. Treat the generated `/tmp` artifact as a smoke-test artifact only.

## Post-Report Trace Investigation

Additional trace inspection sharpened the failure diagnosis:

- The Pro full-instruction `maestro-search-context` report shows several empty-action iterations before timeout. dspy-go extracts `outputs["action"]` directly in `pkg/modules/rlm/rlm.go`; when this is empty, RLM records a no-op history entry and continues. That turns parser/custom-prefix failures into silent budget burn.
- The Flash full-instruction failure on the same case had a useful `Query` result in `result`, then repeatedly emitted fenced Go blocks inside the `Code` field. dspy-go strips leading language markers such as `go\n`, but not a full nested Markdown `go` fence inside the extracted `Code` field, so the REPL parses the fence as code and fails before `FINAL`.
- There is no host-side short-circuit when the last `Query`/`QueryRaw` response already matches the requested JSON schema. If a later iteration times out or loops, the benchmark sees an execution error even though a usable JSON answer was present earlier in the trace.
- Corrected one-case traces with absolute `repo_path` showed Gemini Flash can score 1.0 on `maestro-search-context`, while `gpt-5.4-mini` still finished with a broad answer and score 0.10. This makes the full-suite Gemini timeout non-deterministic, but the OpenAI quality issue reproducible.
- The OpenAI low-score pattern is mostly a manifest/query quality issue: the manifest exposes top-level directories, README text, and a flat package list, but not enough package-level metadata for smaller models to reliably connect `internal/search`, `internal/context`, and `internal/chunk` to the question.

Updated priority order:

1. Fix or upstream dspy-go custom-prefix parsing/empty-action handling so malformed iteration outputs fail loudly or are retried instead of burning iterations.
2. Add code-field normalization for nested markdown fences before REPL execution.
3. Add a Maestro-side overview short-circuit: if the latest sub-LLM response parses as the requested overview JSON schema, return it instead of asking the RLM to reformat it through another REPL iteration.
4. Improve `buildRLMOverviewManifest` with package-level file lists or package comments for directories without README/doc metadata.

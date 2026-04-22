package reasoning

import (
	"strings"
	"testing"
)

func TestCreateCodeReviewSignature_OverlayChangesInstructionHash(t *testing.T) {
	base := createCodeReviewSignature("")
	withOverlay := createCodeReviewSignature("Prefer changed-hunk grounded findings.")

	if base.Instruction == withOverlay.Instruction {
		t.Fatalf("instruction should differ when overlay is applied")
	}
	if hashSignature(base) == hashSignature(withOverlay) {
		t.Fatalf("signature hash should change when overlay changes")
	}
}

func TestMaterializeCodeReviewInstruction_AppendsOverlay(t *testing.T) {
	got := materializeCodeReviewInstruction("Use concise, high-confidence findings.")
	if got == codeReviewInstruction {
		t.Fatalf("materialized instruction should include overlay")
	}
	if want := "REVIEW SKILL PACK:"; !strings.Contains(got, want) {
		t.Fatalf("materialized instruction missing %q", want)
	}
}

func TestCreateCodeReviewSignature_IncludesChunkContextInput(t *testing.T) {
	sig := createCodeReviewSignature("")
	for _, input := range sig.Inputs {
		if input.Name == "chunk_context" {
			return
		}
	}
	t.Fatalf("signature inputs missing chunk_context")
}

func TestMaterializeCodeReviewInstruction_IncludesChunkBoundaryGuidance(t *testing.T) {
	got := materializeCodeReviewInstruction("")
	for _, want := range []string{
		"Treat file_content as an excerpt",
		"Do not report missing braces",
		`Do NOT report style or naming issues unless they introduce ambiguity`,
		`Do NOT mark style findings as "critical" or "high" severity`,
		`Reserve "critical" and "high" severities for concrete correctness`,
		"Treat guidelines as secondary evidence",
		"Do NOT report preference-only suggestions such as compile-time interface assertions",
		"Use line numbers relative to file_content",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("materialized instruction missing %q", want)
		}
	}
}

func TestChunkReviewContext_ExplainsBoundaryHandling(t *testing.T) {
	got := ChunkReviewContext()
	for _, want := range []string{
		"partial excerpt",
		"leading_context and trailing_context",
		"line numbers relative to file_content",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("chunk review context missing %q", want)
		}
	}
}

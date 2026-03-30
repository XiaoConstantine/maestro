package terminal

import "testing"

func TestPlanInputModeLayoutDropsChromeOnShortPane(t *testing.T) {
	layout := planInputModeLayout(
		7,
		1, // compact logo
		4, // info section
		0, // no progress
		3, // single-line input with padding
		1, // status bar
	)

	if layout.showInfo {
		t.Fatalf("expected info section to be hidden on short pane")
	}
	if layout.showLogo {
		t.Fatalf("expected logo to be hidden on short pane")
	}
	if !layout.showStatus {
		t.Fatalf("expected status bar to remain visible when layout fits")
	}
	if layout.conversationHeight != 3 {
		t.Fatalf("conversationHeight = %d, want 3", layout.conversationHeight)
	}
}

func TestPlanInputModeLayoutDropsProgressBeforeStatus(t *testing.T) {
	layout := planInputModeLayout(
		8,
		0,
		0,
		2, // progress section
		3,
		1,
	)

	if layout.showProgress {
		t.Fatalf("expected progress section to be hidden before status")
	}
	if !layout.showStatus {
		t.Fatalf("expected status bar to remain visible")
	}
	if layout.conversationHeight < 3 {
		t.Fatalf("conversationHeight = %d, want at least 3", layout.conversationHeight)
	}
}

func TestInputTextareaHeightForPane(t *testing.T) {
	tests := []struct {
		height int
		want   int
	}{
		{height: 7, want: 1},
		{height: 10, want: 2},
		{height: 20, want: 3},
	}

	for _, tc := range tests {
		if got := inputTextareaHeightForPane(tc.height); got != tc.want {
			t.Fatalf("inputTextareaHeightForPane(%d) = %d, want %d", tc.height, got, tc.want)
		}
	}
}

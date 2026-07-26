package terminal

const (
	narrowLayoutWidth = 80
	wideLayoutWidth   = 120
)

type contentLayout struct {
	conversationWidth int
	railWidth         int
	height            int
	showRail          bool
}

func planContentLayout(width, height int, railRequested bool) contentLayout {
	plan := contentLayout{
		conversationWidth: max(1, width),
		height:            max(1, height),
	}
	if !railRequested || width < narrowLayoutWidth {
		return plan
	}

	railWidth := min(24, width/3)
	if width >= wideLayoutWidth {
		railWidth = min(40, max(32, width/4))
	}
	if width-railWidth < 40 {
		return plan
	}
	plan.showRail = true
	plan.railWidth = railWidth
	plan.conversationWidth = width - railWidth
	return plan
}

func composerHeightForPane(totalHeight int, value string) int {
	base := inputTextareaHeightForPane(totalHeight)
	lines := 1
	for _, r := range value {
		if r == '\n' {
			lines++
		}
	}
	capHeight := max(1, min(10, totalHeight*30/100))
	return min(capHeight, max(base, lines))
}

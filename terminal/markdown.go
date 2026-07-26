package terminal

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/glamour/v2"
	glamouransi "charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
)

func messageWidthBucket(width int) int {
	width = max(1, width)
	if width < 4 {
		return width
	}
	return width - width%4
}

func (m *MaestroModel) renderAssistantMessage(message *Message, width int) string {
	bucket := messageWidthBucket(width)
	if message.WidthBucket == bucket && message.renderedContent == message.Content {
		return message.Rendered
	}

	message.renderCount++
	styles := maestroMarkdownStyles(m.theme)
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(styles),
		glamour.WithWordWrap(bucket),
	)
	if err != nil {
		message.Rendered = message.Content
		message.WidthBucket = bucket
		message.renderedContent = message.Content
		return message.Rendered
	}
	rendered, err := renderer.Render(message.Content)
	if err != nil {
		rendered = message.Content
	}
	message.Rendered = strings.Trim(rendered, "\n")
	message.WidthBucket = bucket
	message.renderedContent = message.Content
	return message.Rendered
}

func maestroMarkdownStyles(theme *Theme) glamouransi.StyleConfig {
	zero := uint(0)
	if _, noColor := theme.TextPrimary.(lipgloss.NoColor); noColor {
		styles := glamourstyles.NoTTYStyleConfig
		styles.Document.Margin = &zero
		styles.CodeBlock.Margin = &zero
		return styles
	}
	styles := glamourstyles.DarkStyleConfig
	styles.Document.Margin = &zero
	styles.Document.Color = markdownColor(theme.TextPrimary)
	styles.Heading.Color = markdownColor(theme.LogoPrimary)
	for _, heading := range []*glamouransi.StyleBlock{
		&styles.H1,
		&styles.H2,
		&styles.H3,
		&styles.H4,
		&styles.H5,
		&styles.H6,
	} {
		heading.Color = markdownColor(theme.LogoPrimary)
		heading.BackgroundColor = nil
	}
	styles.Link.Color = markdownColor(theme.Accent)
	styles.LinkText.Color = markdownColor(theme.Accent)
	styles.Code.Color = markdownColor(theme.Code)
	styles.Code.BackgroundColor = markdownColor(theme.Surface)
	styles.CodeBlock.Margin = &zero
	styles.CodeBlock.Color = markdownColor(theme.Code)
	if styles.CodeBlock.Chroma != nil {
		chroma := *styles.CodeBlock.Chroma
		styles.CodeBlock.Chroma = &chroma
		styles.CodeBlock.Chroma.Text.Color = markdownColor(theme.Code)
		styles.CodeBlock.Chroma.Comment.Color = markdownColor(theme.Comment)
		styles.CodeBlock.Chroma.Keyword.Color = markdownColor(theme.Keyword)
		styles.CodeBlock.Chroma.KeywordReserved.Color = markdownColor(theme.Keyword)
		styles.CodeBlock.Chroma.KeywordType.Color = markdownColor(theme.Keyword)
		styles.CodeBlock.Chroma.LiteralString.Color = markdownColor(theme.String)
	}
	return styles
}

func markdownColor(value color.Color) *string {
	r, g, b, _ := value.RGBA()
	hex := fmt.Sprintf("#%02X%02X%02X", uint8(r>>8), uint8(g>>8), uint8(b>>8))
	return &hex
}

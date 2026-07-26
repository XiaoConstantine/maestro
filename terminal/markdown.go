package terminal

import (
	"strings"

	"charm.land/glamour/v2"
	glamouransi "charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
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
	styles := maestroMarkdownStyles()
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

func maestroMarkdownStyles() glamouransi.StyleConfig {
	styles := glamourstyles.DarkStyleConfig
	zero := uint(0)
	styles.Document.Margin = &zero
	styles.Document.Color = stringPointer("#E8E9F3")
	styles.Heading.Color = stringPointer("#E8985A")
	for _, heading := range []*glamouransi.StyleBlock{
		&styles.H1,
		&styles.H2,
		&styles.H3,
		&styles.H4,
		&styles.H5,
		&styles.H6,
	} {
		heading.Color = stringPointer("#E8985A")
		heading.BackgroundColor = nil
	}
	styles.Link.Color = stringPointer("#00D9FF")
	styles.LinkText.Color = stringPointer("#00D9FF")
	styles.Code.Color = stringPointer("#D4D4E5")
	styles.Code.BackgroundColor = stringPointer("#2B2D42")
	styles.CodeBlock.Margin = &zero
	styles.CodeBlock.Color = stringPointer("#D4D4E5")
	if styles.CodeBlock.Chroma != nil {
		chroma := *styles.CodeBlock.Chroma
		styles.CodeBlock.Chroma = &chroma
		styles.CodeBlock.Chroma.Text.Color = stringPointer("#D4D4E5")
		styles.CodeBlock.Chroma.Comment.Color = stringPointer("#6B6B7E")
		styles.CodeBlock.Chroma.Keyword.Color = stringPointer("#B185F7")
		styles.CodeBlock.Chroma.KeywordReserved.Color = stringPointer("#B185F7")
		styles.CodeBlock.Chroma.KeywordType.Color = stringPointer("#B185F7")
		styles.CodeBlock.Chroma.LiteralString.Color = stringPointer("#7FE9DE")
	}
	return styles
}

func stringPointer(value string) *string {
	return &value
}

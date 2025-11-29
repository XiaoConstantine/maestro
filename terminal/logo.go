package terminal

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

const diag = `╱`

// MaestroLogo renders the MAESTRO ASCII art logo similar to Crush style.
func MaestroLogo(width int, theme *Theme) string {
	// ASCII art for MAESTRO
	logo := `
███╗   ███╗ █████╗ ███████╗███████╗████████╗██████╗  ██████╗ 
████╗ ████║██╔══██╗██╔════╝██╔════╝╚══██╔══╝██╔══██╗██╔═══██╗
██╔████╔██║███████║█████╗  ███████╗   ██║   ██████╔╝██║   ██║
██║╚██╔╝██║██╔══██║██╔══╝  ╚════██║   ██║   ██╔══██╗██║   ██║
██║ ╚═╝ ██║██║  ██║███████╗███████║   ██║   ██║  ██║╚██████╔╝
╚═╝     ╚═╝╚═╝  ╚═╝╚══════╝╚══════╝   ╚═╝   ╚═╝  ╚═╝ ╚═════╝ `

	logoLines := strings.Split(strings.TrimPrefix(logo, "\n"), "\n")
	logoWidth := 0
	for _, line := range logoLines {
		if lipgloss.Width(line) > logoWidth {
			logoWidth = lipgloss.Width(line)
		}
	}

	// Apply gradient coloring to logo using Crush-style purple/magenta
	var coloredLogo strings.Builder
	for _, line := range logoLines {
		coloredLine := applyGradient(line, theme.LogoSecondary, theme.LogoPrimary)
		coloredLogo.WriteString(coloredLine)
		coloredLogo.WriteString("\n")
	}

	// Create diagonal field lines on the left
	leftWidth := 4
	rightWidth := max(10, width-logoWidth-leftWidth-4)

	fieldStyle := lipgloss.NewStyle().Foreground(theme.LogoField)
	leftField := strings.Builder{}
	rightField := strings.Builder{}

	for i := 0; i < len(logoLines); i++ {
		leftField.WriteString(fieldStyle.Render(strings.Repeat(diag, leftWidth)))
		leftField.WriteString("\n")

		rw := rightWidth - i
		if rw < 0 {
			rw = 0
		}
		rightField.WriteString(fieldStyle.Render(strings.Repeat(diag, rw)))
		rightField.WriteString("\n")
	}

	// Join horizontally
	result := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftField.String(),
		" ",
		coloredLogo.String(),
		" ",
		rightField.String(),
	)

	// Truncate to width if needed
	lines := strings.Split(result, "\n")
	for i, line := range lines {
		if lipgloss.Width(line) > width {
			lines[i] = truncateWithAnsi(line, width)
		}
	}

	return strings.Join(lines, "\n")
}

// MaestroLogoSmall renders a compact version for narrow terminals.
func MaestroLogoSmall(width int, theme *Theme) string {
	titleStyle := lipgloss.NewStyle().Foreground(theme.LogoSecondary).Bold(true)
	accentStyle := lipgloss.NewStyle().Foreground(theme.LogoPrimary)

	title := titleStyle.Render("◉ MAESTRO")
	remainingWidth := width - lipgloss.Width(title) - 2
	if remainingWidth > 0 {
		lines := strings.Repeat(diag, remainingWidth)
		title = fmt.Sprintf("%s %s", title, accentStyle.Render(lines))
	}
	return title
}

// applyGradient applies a simple two-color gradient to text.
func applyGradient(text string, colorA, colorB color.Color) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return text
	}

	var result strings.Builder
	mid := len(runes) / 2

	styleA := lipgloss.NewStyle().Foreground(colorA)
	styleB := lipgloss.NewStyle().Foreground(colorB)

	for i, r := range runes {
		if r == ' ' || r == '\n' {
			result.WriteRune(r)
			continue
		}
		if i < mid {
			result.WriteString(styleA.Render(string(r)))
		} else {
			result.WriteString(styleB.Render(string(r)))
		}
	}

	return result.String()
}

// truncateWithAnsi truncates a string with ANSI codes to a given width.
func truncateWithAnsi(s string, width int) string {
	if width <= 0 {
		return ""
	}
	currentWidth := 0
	var result strings.Builder
	inAnsi := false

	for _, r := range s {
		if r == '\x1b' {
			inAnsi = true
			result.WriteRune(r)
			continue
		}
		if inAnsi {
			result.WriteRune(r)
			if r == 'm' {
				inAnsi = false
			}
			continue
		}
		if currentWidth >= width {
			break
		}
		result.WriteRune(r)
		currentWidth++
	}

	return result.String()
}

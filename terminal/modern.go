package terminal

import (
	tea "github.com/charmbracelet/bubbletea"
)

// RunModernUI starts the modern terminal UI.
func RunModernUI() error {
	// Create model with modern UI enabled
	m := NewModern()

	// Create the program
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Run the program
	_, err := p.Run()
	return err
}

// RunLegacyUI starts the legacy terminal UI.
func RunLegacyUI() error {
	// Create model with legacy UI
	m := New()

	// Create the program
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Run the program
	_, err := p.Run()
	return err
}

// RunUI starts the appropriate UI based on the modern flag.
func RunUI(modern bool) error {
	if modern {
		return RunModernUI()
	}
	return RunLegacyUI()
}

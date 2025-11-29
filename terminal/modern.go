package terminal

import (
	tea "charm.land/bubbletea/v2"
)

// RunModernUI starts the modern terminal UI.
func RunModernUI() error {
	// Create model with modern UI enabled
	m := NewModern()

	// Create the program
	// In v2, alt screen and mouse are controlled via View() return value
	p := tea.NewProgram(m)

	// Run the program
	_, err := p.Run()
	return err
}

// RunLegacyUI starts the legacy terminal UI.
func RunLegacyUI() error {
	// Create model with legacy UI
	m := New()

	// Create the program
	// In v2, alt screen is controlled via View() return value
	p := tea.NewProgram(m)

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

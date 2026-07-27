package terminal

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// reduce applies one translated action to model state. Async work is returned
// as a Bubble Tea command and is never executed on the dispatch path.
func (m *MaestroModel) reduce(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.restoreConversationTail = m.restoreConversationTail || m.viewport.AtBottom()
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true

		// Forward to sub-models
		if m.inputModel != nil {
			m.inputModel.SetSize(m.width, composerHeightForPane(m.height, m.inputModel.Value()))
			m.inputModel.SetSuggestionLimit(suggestionLimitForPane(m.height))
		}
		if m.statusBar != nil {
			newSB, cmd := m.statusBar.Update(msg)
			m.statusBar = &newSB
			cmds = append(cmds, cmd)
		}
		if m.commandPalette != nil {
			newCP, cmd := m.commandPalette.Update(msg)
			m.commandPalette = &newCP
			cmds = append(cmds, cmd)
		}

	case tea.MouseWheelMsg:
		target := &m.viewport
		if m.inputFocus == FocusToolActivity && m.contextRailActive() {
			target = &m.railViewport
		}
		switch msg.Button {
		case tea.MouseWheelUp:
			target.ScrollUp(3)
		case tea.MouseWheelDown:
			target.ScrollDown(3)
		}
		if target == &m.railViewport {
			m.railFollowTail = m.railViewport.AtBottom()
		}

	case tea.KeyPressMsg:
		if (m.mode == ModeSessionPicker || m.mode == ModeModelPicker) &&
			(msg.String() == "tab" || msg.String() == "ctrl+\\") {
			return m, nil
		}
		// Global key handling
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "ctrl+p":
			if m.mode == ModeSessionPicker || m.mode == ModeModelPicker {
				return m, nil
			}
			// Toggle command palette
			if m.commandPalette.IsVisible() {
				m.commandPalette.Hide()
			} else {
				m.pickerLoadID++
				m.commandPalette.Show()
			}
			return m, nil

		case "ctrl+m":
			if m.commandPalette.IsVisible() || m.mode == ModeSessionPicker || m.mode == ModeModelPicker {
				return m, nil
			}
			return m, m.cmdModelList()

		case "ctrl+\\":
			if m.hasRailContent() {
				m.restoreConversationTail = m.restoreConversationTail || m.viewport.AtBottom()
				m.railVisible = !m.railVisible
				m.renderMessages()
			}
			return m, nil

		case "esc":
			m.pickerLoadID++
			// Overlays close before Esc is interpreted as run cancellation.
			if m.commandPalette.IsVisible() {
				m.commandPalette.Hide()
				return m, nil
			}
			if m.mode == ModeSessionPicker || m.mode == ModeModelPicker {
				m.mode = ModeInput
				m.sessionPickerSessions = nil
				m.sessionPickerIdx = 0
				m.modelPickerModels = nil
				m.modelPickerIdx = 0
				m.pickerFilter = ""
				m.statusBar.SetMode("")
				m.setInputFocus(FocusInput)
				return m, nil
			}
			if m.codingRunActive {
				cancel := m.codingCancel
				backend := m.backend
				m.progressModel.SetMessage("Canceling…")
				m.statusBar.SetMessage("Waiting for the active run to stop")
				return m, func() tea.Msg {
					if cancel != nil {
						cancel()
					}
					if backend != nil {
						backend.CancelCodingTask()
					}
					return nil
				}
			}
			// Close review detail view before dismissing all review results.
			if m.reviewModel != nil && m.reviewModel.showDetail {
				m.reviewModel.showDetail = false
				m.reviewModel.updateViewportSizes()
				m.renderMessages()
				return m, nil
			}
			if m.inputFocus == FocusToolActivity {
				m.setInputFocus(FocusInput)
				m.renderMessages()
				return m, nil
			}
			// Clear review results
			if len(m.reviewResults) > 0 {
				m.reviewResults = nil
				m.reviewModel = nil
				if !m.toolActivity.HasEntries() {
					m.railVisible = false
				}
				m.setInputFocus(FocusInput)
				m.renderMessages()
				return m, nil
			}

		case "tab":
			if m.inputFocus == FocusInput && m.inputModel.HasActiveSuggestions() {
				break
			}
			if (len(m.reviewResults) > 0 || m.toolActivity.HasTools()) && !m.commandPalette.IsVisible() {
				m.changeFocus()
				m.renderMessages()
				return m, nil
			}
		}

		// Handle session picker navigation
		if m.mode == ModeSessionPicker {
			filtered := m.filteredSessions()
			if m.updatePickerFilter(msg) {
				m.sessionPickerIdx = 0
				return m, nil
			}
			if m.sessionPickerCapacity() == 0 {
				return m, nil
			}
			switch msg.String() {
			case "down":
				if len(filtered) == 0 {
					return m, nil
				}
				m.sessionPickerIdx++
				if m.sessionPickerIdx >= len(filtered) {
					m.sessionPickerIdx = 0
				}
				m.renderMessages()
				return m, nil
			case "up":
				if len(filtered) == 0 {
					return m, nil
				}
				m.sessionPickerIdx--
				if m.sessionPickerIdx < 0 {
					m.sessionPickerIdx = len(filtered) - 1
				}
				m.renderMessages()
				return m, nil
			case "enter":
				if len(filtered) == 0 {
					return m, nil
				}
				if m.codingRunActive || m.sessionMutationPending || m.modelSelectionPending || m.specialistRuns > 0 {
					return m, func() tea.Msg { return ErrorMsg{Error: fmt.Errorf("session changes require an idle coding session")} }
				}
				m.sessionMutationPending = true
				requestID := m.nextPickerRequestID()
				selected := filtered[m.sessionPickerIdx]
				return m, m.cmdSessionSwitch(selected.Name, requestID)
			}
		}

		if m.mode == ModeModelPicker {
			filtered := m.filteredModels()
			if m.updatePickerFilter(msg) {
				m.modelPickerIdx = 0
				return m, nil
			}
			if m.modelPickerCapacity() == 0 {
				return m, nil
			}
			switch msg.String() {
			case "down":
				if len(filtered) == 0 {
					return m, nil
				}
				m.modelPickerIdx = (m.modelPickerIdx + 1) % len(filtered)
				return m, nil
			case "up":
				if len(filtered) == 0 {
					return m, nil
				}
				m.modelPickerIdx = (m.modelPickerIdx - 1 + len(filtered)) % len(filtered)
				return m, nil
			case "enter":
				if len(filtered) == 0 {
					return m, nil
				}
				if m.codingRunActive || m.modelSelectionPending || m.sessionMutationPending || m.specialistRuns > 0 {
					return m, func() tea.Msg { return ErrorMsg{Error: fmt.Errorf("model changes require an idle coding session")} }
				}
				m.modelSelectionPending = true
				requestID := m.nextPickerRequestID()
				return m, m.cmdModelSelect(filtered[m.modelPickerIdx].ID, requestID)
			}
		}

		if m.mode == ModeSessionPicker || m.mode == ModeModelPicker {
			return m, nil
		}

		if m.inputFocus == FocusToolActivity && m.toolActivity.HasTools() && !m.commandPalette.IsVisible() {
			switch msg.String() {
			case "j", "down":
				m.toolActivity.Move(1)
				m.renderMessages()
				m.ensureToolSelectionVisible()
				return m, nil
			case "k", "up":
				m.toolActivity.Move(-1)
				m.renderMessages()
				m.ensureToolSelectionVisible()
				return m, nil
			case "enter", " ":
				m.toolActivity.ToggleSelected()
				m.renderMessages()
				return m, nil
			case "esc":
				m.setInputFocus(FocusInput)
				m.renderMessages()
				return m, nil
			}
		}

		if m.reviewModel != nil && m.inputFocus == FocusReviewList && !m.commandPalette.IsVisible() {
			updated, cmd := m.reviewModel.Update(msg)
			m.reviewModel = updated.(*ReviewModel)
			m.renderMessages()
			return m, cmd
		}

		// Route to command palette if visible
		if m.commandPalette.IsVisible() {
			newCP, cmd := m.commandPalette.Update(msg)
			m.commandPalette = &newCP
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}

		// Route to input model
		newInput, cmd := m.inputModel.Update(msg)
		m.inputModel = newInput
		cmds = append(cmds, cmd)

	case CommandResultMsg:
		// Handle command execution result
		if msg.Error != nil {
			m.addMessage("system", fmt.Sprintf("Error: %v", msg.Error))
		} else if msg.Result != "" {
			m.addMessage("assistant", msg.Result)
		}

	case ExecuteCommandMsg:
		// Execute command from palette via handleCommand
		return m, m.handleCommand(msg.Command, msg.Args)

	case InsertCommandMsg:
		// Insert command into the composer for the user to complete with args.
		m.inputModel.SetValue(msg.Command)
		m.setInputFocus(FocusInput)

	case SpecialistResultMsg:
		if m.specialistRuns > 0 {
			m.specialistRuns--
		}
		m.progressModel.Hide()
		if msg.Error != nil {
			m.addMessage("system", fmt.Sprintf("Error: %v", msg.Error))
		} else {
			m.addMessage("assistant", msg.Content)
		}

	case ResponseMsg:
		// Handle async response from backend
		m.addMessage("assistant", msg.Content)
		m.statusBar.SetMessage("")
		m.progressModel.Hide() // Ensure progress is hidden when response arrives

	case CodingEventMsg:
		followTail := m.viewport.AtBottom()
		if !m.toolActivity.Apply(msg.Event) {
			break
		}
		if msg.Event.Kind == "run" && msg.Event.Status == "started" {
			m.toolActivityAnchor = len(m.messages)
			m.restoreConversationTail = m.restoreConversationTail || m.viewport.AtBottom()
			m.railFollowTail = true
			m.railVisible = true
		}
		if msg.Event.Kind == "run" && msg.Event.Status != "started" {
			m.progressModel.Hide()
		} else {
			status := msg.Event.Detail
			if status == "" {
				status = msg.Event.Status
			}
			if !m.progressModel.IsVisible() {
				cmds = append(cmds, m.progressModel.Start(status))
			} else {
				m.progressModel.SetMessage(status)
			}
			if msg.Event.Tool != "" {
				m.progressModel.SetDetail(msg.Event.Tool)
			}
		}
		m.renderMessages()
		if m.inputFocus == FocusToolActivity && (!m.contextRailActive() || m.railFollowTail) {
			m.ensureToolSelectionVisible()
		} else if m.inputFocus != FocusToolActivity && followTail {
			m.viewport.GotoBottom()
		}

	case CodingResultMsg:
		followTail := m.viewport.AtBottom()
		previousOffset := m.viewport.YOffset()
		cancel := m.codingCancel
		m.codingCancel = nil
		if cancel != nil {
			cmds = append(cmds, func() tea.Msg {
				cancel()
				return nil
			})
		}
		m.codingRunActive = false
		m.statusBar.SetMessage("")
		m.progressModel.Hide()
		if msg.Error != nil {
			m.addMessage("system", fmt.Sprintf("Error: %v", msg.Error))
		} else {
			m.addMessage("assistant", msg.Content)
		}
		if m.inputFocus == FocusToolActivity && (!m.contextRailActive() || m.railFollowTail) {
			m.ensureToolSelectionVisible()
		} else if m.inputFocus != FocusToolActivity && !followTail {
			m.viewport.SetYOffset(previousOffset)
		}

	case SessionPickerMsg:
		if msg.RequestID != m.pickerLoadID || m.commandPalette.IsVisible() || m.mode != ModeInput {
			break
		}
		if msg.Error != nil {
			m.addMessage("system", fmt.Sprintf("Error: %v", msg.Error))
			break
		}
		if len(msg.Sessions) == 0 {
			m.addMessage("assistant", "No sessions found.")
			break
		}
		// Enter session picker mode.
		m.commandPalette.Hide()
		m.sessionPickerSessions = msg.Sessions
		m.sessionPickerIdx = 0
		m.pickerFilter = ""
		// Find current session and select it
		for i, s := range msg.Sessions {
			if s.IsCurrent {
				m.sessionPickerIdx = i
				break
			}
		}
		m.mode = ModeSessionPicker
		m.setInputFocus(FocusInput)
		m.statusBar.SetMode(ModeSessionPicker.String())
		m.inputModel.Blur()
		m.renderMessages()

	case ModelPickerMsg:
		if msg.RequestID != m.pickerLoadID || m.commandPalette.IsVisible() || m.mode != ModeInput {
			break
		}
		if msg.Error != nil {
			m.addMessage("system", fmt.Sprintf("Error: %v", msg.Error))
			break
		}
		if len(msg.Models) == 0 {
			m.addMessage("system", "No switchable models are configured.")
			break
		}
		m.commandPalette.Hide()
		m.modelPickerModels = msg.Models
		m.modelPickerIdx = 0
		m.pickerFilter = ""
		for i, option := range msg.Models {
			if option.Current {
				m.modelPickerIdx = i
				break
			}
		}
		m.mode = ModeModelPicker
		m.statusBar.SetMode(ModeModelPicker.String())
		m.setInputFocus(FocusInput)
		m.inputModel.Blur()

	case ModelSelectedMsg:
		if msg.RequestID != m.pickerRequestID || !m.modelSelectionPending {
			break
		}
		m.modelSelectionPending = false
		if msg.Error != nil {
			m.addMessage("system", fmt.Sprintf("Error: %v", msg.Error))
			break
		}
		m.mode = ModeInput
		m.modelPickerModels = nil
		m.modelPickerIdx = 0
		m.pickerFilter = ""
		m.setInputFocus(FocusInput)
		m.addMessage("assistant", fmt.Sprintf("Model changed to %s for the next run.", msg.ID))

	case SessionMutationMsg:
		if msg.RequestID != m.pickerRequestID || !m.sessionMutationPending {
			break
		}
		m.sessionMutationPending = false
		m.progressModel.Hide()
		if msg.Error != nil {
			m.addMessage("system", fmt.Sprintf("Error: %v", msg.Error))
			break
		}
		m.mode = ModeInput
		m.sessionPickerSessions = nil
		m.sessionPickerIdx = 0
		m.pickerFilter = ""
		m.resetSessionUI()
		action := "Switched to"
		if msg.Created {
			action = "Created and switched to"
		}
		m.addMessage("assistant", fmt.Sprintf("%s session: %s", action, msg.Name))

	case ReviewFailedMsg:
		if m.specialistRuns > 0 {
			m.specialistRuns--
		}
		m.progressModel.Hide()
		m.addMessage("system", fmt.Sprintf("Error: %v", msg.Error))

	case ReviewResultMsg:
		if m.specialistRuns > 0 {
			m.specialistRuns--
		}
		// Store review results for inline display (Crush-style)
		m.reviewResults = msg.Comments
		m.reviewModel = NewEmbeddedReviewModel(msg.Comments, m.theme)
		m.reviewModel.SetSize(max(1, m.viewport.Width()), max(6, m.viewport.Height()))
		m.restoreConversationTail = m.restoreConversationTail || m.viewport.AtBottom()
		m.railVisible = true
		// Focus on review list when results arrive
		m.setInputFocus(FocusReviewList)
		// Add a summary message
		counts := m.getReviewCounts()
		summary := fmt.Sprintf("Review complete: %d comments", counts["total"])
		if counts["critical"] > 0 {
			summary += fmt.Sprintf(" (%d critical)", counts["critical"])
		} else if counts["high"] > 0 {
			summary += fmt.Sprintf(" (%d high)", counts["high"])
		}
		m.addMessage("assistant", summary)

	case ErrorMsg:
		m.addMessage("system", fmt.Sprintf("Error: %v", msg.Error))
		m.statusBar.SetMessage("")

	case ProgressMsg:
		// Update progress display with status
		if msg.Status != "" {
			// Start or update progress
			if !m.progressModel.IsVisible() {
				cmd := m.progressModel.Start(msg.Status)
				cmds = append(cmds, cmd)
			} else {
				m.progressModel.SetMessage(msg.Status)
			}
			if msg.Detail != "" {
				m.progressModel.SetDetail(msg.Detail)
			}
		} else {
			// Empty status means hide progress
			m.progressModel.Hide()
		}

	case ProgressTickMsg:
		// Forward to progress model
		newProgress, cmd := m.progressModel.Update(msg)
		m.progressModel = newProgress
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

package terminal

import tea "charm.land/bubbletea/v2"

// Action is a closed, synchronous input to Maestro's state reducer. Concrete
// actions carry typed payloads and never perform terminal, backend, network, or
// filesystem I/O.
type Action interface {
	message() tea.Msg
	isAction()
}

type KeyAction struct{ Key tea.KeyPressMsg }

func (a KeyAction) message() tea.Msg { return a.Key }
func (KeyAction) isAction()          {}

type ResizeAction struct{ Size tea.WindowSizeMsg }

func (a ResizeAction) message() tea.Msg { return a.Size }
func (ResizeAction) isAction()          {}

type ScrollAction struct{ Wheel tea.MouseWheelMsg }

func (a ScrollAction) message() tea.Msg { return a.Wheel }
func (ScrollAction) isAction()          {}

// taskResult is the closed set of asynchronous application results accepted by
// TaskResultAction. Adding a result message requires updating this union,
// actionFromMessage, and Dispatch.
type taskResult interface {
	CommandResultMsg | ExecuteCommandMsg | InsertCommandMsg |
		SpecialistResultMsg | ResponseMsg | CodingEventMsg | CodingResultMsg |
		SessionPickerMsg | ModelPickerMsg | ModelSelectedMsg | SessionMutationMsg |
		ReviewFailedMsg | ReviewResultMsg | ErrorMsg | ProgressMsg
}

type TaskResultAction[T taskResult] struct{ Result T }

func (a TaskResultAction[T]) message() tea.Msg { return a.Result }
func (TaskResultAction[T]) isAction()          {}

type ProgressTickAction struct{ Tick ProgressTickMsg }

func (a ProgressTickAction) message() tea.Msg { return a.Tick }
func (ProgressTickAction) isAction()          {}

// UnknownFrameworkAction is explicit and intentionally reduces to no work.
// Unknown Bubble Tea messages cannot masquerade as application task results.
type UnknownFrameworkAction struct{ Message tea.Msg }

func (a UnknownFrameworkAction) message() tea.Msg { return a.Message }
func (UnknownFrameworkAction) isAction()          {}

// Effect describes work that must execute after deterministic dispatch.
type Effect interface {
	command() tea.Cmd
	isEffect()
}

// CommandEffect adapts a Bubble Tea command at the framework boundary. Backend
// calls remain inside the command and therefore cannot run during Dispatch.
type CommandEffect struct{ Command tea.Cmd }

func (e CommandEffect) command() tea.Cmd { return e.Command }
func (CommandEffect) isEffect()          {}

func actionFromMessage(msg tea.Msg) Action {
	switch value := msg.(type) {
	case tea.KeyPressMsg:
		return KeyAction{Key: value}
	case tea.WindowSizeMsg:
		return ResizeAction{Size: value}
	case tea.MouseWheelMsg:
		return ScrollAction{Wheel: value}
	case CommandResultMsg:
		return TaskResultAction[CommandResultMsg]{Result: value}
	case ExecuteCommandMsg:
		return TaskResultAction[ExecuteCommandMsg]{Result: value}
	case InsertCommandMsg:
		return TaskResultAction[InsertCommandMsg]{Result: value}
	case SpecialistResultMsg:
		return TaskResultAction[SpecialistResultMsg]{Result: value}
	case ResponseMsg:
		return TaskResultAction[ResponseMsg]{Result: value}
	case CodingEventMsg:
		return TaskResultAction[CodingEventMsg]{Result: value}
	case CodingResultMsg:
		return TaskResultAction[CodingResultMsg]{Result: value}
	case SessionPickerMsg:
		return TaskResultAction[SessionPickerMsg]{Result: value}
	case ModelPickerMsg:
		return TaskResultAction[ModelPickerMsg]{Result: value}
	case ModelSelectedMsg:
		return TaskResultAction[ModelSelectedMsg]{Result: value}
	case SessionMutationMsg:
		return TaskResultAction[SessionMutationMsg]{Result: value}
	case ReviewFailedMsg:
		return TaskResultAction[ReviewFailedMsg]{Result: value}
	case ReviewResultMsg:
		return TaskResultAction[ReviewResultMsg]{Result: value}
	case ErrorMsg:
		return TaskResultAction[ErrorMsg]{Result: value}
	case ProgressMsg:
		return TaskResultAction[ProgressMsg]{Result: value}
	case ProgressTickMsg:
		return ProgressTickAction{Tick: value}
	default:
		return UnknownFrameworkAction{Message: msg}
	}
}

// Dispatch synchronously applies a closed action variant and describes any
// resulting work. Effects remain inert until executeEffects runs in Update.
func (m *MaestroModel) Dispatch(action Action) []Effect {
	var msg tea.Msg
	switch value := action.(type) {
	case KeyAction:
		msg = value.Key
	case ResizeAction:
		msg = value.Size
	case ScrollAction:
		msg = value.Wheel
	case TaskResultAction[CommandResultMsg]:
		msg = value.Result
	case TaskResultAction[ExecuteCommandMsg]:
		msg = value.Result
	case TaskResultAction[InsertCommandMsg]:
		msg = value.Result
	case TaskResultAction[SpecialistResultMsg]:
		msg = value.Result
	case TaskResultAction[ResponseMsg]:
		msg = value.Result
	case TaskResultAction[CodingEventMsg]:
		msg = value.Result
	case TaskResultAction[CodingResultMsg]:
		msg = value.Result
	case TaskResultAction[SessionPickerMsg]:
		msg = value.Result
	case TaskResultAction[ModelPickerMsg]:
		msg = value.Result
	case TaskResultAction[ModelSelectedMsg]:
		msg = value.Result
	case TaskResultAction[SessionMutationMsg]:
		msg = value.Result
	case TaskResultAction[ReviewFailedMsg]:
		msg = value.Result
	case TaskResultAction[ReviewResultMsg]:
		msg = value.Result
	case TaskResultAction[ErrorMsg]:
		msg = value.Result
	case TaskResultAction[ProgressMsg]:
		msg = value.Result
	case ProgressTickAction:
		msg = value.Tick
	case UnknownFrameworkAction, nil:
		return nil
	default:
		return nil
	}
	_, cmd := m.reduce(msg)
	if cmd == nil {
		return nil
	}
	return []Effect{CommandEffect{Command: cmd}}
}

func executeEffects(effects []Effect) tea.Cmd {
	commands := make([]tea.Cmd, 0, len(effects))
	for _, effect := range effects {
		if effect == nil {
			continue
		}
		if cmd := effect.command(); cmd != nil {
			commands = append(commands, cmd)
		}
	}
	return tea.Batch(commands...)
}

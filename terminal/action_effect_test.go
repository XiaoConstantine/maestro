package terminal

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type dispatchProbeBackend struct {
	*NoOpBackend
	listModelCalls int
	cancelCalls    int
}

func (b *dispatchProbeBackend) ListModels(context.Context) ([]ModelOption, error) {
	b.listModelCalls++
	return []ModelOption{{ID: "model-a"}}, nil
}

func (b *dispatchProbeBackend) SetModel(context.Context, string) error { return nil }

func (b *dispatchProbeBackend) CancelCodingTask() bool {
	b.cancelCalls++
	return true
}

func TestDispatchDescribesBackendWorkWithoutExecutingIt(t *testing.T) {
	backend := &dispatchProbeBackend{NoOpBackend: NewNoOpBackend("owner", "repo")}
	model := NewMaestroModel(&MaestroConfig{}, backend)

	effects := model.Dispatch(KeyAction{Key: tea.KeyPressMsg{Code: 'm', Mod: tea.ModCtrl}})
	if backend.listModelCalls != 0 {
		t.Fatalf("Dispatch performed backend I/O: ListModels calls = %d", backend.listModelCalls)
	}
	if len(effects) != 1 {
		t.Fatalf("effects = %d, want one model-list effect", len(effects))
	}

	executeTestCommand(executeEffects(effects))
	if backend.listModelCalls != 1 {
		t.Fatalf("executed effects made %d ListModels calls, want 1", backend.listModelCalls)
	}
}

func TestDispatchDefersCancellationEffect(t *testing.T) {
	backend := &dispatchProbeBackend{NoOpBackend: NewNoOpBackend("owner", "repo")}
	model := NewMaestroModel(&MaestroConfig{}, backend)
	runContext, cancel := context.WithCancel(context.Background())
	model.codingRunActive = true
	model.codingCancel = cancel

	effects := model.Dispatch(KeyAction{Key: tea.KeyPressMsg{Code: tea.KeyEscape}})
	if backend.cancelCalls != 0 || runContext.Err() != nil {
		t.Fatal("Dispatch executed cancellation instead of describing it")
	}

	executeTestCommand(executeEffects(effects))
	if backend.cancelCalls != 1 || runContext.Err() == nil {
		t.Fatalf("cancellation effect = backend calls:%d context error:%v", backend.cancelCalls, runContext.Err())
	}
}

func TestCodingResultDispatchDefersContextCancellation(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	runContext, cancel := context.WithCancel(context.Background())
	model.codingRunActive = true
	model.codingCancel = cancel

	effects := model.Dispatch(TaskResultAction[CodingResultMsg]{Result: CodingResultMsg{Content: "done"}})
	if runContext.Err() != nil {
		t.Fatal("coding-result dispatch canceled context synchronously")
	}
	if model.codingRunActive || model.codingCancel != nil {
		t.Fatal("coding-result dispatch did not synchronously settle run state")
	}
	executeTestCommand(executeEffects(effects))
	if runContext.Err() == nil {
		t.Fatal("coding-result cancellation effect did not run")
	}
}

func TestDispatchUsesInjectedClockForDeterministicState(t *testing.T) {
	fixed := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.now = func() time.Time { return fixed }
	model.progressModel.now = model.now

	_ = model.Dispatch(TaskResultAction[ResponseMsg]{Result: ResponseMsg{Content: "answer"}})
	if got := model.messages[len(model.messages)-1].Timestamp; !got.Equal(fixed) {
		t.Fatalf("message timestamp = %v, want %v", got, fixed)
	}
	_ = model.Dispatch(TaskResultAction[ProgressMsg]{Result: ProgressMsg{Status: "working"}})
	if !model.progressModel.start.Equal(fixed) {
		t.Fatalf("progress start = %v, want %v", model.progressModel.start, fixed)
	}
}

func TestActionTranslationSeparatesInputResultsAndFrameworkMessages(t *testing.T) {
	if _, ok := actionFromMessage(tea.KeyPressMsg{Code: 'x'}).(KeyAction); !ok {
		t.Fatal("key press did not translate to KeyAction")
	}
	if _, ok := actionFromMessage(CodingResultMsg{Content: "done"}).(TaskResultAction[CodingResultMsg]); !ok {
		t.Fatal("coding result did not translate to TaskResultAction")
	}
	if _, ok := actionFromMessage(ProgressTickMsg{}).(ProgressTickAction); !ok {
		t.Fatal("progress tick did not translate to ProgressTickAction")
	}
}

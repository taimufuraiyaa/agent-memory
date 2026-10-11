package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type fakeJevCredentials struct {
	token string
	err   error
}

func (f *fakeJevCredentials) Configured(context.Context) (bool, error) {
	return f.token != "", f.err
}
func (f *fakeJevCredentials) Save(_ context.Context, token string) error {
	if f.err == nil {
		f.token = token
	}
	return f.err
}
func (f *fakeJevCredentials) Clear(context.Context) error {
	if f.err == nil {
		f.token = ""
	}
	return f.err
}

func TestJevTokenEntryIsHiddenAndCanBeReplacedOrRemoved(t *testing.T) {
	store := &fakeJevCredentials{}
	model := resize(NewModelWithCredentials(context.Background(), "agent-memory", nil, store), 90, 24)
	updated, _ := model.Update(loadJevStatusCmd(context.Background(), store, model.jevGeneration)())
	model = updated.(Model)
	model, _ = updateKey(model, press('4', "4"))
	model, _ = updateKey(model, press('s', "s"))
	model, _ = updateKey(model, press('a', "jev-secret-token"))
	plain := ansi.Strip(model.View().Content)
	if strings.Contains(plain, "jev-secret-token") || !strings.Contains(plain, "[hidden input]") {
		t.Fatalf("token entry was exposed or invisible: %q", plain)
	}
	model, command := updateKey(model, press(tea.KeyEnter, ""))
	if command == nil || model.jevDraft != "" {
		t.Fatal("save command missing or draft not cleared")
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if store.token != "jev-secret-token" || !model.jevConfigured {
		t.Fatal("token not saved")
	}
	updated, _ = model.Update(jevStatusMsg{generation: 1, configured: false})
	model = updated.(Model)
	if !model.jevConfigured {
		t.Fatal("stale status overwrote saved credential state")
	}
	plain = ansi.Strip(model.View().Content)
	if strings.Contains(plain, store.token) || !strings.Contains(plain, "not verified with Jev") {
		t.Fatal("configured screen leaked token or claimed validation")
	}
	model, _ = updateKey(model, press('x', "x"))
	model, _ = updateKey(model, press('n', "n"))
	if store.token == "" {
		t.Fatal("removal happened without confirmation")
	}
	model, _ = updateKey(model, press('x', "x"))
	model, command = updateKey(model, press('y', "y"))
	if command == nil {
		t.Fatal("confirmed removal did not issue command")
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if store.token != "" || model.jevConfigured {
		t.Fatal("token not removed")
	}
}

func TestJevTokenCancelAndErrorDoNotLeak(t *testing.T) {
	store := &fakeJevCredentials{err: errors.New("secret-in-error")}
	model := resize(NewModelWithCredentials(context.Background(), "agent-memory", nil, store), 90, 24)
	updated, _ := model.Update(loadJevStatusCmd(context.Background(), store, model.jevGeneration)())
	model = updated.(Model)
	model, _ = updateKey(model, press('4', "4"))
	if strings.Contains(ansi.Strip(model.View().Content), "secret-in-error") {
		t.Fatal("status error leaked backend details")
	}
	model, _ = updateKey(model, press('s', "s"))
	model, _ = updateKey(model, press('x', "secret-draft"))
	model, _ = updateKey(model, press(tea.KeyEsc, ""))
	if model.jevDraft != "" || model.jevInput {
		t.Fatal("cancel retained draft")
	}
	model, _ = updateKey(model, press('s', "s"))
	model, _ = updateKey(model, press('x', "secret-draft"))
	model, command := updateKey(model, press(tea.KeyEnter, ""))
	updated, _ = model.Update(command())
	model = updated.(Model)
	plain := ansi.Strip(model.View().Content)
	if strings.Contains(plain, "secret-draft") || strings.Contains(plain, "secret-in-error") {
		t.Fatal("mutation error leaked secret")
	}
}

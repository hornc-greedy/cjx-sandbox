package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// A form needs both ends of the terminal. A prompt that starts without one
// waits for keys nobody can see it asking for, so it must refuse instead.
func TestRunFormRefusesWithoutATerminal(t *testing.T) {
	if err := runForm(); err == nil {
		t.Error("runForm started a form in a process that has no terminal")
	}
}

// Whitespace is not an answer; the message names what is still missing.
func TestNonEmpty(t *testing.T) {
	validate := NonEmpty("a name")
	for _, blank := range []string{"", " ", "\t\n"} {
		err := validate(blank)
		if err == nil {
			t.Errorf("%q was accepted as an answer", blank)
			continue
		}
		if !strings.Contains(err.Error(), "a name") {
			t.Errorf("the message does not say what is missing: %v", err)
		}
	}
	if err := validate(" playwright "); err != nil {
		t.Errorf("a real answer was rejected: %v", err)
	}
}

// Walking out of a menu is not a failure; anything else still is.
func TestIgnoreUserAbort(t *testing.T) {
	if err := IgnoreUserAbort(ErrAborted); err != nil {
		t.Errorf("leaving a menu was reported as a failure: %v", err)
	}
	if err := IgnoreUserAbort(fmt.Errorf("wrapped: %w", ErrAborted)); err != nil {
		t.Errorf("a wrapped abort was reported as a failure: %v", err)
	}
	fault := errors.New("the engine is not installed")
	if err := IgnoreUserAbort(fault); !errors.Is(err, fault) {
		t.Errorf("a real error was swallowed: %v", err)
	}
	if IgnoreUserAbort(nil) != nil {
		t.Error("nil turned into an error")
	}
}

// The back key has to be both bound and named: huh renders help only for the
// slots a field uses, so a label in the wrong slot is silently invisible.
func TestMenuLegendNamesTheBackKey(t *testing.T) {
	var idx int
	form := huh.NewForm(huh.NewGroup(huh.NewSelect[int]().
		Options(huh.NewOption("only", 0)).Value(&idx))).WithKeyMap(menuKeys())
	form.Init()
	for _, want := range []string{"← back", "/ filter"} {
		if !strings.Contains(form.View(), want) {
			t.Errorf("the legend does not name %q:\n%s", want, form.View())
		}
	}
	filtering := form
	filtering.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !strings.Contains(filtering.View(), "set filter") {
		t.Errorf("the back key took the filter with it:\n%s", filtering.View())
	}

	form.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if form.State != huh.StateAborted {
		t.Errorf("← left the form in state %v, want aborted", form.State)
	}
}

// PickMany is a menu too: the checklists have to name and honour the same key.
func TestPickManyLegendNamesTheBackKey(t *testing.T) {
	var picked []string
	form := huh.NewForm(huh.NewGroup(huh.NewMultiSelect[string]().
		Options(huh.NewOption("only", "only")).Value(&picked))).WithKeyMap(menuKeys())
	form.Init()
	if !strings.Contains(form.View(), "← back") {
		t.Errorf("the legend does not name the back key:\n%s", form.View())
	}
	form.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if form.State != huh.StateAborted {
		t.Errorf("← left the form in state %v, want aborted", form.State)
	}
}

// A text prompt cannot use ← for anything but the cursor, and huh leaves a
// one-field input a single help slot, so the submit entry has to name Esc too.
func TestAskLegendNamesTheBackKey(t *testing.T) {
	var value string
	form := huh.NewForm(huh.NewGroup(huh.NewInput().Title("Host").Value(&value))).
		WithKeyMap(askKeys())
	form.Init()
	if !strings.Contains(form.View(), "esc back") {
		t.Errorf("the legend does not name the back key:\n%s", form.View())
	}
	form.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("api.x")})
	form.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if form.State != huh.StateNormal {
		t.Error("← left the prompt instead of moving the cursor")
	}
	form.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if form.State != huh.StateAborted {
		t.Errorf("Esc left the prompt in state %v, want aborted", form.State)
	}
}

// huh puts Enter on both Open and Submit, which renders as "enter open • enter
// submit". Enter selects here, so only the arrow may claim "open".
func TestPickerLegendNamesOneKeyPerAction(t *testing.T) {
	var dir string
	form := huh.NewForm(huh.NewGroup(huh.NewFilePicker().
		CurrentDirectory("/").DirAllowed(true).FileAllowed(false).
		Picking(true).Value(&dir))).WithKeyMap(pickerKeys())
	form.Init()
	view := form.View()
	if strings.Contains(view, "enter open") {
		t.Errorf("two entries claim Enter:\n%s", view)
	}
	for _, want := range []string{"→ open", "enter select"} {
		if !strings.Contains(view, want) {
			t.Errorf("the legend does not name %q:\n%s", want, view)
		}
	}
}

// Spin runs its work and hands back what that work returned, terminal or not.
func TestSpinRunsTheWork(t *testing.T) {
	want := errors.New("from the work")
	ran := false
	got := Spin("working", func() error {
		ran = true
		return want
	})
	if !ran {
		t.Error("Spin did not run the work")
	}
	if !errors.Is(got, want) {
		t.Errorf("Spin returned %v, want %v", got, want)
	}
}

// A list of things to do needs two answers, and both have to submit the form:
// space acts on the line under the cursor, Enter ends the list. Every other key
// leaves the answer as it was, so moving the cursor decides nothing.
func TestAnsweredWithSpace(t *testing.T) {
	space, ok := answeredWithSpace(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if !ok || !space {
		t.Errorf("space read as (%v, %v), want an act", space, ok)
	}
	end, ok := answeredWithSpace(tea.KeyMsg{Type: tea.KeyEnter})
	if !ok || end {
		t.Errorf("enter read as (%v, %v), want an end", end, ok)
	}
	for _, other := range []tea.Msg{
		tea.KeyMsg{Type: tea.KeyDown},
		tea.KeyMsg{Type: tea.KeyLeft},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")},
		tea.WindowSizeMsg{},
	} {
		if _, ok := answeredWithSpace(other); ok {
			t.Errorf("%v was read as an answer", other)
		}
	}
}

// Space has to be bound to submit, or the watcher would read a key the form
// never acts on - and the filter has to be off, or a space typed into it would
// read as an answer.
func TestListKeysBindSpaceAndDropTheFilter(t *testing.T) {
	km := listKeys()
	if !slices.Contains(km.Select.Submit.Keys(), " ") {
		t.Errorf("space does not submit: %v", km.Select.Submit.Keys())
	}
	if slices.Contains(km.Select.Filter.Keys(), "/") {
		t.Errorf("the filter is still on: %v", km.Select.Filter.Keys())
	}
}

// An answered prompt is cleared rather than left on the screen: bubbletea paints
// the view once more on the way out, so the last frame has to be empty.
func TestQuietFormClearsWhenItIsDone(t *testing.T) {
	var idx int
	form := huh.NewForm(huh.NewGroup(huh.NewSelect[int]().
		Options(huh.NewOption("only", 0)).Value(&idx))).WithKeyMap(menuKeys())
	q := &quietForm{form: form}
	q.Init()

	if q.View() == "" {
		t.Error("the prompt is blank while it is still being answered")
	}
	q.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if form.State == huh.StateNormal {
		t.Fatalf("the form is still in state %v", form.State)
	}
	if got := q.View(); got != "" {
		t.Errorf("the answered prompt still paints:\n%s", got)
	}
}

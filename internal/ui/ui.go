// Package ui writes to the terminal and asks the user questions.
package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
)

// ErrAborted is returned when the user leaves a prompt without answering it.
// Callers decide whether that ends the command or only the menu they were in.
var ErrAborted = huh.ErrUserAborted

// Spin marks the wait while fn runs and returns fn's error. Without a terminal
// it only runs fn: the frames would land in whatever the output is piped into.
func Spin(title string, fn func() error) error {
	if !Interactive() {
		return fn()
	}
	done, cleared := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(cleared)
		frames := []rune("|/-\\")
		for i := 0; ; i++ {
			select {
			case <-done:
				fmt.Fprint(os.Stderr, "\r\033[K")
				return
			case <-time.After(120 * time.Millisecond):
				fmt.Fprintf(os.Stderr, "\r%c %s", frames[i%len(frames)], title)
			}
		}
	}()
	err := fn()
	close(done)
	<-cleared
	return err
}

// Warnf reports a non-fatal problem on stderr.
func Warnf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format, args...)
}

// Confirm asks a yes/no question that defaults to no. Aborting returns ErrAborted.
func Confirm(title string) (bool, error) {
	return ConfirmNote(title, "")
}

// ConfirmNote is Confirm with a note rendered above the buttons.
func ConfirmNote(title, note string) (bool, error) {
	var ok bool
	err := runForm(huh.NewConfirm().Title(title).Description(note).
		Affirmative("Yes").Negative("No").Value(&ok))
	return ok, err
}

// An Option is one entry of a multi-select: what the user reads and what the
// caller gets back for it.
type Option struct {
	Label string
	Value string
}

// PickMany returns the values of the options the user ticked. Nothing is
// preselected.
func PickMany(title, body string, options []Option) ([]string, error) {
	opts := make([]huh.Option[string], 0, len(options))
	for _, o := range options {
		opts = append(opts, huh.NewOption(o.Label, o.Value))
	}
	var picked []string
	err := runFormWith(menuKeys(), huh.NewMultiSelect[string]().
		Title(title).Description(body).Options(opts...).Value(&picked))
	return picked, err
}

// PickAtOrEnd is a list of things to do rather than a list to choose from. Space
// answers the line under the cursor and the caller is told which one, so it can
// act and open the list again with the cursor where it was; Enter is the other
// answer - "I am done with this list". Both keys submit the form, so nothing is
// rewritten on the way in: the watcher only reads which one arrived.
func PickAtOrEnd(title, body string, labels []string, at int) (int, bool, error) {
	if !Interactive() {
		return 0, false, errNoTerminal
	}
	opts := make([]huh.Option[int], 0, len(labels))
	for i, l := range labels {
		opts = append(opts, huh.NewOption(l, i))
	}
	idx := at
	form := huh.NewForm(huh.NewGroup(huh.NewSelect[int]().
		Title(title).Description(body).Options(opts...).Value(&idx))).
		WithKeyMap(listKeys())
	w := &actWatcher{form: form}
	if _, err := tea.NewProgram(w).Run(); err != nil {
		return 0, false, err
	}
	if form.State == huh.StateAborted {
		return 0, false, ErrAborted
	}
	return idx, w.act, nil
}

// actWatcher reads which key finished the form. Both are bound to submit, so the
// form behaves the same either way and the index still reaches the caller.
type actWatcher struct {
	form *huh.Form
	act  bool
}

func (w *actWatcher) Init() tea.Cmd { return w.form.Init() }

func (w *actWatcher) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if act, ok := answeredWithSpace(msg); ok {
		w.act = act
	}
	form, cmd := w.form.Update(msg)
	w.form, _ = form.(*huh.Form)
	if w.form.State != huh.StateNormal {
		return w, tea.Quit
	}
	return w, cmd
}

// The last frame is empty for the same reason quietForm's is: an answered list
// is not worth the lines it occupies, and this one is answered on every key.
func (w *actWatcher) View() string {
	if w.form.State != huh.StateNormal {
		return ""
	}
	return w.form.View()
}

// answeredWithSpace reports what a key means for a list of things to do: space
// acts on the line, Enter ends the list. ok is false for every other key, so a
// keystroke that is neither leaves the answer as it was.
func answeredWithSpace(msg tea.Msg) (space, ok bool) {
	k, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return false, false
	}
	switch k.String() {
	case " ":
		return true, true
	case "enter":
		return false, true
	}
	return false, false
}

// Pick shows the labels as a menu and returns the index of the chosen one.
// Aborting the menu returns huh.ErrUserAborted.
func Pick(title string, labels []string) (int, error) {
	opts := make([]huh.Option[int], 0, len(labels))
	for i, l := range labels {
		opts = append(opts, huh.NewOption(l, i))
	}
	var idx int
	err := runFormWith(menuKeys(),
		huh.NewSelect[int]().Title(title).Options(opts...).Value(&idx))
	return idx, err
}

// Ask reads one line. validate may be nil; it runs on every keystroke and its
// error is shown under the field until the input satisfies it.
func Ask(title, placeholder string, validate func(string) error) (string, error) {
	var value string
	input := huh.NewInput().Title(title).Placeholder(placeholder).Value(&value)
	if validate != nil {
		input = input.Validate(validate)
	}
	err := runFormWith(askKeys(), input)
	return value, err
}

// NonEmpty builds a validator for Ask that names what is missing.
func NonEmpty(what string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("enter " + what)
		}
		return nil
	}
}

// PickDir browses directories from start down. Files cannot be chosen.
func PickDir(title, start string) (string, error) {
	var dir string
	err := runFormWith(pickerKeys(), huh.NewFilePicker().
		Title(title).
		Description("Enter takes it, backspace goes up. Esc finishes, or goes back\n"+
			"if you picked nothing.").
		CurrentDirectory(start).
		DirAllowed(true).FileAllowed(false).
		ShowSize(false).ShowPermissions(false).
		Picking(true).Height(10).Value(&dir))
	return dir, err
}

// pickerKeys relabels what the browser already does. huh puts Enter on both Open
// and Submit, so the legend reads "enter open • enter submit"; with directories
// selectable, Enter selects and only the arrow descends.
func pickerKeys() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.FilePicker.Open = key.NewBinding(key.WithKeys("l", "right", "enter"),
		key.WithHelp("→", "open"))
	km.FilePicker.Submit = key.NewBinding(key.WithKeys("enter"),
		key.WithHelp("enter", "select"))
	km.FilePicker.Close = key.NewBinding(key.WithKeys("esc"),
		key.WithHelp("esc", "stop"))
	return km
}

// AskDir reads a path instead of browsing for one; Tab takes the completion the
// field offers. An empty answer means no more directories.
func AskDir(title string) (string, error) {
	var path string
	in := huh.NewInput().Title(title).
		Description("Tab completes; empty and Enter finishes, or goes back if you typed nothing.").
		Value(&path).Validate(existingDir)
	err := runFormWith(dirKeys(),
		in.SuggestionsFunc(func() []string { return dirsCompleting(path) }, &path))
	return path, err
}

// dirKeys is askKeys with completion on Tab, which is what a shell binds. Tab is
// free here: a one-field form has no next field to move to.
func dirKeys() *huh.KeyMap {
	km := askKeys()
	km.Input.AcceptSuggestion = key.NewBinding(
		key.WithKeys("tab", "ctrl+e"), key.WithHelp("tab", "complete"))
	return km
}

// existingDir accepts an empty answer, which the caller reads as "no more".
func existingDir(path string) error {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

// dirsCompleting lists the directories that could continue what has been typed.
func dirsCompleting(typed string) []string {
	dir, frag := filepath.Split(typed)
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), frag) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// Note shows body until the user dismisses it.
func Note(title, body string) error {
	return runForm(huh.NewNote().Title(title).Description(body).Next(true).NextLabel("Back"))
}

// IgnoreUserAbort turns leaving a prompt into a clean return; a menu the user
// walks out of is not a failure.
func IgnoreUserAbort(err error) error {
	if errors.Is(err, ErrAborted) {
		return nil
	}
	return err
}

// Interactive reports whether cjx can put a form on this terminal. huh reads
// keystrokes from stdin and paints on stdout; redirect either and the form waits
// for keys nobody can see it asking for.
func Interactive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// askKeys gives a text prompt the way back the menus have on ←; there the arrow
// belongs to the cursor, so it falls to Esc. A one-field input has only its
// submit slot to render help in, so that entry names both.
func askKeys() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"))
	km.Input.Submit = key.NewBinding(key.WithKeys("enter"),
		key.WithHelp("enter", "submit • esc back"))
	return km
}

// menuKeys makes the left arrow leave a list prompt, the way its Quit entry
// does. Both keys ride on the filter slot: huh renders help only for bindings
// the field itself uses, and in a one-field form every other slot is switched
// off, so one entry has to name them both.
func menuKeys() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("left", "ctrl+c"))
	back := key.NewBinding(key.WithKeys("left", "/"),
		key.WithHelp("←", "back • / filter"))
	km.Select.Filter = back
	km.MultiSelect.Filter = back
	return km
}

// listKeys is menuKeys for a list of things to do: space answers a line as well
// as Enter, and the filter is off, because a space typed into a filter would
// otherwise read as an answer.
func listKeys() *huh.KeyMap {
	km := menuKeys()
	km.Select.Filter = key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "back"))
	km.Select.Submit = key.NewBinding(key.WithKeys("enter", " "),
		key.WithHelp("enter", "submit"))
	return km
}

func runForm(fields ...huh.Field) error {
	return runFormWith(huh.NewDefaultKeyMap(), fields...)
}

var errNoTerminal = errors.New(
	"this prompt needs an interactive shell: stdin and stdout must be a terminal")

func runFormWith(km *huh.KeyMap, fields ...huh.Field) error {
	if !Interactive() {
		return errNoTerminal
	}
	return runQuietly(huh.NewForm(huh.NewGroup(fields...)).WithKeyMap(km))
}

// runQuietly runs a form and leaves nothing behind. An answered prompt is not
// worth the lines it occupies, and a run that asks for an agent and then a mode
// would otherwise stack both on the screen.
func runQuietly(form *huh.Form) error {
	if _, err := tea.NewProgram(&quietForm{form: form}).Run(); err != nil {
		return err
	}
	if form.State == huh.StateAborted {
		return huh.ErrUserAborted
	}
	return nil
}

// quietForm is the form with an empty last frame: bubbletea paints the view once
// more on the way out, and painting nothing is what clears the prompt.
type quietForm struct {
	form *huh.Form
}

func (q *quietForm) Init() tea.Cmd { return q.form.Init() }

func (q *quietForm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	form, cmd := q.form.Update(msg)
	q.form, _ = form.(*huh.Form)
	if q.form.State != huh.StateNormal {
		return q, tea.Quit
	}
	return q, cmd
}

func (q *quietForm) View() string {
	if q.form.State != huh.StateNormal {
		return ""
	}
	return q.form.View()
}

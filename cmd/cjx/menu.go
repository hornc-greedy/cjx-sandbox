package main

import (
	"errors"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/ui"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// runCmdName is the one command the menu asks an agent for.
const runCmdName = "run"

// menuEnds marks a command after which the menu does not come back.
const menuEnds = "cjx.menu-ends"

// menuShows marks a command whose output belongs in the menu rather than on the
// screen behind it: it reports and returns, so there is nothing to watch while
// it runs and everything to read once it is done.
const menuShows = "cjx.menu-shows"

// A menu is the bare-cjx command chooser. Its engine survives a command and the
// return to the list, so a switch holds for the whole session.
type menu struct {
	// engine is empty when no runtime is installed at all; otherwise it is the
	// one the next command runs on, and the list shows it.
	engine string
}

// newMenu starts on CJX_ENGINE, else the engine saved from the last menu, else
// the one Detect would pick, each only if it is installed. With no runtime at
// all it stays empty: cjx then passes no --engine and Detect keeps its own
// message.
func newMenu() *menu {
	found := engine.Installed()
	if len(found) == 0 {
		return &menu{}
	}
	name := os.Getenv("CJX_ENGINE")
	if !slices.Contains(found, name) {
		name = config.SavedEngine()
	}
	if !slices.Contains(found, name) {
		name = found[0]
	}
	return &menu{engine: name}
}

// argv is the command line the menu stands in for. The engine rides along so a
// choice made once holds for every command in the session.
func (m *menu) argv(name, agent string, picked []string, values map[string][]string) []string {
	args := commandArgs(name, picked, values)
	if agent != "" {
		args = append(args, agent)
	}
	if m.engine == "" {
		return args
	}
	return append([]string{"--engine", m.engine}, args...)
}

// chooseEngine offers the runtimes that are actually installed; there is nothing
// useful cjx could do with one that is not.
func (m *menu) chooseEngine() error {
	found := engine.Installed()
	i, err := ui.Pick("Engine", found)
	if err != nil {
		return err
	}
	m.engine = found[i]
	config.SaveEngine(m.engine)
	return nil
}

func (m *menu) choose(root *cobra.Command) ([]string, *cobra.Command, error) {
	cmds := offered(root)
	for {
		labels := make([]string, 0, len(cmds)+2)
		for _, c := range cmds {
			labels = append(labels, pad(c.Name(), nameWidth(cmds))+c.Short)
		}
		if m.engine != "" {
			labels = append(labels, pad("engine", nameWidth(cmds))+m.engine)
		}
		labels = append(labels, "Quit")

		i, err := ui.Pick("cjx", labels)
		if err != nil {
			return nil, nil, err
		}
		if i == len(labels)-1 {
			return nil, nil, ui.ErrAborted
		}
		if i == len(cmds) {
			if err := m.chooseEngine(); err != nil && !errors.Is(err, ui.ErrAborted) {
				return nil, nil, err
			}
			continue
		}
		cmd := cmds[i]
		agent, err := agentFor(cmd)
		// Walking out of a question goes back to the command list, not out of cjx.
		if errors.Is(err, ui.ErrAborted) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		picked, values, err := askFlags(cmd, m.engine)
		if errors.Is(err, ui.ErrAborted) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		return m.argv(cmd.Name(), agent, picked, values), cmd, nil
	}
}

// agentFor asks which coding agent a run starts, so the menu offers the tools
// before the flags. Every other command takes no agent.
func agentFor(cmd *cobra.Command) (string, error) {
	if cmd.Name() != runCmdName {
		return "", nil
	}
	agents, err := config.LoadAgents()
	if err != nil {
		return "", err
	}
	if len(agents) == 1 {
		return agents[0].Name, nil
	}
	i, err := ui.Pick("Select an agent", agentLabels(agents))
	if err != nil {
		return "", err
	}
	return agents[i].Name, nil
}

// offered lists the commands worth putting in a menu, in the order they were
// registered: cobra's own help and completion entries are not among them.
func offered(root *cobra.Command) []*cobra.Command {
	var cmds []*cobra.Command
	for _, c := range root.Commands() {
		if c.IsAvailableCommand() && c.Name() != "help" {
			cmds = append(cmds, c)
		}
	}
	return cmds
}

// askFlags offers the command's own switches as a checklist and asks for any
// flag it cannot run without. Optional flags that take a value are left to the
// command line: a menu has no sensible list of answers for them.
func askFlags(cmd *cobra.Command, current string) (
	picked []string, values map[string][]string, err error,
) {
	var flags, required []*pflag.Flag
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		switch {
		case f.Name == "help":
		case takesDirectory(f), f.Value.Type() == "bool":
			flags = append(flags, f)
		case isRequired(f):
			required = append(required, f)
		}
	})

	values = map[string][]string{}
	for _, f := range required {
		v, err := askRequired(f, current)
		if err != nil {
			return nil, nil, err
		}
		values[f.Name] = []string{v}
	}
	if len(flags) == 0 {
		return nil, values, nil
	}
	return chooseFlags(cmd.Name(), flags, values)
}

// chooseFlags shows the flags as a list of things to do: space answers the line
// under the cursor - a switch goes on or off, a directory flag opens its paths -
// and Enter runs the command. The list comes back with the cursor where it was,
// so a second directory is one space away.
func chooseFlags(name string, flags []*pflag.Flag, values map[string][]string) (
	[]string, map[string][]string, error,
) {
	on := map[string]bool{}
	at := 0
	body := "space answers the line under the cursor · enter runs " + name
	for {
		i, act, err := ui.PickAtOrEnd(name+" options", body, optionLines(flags, on, values), at)
		if err != nil {
			return nil, nil, err
		}
		if !act {
			return switchedOn(flags, on), values, nil
		}
		at = i
		f := flags[i]
		if takesDirectory(f) {
			if err := askDir(f, values); err != nil {
				return nil, nil, err
			}
			continue
		}
		on[f.Name] = !on[f.Name]
	}
}

// optionLines is one line per flag. A switch is ticked when it is on, a directory
// flag when it holds paths - and then it shows the paths rather than its usage,
// because that is what changes while you work.
func optionLines(flags []*pflag.Flag, on map[string]bool, values map[string][]string) []string {
	width := 0
	for _, f := range flags {
		if len(f.Name)+4 > width {
			width = len(f.Name) + 4
		}
	}
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		_, tail := pflag.UnquoteUsage(f)
		mark := " "
		if takesDirectory(f) {
			if len(values[f.Name]) > 0 {
				mark, tail = "x", strings.Join(values[f.Name], ", ")
			}
		} else if on[f.Name] {
			mark = "x"
		}
		out = append(out, "["+mark+"] "+pad("--"+f.Name, width)+tail)
	}
	return out
}

// switchedOn is what the user turned on, in the order the flags are declared.
// Only switches get here: chooseFlags sends a directory flag to its own list
// instead, and it reaches the command line as its paths.
func switchedOn(flags []*pflag.Flag, on map[string]bool) []string {
	var out []string
	for _, f := range flags {
		if on[f.Name] {
			out = append(out, f.Name)
		}
	}
	return out
}

// askDir is the directory flag's own list: it browses, takes a typed path, or
// takes one back out again. Browsing and typing are two lines rather than a menu
// of their own, so adding a directory stays two keystrokes.
func askDir(f *pflag.Flag, values map[string][]string) error {
	_, usage := pflag.UnquoteUsage(f)
	i, err := ui.Pick("--"+f.Name+"  "+usage, dirActions(f, values))
	if err != nil {
		return ui.IgnoreUserAbort(err)
	}
	var dir string
	switch {
	case i == 0:
		dir, err = ui.PickDir(usage, "/")
	case i == 1:
		dir, err = ui.AskDir(usage)
	case i-2 < len(values[f.Name]):
		values[f.Name] = slices.Delete(values[f.Name], i-2, i-1)
		return nil
	default:
		return nil
	}
	if err != nil && !errors.Is(err, ui.ErrAborted) {
		return err
	}
	if dir != "" {
		values[f.Name] = append(values[f.Name], dir)
	}
	return nil
}

// dirActions is what that list says: the two ways in, every path already in -
// running one takes it out - and the way back with nothing changed.
func dirActions(f *pflag.Flag, values map[string][]string) []string {
	out := make([]string, 0, 3+len(values[f.Name]))
	out = append(out, "browse the tree", "type the path")
	for _, p := range values[f.Name] {
		out = append(out, p+"   (Enter removes)")
	}
	return append(out, "back")
}

// flagChoices names the annotation a command sets when its flag takes one of a
// fixed set of values.
const flagChoices = "cjx_choices"

// askRequired asks for a flag the command cannot run without: a flag with fixed
// values is a list, any other one a typed answer.
func askRequired(f *pflag.Flag, current string) (string, error) {
	_, usage := pflag.UnquoteUsage(f)
	choices := f.Annotations[flagChoices]
	if len(choices) == 0 {
		return ui.Ask(usage, f.DefValue, ui.NonEmpty("a value for --"+f.Name))
	}
	choices = otherFirst(choices, current)
	i, err := ui.Pick(usage, choices)
	if err != nil {
		return "", err
	}
	return choices[i], nil
}

// otherFirst puts the value this session is not on under the cursor: copying
// into the engine you are already running is the rarer half of the question.
func otherFirst(choices []string, current string) []string {
	out := make([]string, 0, len(choices))
	for _, c := range choices {
		if c != current {
			out = append(out, c)
		}
	}
	for _, c := range choices {
		if c == current {
			out = append(out, c)
		}
	}
	return out
}

// takesDirectory reports whether cobra was told the flag names a directory. The
// annotation is the one MarkFlagDirname sets, so the flag says what it wants and
// the menu needs no list of its own.
func takesDirectory(f *pflag.Flag) bool {
	_, ok := f.Annotations[cobra.BashCompSubdirsInDir]
	return ok
}

// huh renders a label as one string, so the columns are padded here rather than
// left to a tab the form would print verbatim.
func pad(s string, width int) string {
	for len(s) < width {
		s += " "
	}
	return s
}

func nameWidth(cmds []*cobra.Command) int {
	w := 0
	for _, c := range cmds {
		if len(c.Name())+2 > w {
			w = len(c.Name()) + 2
		}
	}
	return w
}

// isRequired reports whether cobra was told the command cannot run without the
// flag; cobra records that as an annotation rather than a field.
func isRequired(f *pflag.Flag) bool {
	return len(f.Annotations[cobra.BashCompOneRequiredFlag]) > 0 &&
		f.Annotations[cobra.BashCompOneRequiredFlag][0] == "true"
}

// commandArgs assembles what the user chose into the command line they did not
// have to type. Switches are passed as bare flags, values as --name value.
func commandArgs(name string, picked []string, values map[string][]string) []string {
	args := []string{name}
	for _, p := range picked {
		args = append(args, "--"+p)
	}
	names := make([]string, 0, len(values))
	for k := range values {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		for _, v := range values[k] {
			args = append(args, "--"+k, v)
		}
	}
	return args
}

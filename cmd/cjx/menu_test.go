package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine/enginetest"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The menu lists what cobra knows, so a command added later appears without the
// menu being told about it - and cobra's own help entry does not.
func TestOffered(t *testing.T) {
	got := offered(newRoot())
	names := make([]string, 0, len(got))
	for _, c := range got {
		names = append(names, c.Name())
	}
	// Registration order, not alphabetical. Asserting the ends rather than the
	// whole list keeps this true on a branch that adds a command in between.
	if len(names) == 0 || names[0] != "run" {
		t.Errorf("offered starts with %v, want run first", names)
	}
	if names[len(names)-1] != "install" {
		t.Errorf("offered ends with %v, want install last", names)
	}
	if slices.Contains(names, "help") {
		t.Errorf("help is not something to do: %v", names)
	}
}

// What the user ticked has to come out as the command line they did not type.
func TestCommandArgs(t *testing.T) {
	cases := map[string]struct {
		picked []string
		values map[string][]string
		want   []string
	}{
		"nothing chosen": {
			nil, nil,
			[]string{"run"},
		},
		"two switches": {
			[]string{"rw", "select"},
			nil,
			[]string{"run", "--rw", "--select"},
		},
		"a value": {
			nil,
			map[string][]string{"to": {"docker"}},
			[]string{"run", "--to", "docker"},
		},
		"a flag given twice": {
			nil,
			map[string][]string{"add-dir": {"/a", "/b"}},
			[]string{"run", "--add-dir", "/a", "--add-dir", "/b"},
		},
		"switch and value": {
			[]string{"rw"},
			map[string][]string{"to": {"docker"}},
			[]string{"run", "--rw", "--to", "docker"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := commandArgs("run", c.picked, c.values); !slices.Equal(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// A flag the command cannot run without has to be asked; an optional one must
// not be, because the menu has no list of answers for it.
func TestRequiredFlagsAreDetected(t *testing.T) {
	cmd := &cobra.Command{Use: "migrate", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().String("to", "", "engine to copy into")
	cmd.Flags().String("from", "", "engine to copy out of")
	if err := cmd.MarkFlagRequired("to"); err != nil {
		t.Fatal(err)
	}
	if !isRequired(cmd.Flags().Lookup("to")) {
		t.Error("--to is required and was not detected")
	}
	if isRequired(cmd.Flags().Lookup("from")) {
		t.Error("--from is optional and was treated as required")
	}
}

// The menu comes back after a command unless the command took over the terminal
// or replaced the binary underneath it.
func TestOnlyRunAndInstallEndTheMenu(t *testing.T) {
	var ends []string
	for _, c := range offered(newRoot()) {
		if _, ok := c.Annotations[menuEnds]; ok {
			ends = append(ends, c.Name())
		}
	}
	slices.Sort(ends)
	want := []string{"install", "run"}
	if !slices.Equal(ends, want) {
		t.Errorf("commands that end the menu = %v, want %v", ends, want)
	}
}

// A command that only reports gets its own screen; everything else writes
// straight to the terminal the menu is drawn on.
func TestOnlyStatusShowsItsOutputInTheMenu(t *testing.T) {
	var shows []string
	for _, c := range offered(newRoot()) {
		if _, ok := c.Annotations[menuShows]; ok {
			shows = append(shows, c.Name())
		}
	}
	slices.Sort(shows)
	want := []string{"status"}
	if !slices.Equal(shows, want) {
		t.Errorf("commands shown in the menu = %v, want %v", shows, want)
	}
}

// The menu asks for a directory only because cobra was told the flag takes one;
// it keeps no list of flag names of its own.
func TestTakesDirectoryFollowsTheAnnotation(t *testing.T) {
	cmd := &cobra.Command{Use: "run", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().StringArray("add-dir", nil, "extra directory")
	cmd.Flags().StringArray("other", nil, "something else")
	if err := cmd.MarkFlagDirname("add-dir"); err != nil {
		t.Fatal(err)
	}
	if !takesDirectory(cmd.Flags().Lookup("add-dir")) {
		t.Error("the marked flag is not recognised as taking a directory")
	}
	if takesDirectory(cmd.Flags().Lookup("other")) {
		t.Error("an unmarked flag would open a directory browser")
	}
}

// The directory flag's list browses or types first, then every path already in -
// running one takes it out - and the way back with nothing changed.
func TestDirActions(t *testing.T) {
	cmd := &cobra.Command{Use: "run", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().StringArray("add-dir", nil, "extra directory")
	add := cmd.Flags().Lookup("add-dir")

	empty := dirActions(add, nil)
	if !slices.Equal(empty, []string{"browse the tree", "type the path", "back"}) {
		t.Errorf("empty list = %v", empty)
	}
	got := dirActions(add, map[string][]string{"add-dir": {"/a", "/b"}})
	want := []string{
		"browse the tree", "type the path",
		"/a   (Enter removes)", "/b   (Enter removes)",
		"back",
	}
	if !slices.Equal(got, want) {
		t.Errorf("list = %v, want %v", got, want)
	}
}

// Running a path line takes that path out; the indexes have to line up with what
// dirActions renders, or the wrong one goes.
func TestDirActionRemovesTheOneItNames(t *testing.T) {
	cmd := &cobra.Command{Use: "run", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().StringArray("add-dir", nil, "extra directory")
	add := cmd.Flags().Lookup("add-dir")
	values := map[string][]string{"add-dir": {"/a", "/b", "/c"}}

	i := slices.Index(dirActions(add, values), "/b   (Enter removes)")
	if i < 2 {
		t.Fatalf("/b is not among the paths: %v", dirActions(add, values))
	}
	values["add-dir"] = slices.Delete(values["add-dir"], i-2, i-1)
	if !slices.Equal(values["add-dir"], []string{"/a", "/c"}) {
		t.Errorf("after running /b the list holds %v", values["add-dir"])
	}
}

// Every line says what it holds: a switch whether it is on, a directory flag its
// paths rather than its usage - and a directory flag with paths is ticked, so the
// list shows at a glance that it is in play.
func TestOptionLines(t *testing.T) {
	cmd := &cobra.Command{Use: "run", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().StringArray("add-dir", nil, "extra `directory`")
	cmd.Flags().Bool("rw", false, "read-write")
	if err := cmd.MarkFlagDirname("add-dir"); err != nil {
		t.Fatal(err)
	}
	flags := []*pflag.Flag{cmd.Flags().Lookup("add-dir"), cmd.Flags().Lookup("rw")}

	got := optionLines(flags, nil, nil)
	if len(got) != 2 {
		t.Fatalf("got %d lines for 2 flags: %v", len(got), got)
	}
	for i, want := range []string{"[ ] --add-dir", "[ ] --rw"} {
		if !strings.HasPrefix(got[i], want) {
			t.Errorf("line %d is %q, want it to start with %q", i, got[i], want)
		}
	}
	if !strings.Contains(got[0], "extra directory") {
		t.Errorf("an empty directory flag reads %q, want its usage", got[0])
	}

	got = optionLines(flags, map[string]bool{"rw": true},
		map[string][]string{"add-dir": {"/a", "/b"}})
	for i, want := range []string{"[x] --add-dir", "[x] --rw"} {
		if !strings.HasPrefix(got[i], want) {
			t.Errorf("line %d is %q, want it to start with %q", i, got[i], want)
		}
	}
	if !strings.Contains(got[0], "/a, /b") {
		t.Errorf("the directory line does not name its paths: %s", got[0])
	}
	if strings.Contains(got[0], "extra directory") {
		t.Errorf("the paths did not replace the usage: %s", got[0])
	}
}

// What comes out is what was turned on, in the order the flags are declared -
// the command line reads the same however the user walked the list.
func TestSwitchedOnKeepsDeclarationOrder(t *testing.T) {
	cmd := &cobra.Command{Use: "run", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().Bool("rw", false, "read-write")
	cmd.Flags().Bool("shell", false, "shell")
	cmd.Flags().Bool("select", false, "select")
	flags := []*pflag.Flag{
		cmd.Flags().Lookup("rw"),
		cmd.Flags().Lookup("shell"),
		cmd.Flags().Lookup("select"),
	}

	got := switchedOn(flags, map[string]bool{"select": true, "rw": true})
	if !slices.Equal(got, []string{"rw", "select"}) {
		t.Errorf("switches = %v, want rw then select", got)
	}
	if got := switchedOn(flags, nil); got != nil {
		t.Errorf("nothing was turned on, yet %v came out", got)
	}
}

// The engine row lists what is there, and the choice rides along as --engine so
// every command in the session sees it.
func TestMenuEngineListsWhatIsInstalled(t *testing.T) {
	enginetest.Fake(t, "")
	m := newMenu()
	if m.engine != engine.Podman {
		t.Errorf("the menu starts on %q, want %q", m.engine, engine.Podman)
	}
	if got := engine.Installed(); !slices.Equal(got, []string{engine.Podman, engine.Docker}) {
		t.Errorf("Installed = %v, want both, most preferred first", got)
	}

	m.engine = engine.Docker
	if args := m.argv("status", "", nil, nil); !slices.Equal(args,
		[]string{"--engine", "docker", "status"}) {
		t.Errorf("the choice did not reach the command: %v", args)
	}
	if args := (&menu{}).argv("status", "", nil, nil); !slices.Equal(args, []string{"status"}) {
		t.Errorf("without a runtime cjx still passes an engine: %v", args)
	}

	// The agent the menu asked for rides along as run's positional argument.
	if args := (&menu{}).argv("run", "codex", []string{"rw"}, nil); !slices.Equal(args,
		[]string{"run", "--rw", "codex"}) {
		t.Errorf("the chosen agent did not reach run: %v", args)
	}

	// Nothing installed: no row, and no --engine, so Detect keeps its own message.
	t.Setenv("PATH", t.TempDir())
	if empty := newMenu(); empty.engine != "" {
		t.Errorf("with no runtime the menu names %q", empty.engine)
	}
}

// The menu asks for --to with a list, so the engine names are annotations the
// command carries rather than something the menu knows about migrate.
func TestMigrateOffersBothEnginesAsChoices(t *testing.T) {
	got := migrateCmd().Flags().Lookup("to").Annotations[flagChoices]
	want := []string{engine.Podman, engine.Docker}
	if !slices.Equal(got, want) {
		t.Errorf("--to choices = %v, want %v", got, want)
	}
}

// Migrating into the engine you are already on is the rarer half, so the other
// one is the line under the cursor.
func TestOtherFirstLeadsWithTheEngineNotInUse(t *testing.T) {
	got := otherFirst([]string{engine.Podman, engine.Docker}, engine.Podman)
	want := []string{engine.Docker, engine.Podman}
	if !slices.Equal(got, want) {
		t.Errorf("otherFirst = %v, want %v", got, want)
	}
}

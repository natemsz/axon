// Package install puts Axon on a computer from the one file a player
// downloads: run from anywhere but its installed place, that file
// installs itself. It copies itself there, carries over a config left
// beside it or by an older install, finds the EVE chat logs, puts
// `axon` on the PATH, makes the shortcuts (Start menu and Desktop on
// Windows, an app on macOS, a menu entry on Linux), makes the site's
// evesynapse-axon:// links open it (Windows and Linux), and starts the
// installed copy, which opens Axon's window. Running a newer download
// the same way updates it in place. Everything is per user: no
// administrator rights are asked for.
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/natemsz/axon/internal/sys"
	"github.com/natemsz/axon/watch"
)

// AppName is the name Axon is installed under.
const AppName = "EveSynapse Axon"

// URLScheme is the kind of link that opens Axon: the EveSynapse site
// links to evesynapse-axon://open. Installing registers it (on Windows
// and Linux) to run the installed program with the link.
const URLScheme = "evesynapse-axon"

// Layout is where an installed Axon lives on this computer.
type Layout struct {
	Home   string // the player's home folder
	Dir    string // the folder holding the program
	Exe    string // the installed program
	Config string // its axon.json
	App    string // macOS: the .app bundle the program is inside
	Bin    string // macOS and Linux: the PATH folder `axon` is in
	// Legacy are configs older installs (the install scripts) left,
	// to carry over when there is none at Config yet.
	Legacy []string
}

// Default is the Layout for the player running axon.
func Default() (Layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, err
	}
	return layoutFor(runtime.GOOS, home, os.Getenv), nil
}

func layoutFor(goos, home string, getenv func(string) string) Layout {
	l := Layout{Home: home}
	switch goos {
	case "windows":
		base := getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		l.Dir = filepath.Join(base, "Programs", AppName)
		l.Exe = filepath.Join(l.Dir, "axon.exe")
		l.Config = filepath.Join(l.Dir, "axon.json")
	case "darwin":
		l.App = filepath.Join(home, "Applications", AppName+".app")
		l.Dir = filepath.Join(l.App, "Contents", "MacOS")
		l.Exe = filepath.Join(l.Dir, "axon")
		l.Config = filepath.Join(home, "Library", "Application Support", AppName, "axon.json")
		l.Bin = filepath.Join(home, ".local", "bin")
		l.Legacy = []string{filepath.Join(l.Bin, "axon.json")}
	default:
		l.Bin = getenv("XDG_BIN_HOME")
		if l.Bin == "" {
			l.Bin = filepath.Join(home, ".local", "bin")
		}
		l.Dir = l.Bin
		l.Exe = filepath.Join(l.Bin, "axon")
		l.Config = filepath.Join(l.Bin, "axon.json")
	}
	return l
}

// Holds reports whether exe is the installed program.
func (l Layout) Holds(exe string) bool {
	return samePath(exe, l.Exe)
}

func samePath(a, b string) bool {
	clean := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		return filepath.Clean(p)
	}
	a, b = clean(a), clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Report is what installing did, for the player.
type Report struct {
	Exe    string
	Config string // the config carried over, "" for none
	Logs   string // the chat log folder found, "" for none
}

// Install installs src (this program) per l, stopping first an Axon
// that is running (stop, which reports whether none is left). logf
// says each step.
func Install(l Layout, src, version string, stop func() bool, logf func(string, ...any)) (Report, error) {
	r := Report{Exe: l.Exe}
	if !stop() {
		return r, errors.New("the Axon already running would not stop: quit it from its tray icon or window, then run this again")
	}
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return r, err
	}
	if err := copyProgram(src, l.Exe); err != nil {
		return r, fmt.Errorf("installing %s: %w", l.Exe, err)
	}
	logf("installed %s", l.Exe)

	from, err := carryConfig(l, filepath.Dir(src))
	if err != nil {
		logf("could not carry over the config: %v", err)
	} else if from != "" {
		r.Config = from
		logf("carried over the config from %s", from)
	}

	candidates := watch.DefaultLogDirs()
	if r.Logs = watch.FindLogDir(candidates); r.Logs != "" {
		logf("found the EVE chat logs: %s", r.Logs)
		if set, err := recordLogs(l.Config, r.Logs); err != nil {
			logf("could not record them in %s: %v", l.Config, err)
		} else if set {
			logf("recorded them as \"logs\" in %s", l.Config)
		}
	} else {
		logf("no EVE chat logs yet (looked in %s); Axon finds them once the game has written some", strings.Join(candidates, ", "))
	}

	if err := platform(l, version, logf); err != nil {
		return r, err
	}
	return r, nil
}

// copyProgram puts src at dst. A program that is running cannot be
// overwritten on Windows but can be moved aside, so the old copy goes
// to dst.old first.
func copyProgram(src, dst string) error {
	if samePath(src, dst) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		os.Remove(dst + ".old")
		if err := os.Rename(dst, dst+".old"); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	os.Remove(dst + ".old") // gone unless still running
	return nil
}

// carryConfig copies a config (and its key) to l.Config when there is
// none there yet: the one beside the downloaded program first, else
// one an older install left. It returns where it came from.
func carryConfig(l Layout, srcDir string) (string, error) {
	if _, err := os.Stat(l.Config); err == nil {
		return "", nil
	}
	for _, c := range append([]string{filepath.Join(srcDir, "axon.json")}, l.Legacy...) {
		if samePath(c, l.Config) {
			continue
		}
		if _, err := os.Stat(c); err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(l.Config), 0o700); err != nil {
			return "", err
		}
		if err := copyFile(c, l.Config, 0o600); err != nil {
			return "", err
		}
		if k := watch.KeyPath(c); fileExists(k) {
			if err := copyFile(k, watch.KeyPath(l.Config), 0o600); err != nil {
				return "", err
			}
		}
		return c, nil
	}
	return "", nil
}

// recordLogs sets "logs" in the config at path to dir when the config
// exists and names no folder yet, keeping everything else in it as it
// was. It reports whether it changed the file.
func recordLogs(path, dir string) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		return false, err
	}
	if s, _ := cfg["logs"].(string); s != "" {
		return false, nil
	}
	cfg["logs"] = dir
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, append(out, '\n'), 0o600)
}

// Launch starts the installed Axon, apart from this program, with
// args.
func Launch(l Layout, args ...string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", append([]string{"-n", "-a", l.App, "--args"}, args...)...)
	} else {
		cmd = exec.Command(l.Exe, args...)
		cmd.Dir = l.Dir
		sys.Detach(cmd)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Uninstall removes what Install made, stopping a running Axon first.
// The config, its key and the log stay: they are the player's.
func Uninstall(l Layout, stop func() bool, logf func(string, ...any)) error {
	if !stop() {
		return errors.New("the Axon running would not stop: quit it from its tray icon or window, then try again")
	}
	return unplatform(l, logf)
}

func copyFile(src, dst string, perm os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, perm)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// addToPath adds l.Bin to PATH in the shell's startup file (macOS and
// Linux), as the install script did, unless PATH or the file already
// has it.
func addToPath(l Layout, logf func(string, ...any)) {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if samePath(p, l.Bin) {
			return
		}
	}
	rc := filepath.Join(l.Home, ".bashrc")
	if runtime.GOOS == "darwin" || filepath.Base(os.Getenv("SHELL")) == "zsh" {
		rc = filepath.Join(l.Home, ".zshrc")
	}
	if b, err := os.ReadFile(rc); err == nil && strings.Contains(string(b), l.Bin) {
		return
	}
	f, err := os.OpenFile(rc, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logf("could not add %s to PATH in %s: %v", l.Bin, rc, err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n# %s\nexport PATH=\"%s:$PATH\"\n", AppName, l.Bin)
	logf("added %s to PATH in %s (new terminals will see `axon`)", l.Bin, rc)
}

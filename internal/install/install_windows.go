package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/natemsz/axon/internal/icon"
	"github.com/natemsz/axon/internal/sys"
)

// Summary says, after installing, where Axon now is.
const Summary = "EveSynapse Axon is installed, with shortcuts on your Desktop and in the Start menu. " +
	"Closing this window leaves Axon running in the tray, by the clock; quit it from there."

// uninstallID names Axon's entry in Apps & features.
const uninstallID = "EveSynapseAxon"

func icoPath(l Layout) string { return filepath.Join(l.Dir, "axon.ico") }

// shortcutPaths are the Desktop and Start menu shortcuts.
func shortcutPaths() (desktop, menuDir string) {
	if d, err := sys.DesktopDir(); err == nil {
		desktop = filepath.Join(d, AppName+".lnk")
	}
	if p, err := sys.ProgramsDir(); err == nil {
		menuDir = filepath.Join(p, AppName)
	}
	return desktop, menuDir
}

func platform(l Layout, version string, logf func(string, ...any)) error {
	ico := icoPath(l)
	if err := os.WriteFile(ico, icon.ICO(), 0o644); err != nil {
		return err
	}
	if changed, err := sys.UserPath(l.Dir, true); err != nil {
		logf("could not add %s to your PATH: %v", l.Dir, err)
	} else if changed {
		logf("added %s to your PATH (new terminals will see axon)", l.Dir)
	}
	desktop, menuDir := shortcutPaths()
	const about = "Post your EVE intel channels to EveSynapse"
	if menuDir != "" {
		if err := os.MkdirAll(menuDir, 0o755); err != nil {
			return err
		}
		if err := sys.Shortcut(filepath.Join(menuDir, AppName+".lnk"), l.Exe, l.Dir, ico, about); err != nil {
			return err
		}
	}
	if desktop != "" {
		if err := sys.Shortcut(desktop, l.Exe, l.Dir, ico, about); err != nil {
			return err
		}
	}
	logf("made the shortcuts on the Desktop and in the Start menu")
	err := sys.UninstallEntry(uninstallID, map[string]string{
		"DisplayName":     AppName,
		"DisplayIcon":     ico,
		"DisplayVersion":  strings.TrimPrefix(version, "v"),
		"Publisher":       "Synap6.io LLC",
		"InstallLocation": l.Dir,
		"URLInfoAbout":    "https://github.com/natemsz/axon",
		"UninstallString": `"` + l.Exe + `" -uninstall`,
	}, map[string]uint32{"NoModify": 1, "NoRepair": 1})
	if err != nil {
		logf("could not list Axon in Apps & features: %v", err)
	} else {
		logf("listed it in Apps & features, to uninstall from there")
	}
	return nil
}

func unplatform(l Layout, logf func(string, ...any)) error {
	if _, err := sys.UserPath(l.Dir, false); err != nil {
		logf("could not take %s off your PATH: %v", l.Dir, err)
	}
	desktop, menuDir := shortcutPaths()
	if desktop != "" {
		os.Remove(desktop)
	}
	if menuDir != "" {
		os.RemoveAll(menuDir)
	}
	sys.RemoveUninstallEntry(uninstallID)
	os.Remove(icoPath(l))
	os.Remove(l.Exe + ".old")
	logf("removed the shortcuts, the PATH entry and the Apps & features entry")
	// The program cannot delete itself while it runs: a hidden cmd
	// does it a moment after this exits, then the folder if empty.
	// cmd reads its own quoting, which Go's would garble: the line goes
	// as it is.
	cmd := exec.Command("cmd")
	sys.Hide(cmd)
	cmd.SysProcAttr.CmdLine = `cmd /C "ping -n 3 127.0.0.1 >NUL & del /F /Q "` + l.Exe + `" & rmdir "` + l.Dir + `""`
	if err := cmd.Start(); err != nil {
		return err
	}
	logf("removed %s (your axon.json, axon.key and log stay in %s)", l.Exe, l.Dir)
	return nil
}

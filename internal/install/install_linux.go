package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/natemsz/axon/internal/icon"
)

// Summary says, after installing, where Axon now is.
const Summary = "EveSynapse Axon is installed, in your applications menu and on your Desktop. " +
	"Closing this window leaves Axon running; open it again to bring it back, or quit it here."

// desktopEntry's StartupWMClass matches the window's --class, so the
// window groups under Axon's own entry and icon. MimeType and %u make
// it the handler for evesynapse-axon:// links, which it is given as
// its one argument (none, started from the menu).
const desktopEntry = `[Desktop Entry]
Type=Application
Name=%s
Comment=Post your EVE intel channels to EveSynapse
Exec="%s" %%u
Icon=evesynapse-axon
Terminal=false
Categories=Game;Utility;
StartupWMClass=evesynapse-axon
MimeType=x-scheme-handler/evesynapse-axon;
`

func entryText(l Layout) string { return fmt.Sprintf(desktopEntry, AppName, l.Exe) }

// registerScheme makes the menu entry the handler for the site's
// links, through xdg-mime when it is there.
func registerScheme(entry string, logf func(string, ...any)) {
	xdg, err := exec.LookPath("xdg-mime")
	if err != nil {
		logf("no xdg-mime here, so %s:// links may not open Axon", URLScheme)
		return
	}
	if out, err := exec.Command(xdg, "default", entry, "x-scheme-handler/"+URLScheme).CombinedOutput(); err != nil {
		logf("could not make %s:// links open Axon: %v %s", URLScheme, err, strings.TrimSpace(string(out)))
		return
	}
	logf("made %s:// links (from your EveSynapse site) open Axon", URLScheme)
}

func dataHome(l Layout) string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	return filepath.Join(l.Home, ".local", "share")
}

func entryPaths(l Layout) (menu, desktop, iconFile string) {
	data := dataHome(l)
	return filepath.Join(data, "applications", "evesynapse-axon.desktop"),
		filepath.Join(l.Home, "Desktop", "evesynapse-axon.desktop"),
		filepath.Join(data, "icons", "hicolor", "256x256", "apps", "evesynapse-axon.png")
}

func platform(l Layout, _ string, logf func(string, ...any)) error {
	menu, desktop, iconFile := entryPaths(l)
	if err := os.MkdirAll(filepath.Dir(iconFile), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(iconFile, icon.PNG(256), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(menu), 0o755); err != nil {
		return err
	}
	entry := []byte(entryText(l))
	if err := os.WriteFile(menu, entry, 0o755); err != nil {
		return err
	}
	logf("added Axon to the applications menu")
	registerScheme(filepath.Base(menu), logf)
	if st, err := os.Stat(filepath.Dir(desktop)); err == nil && st.IsDir() {
		if err := os.WriteFile(desktop, entry, 0o755); err == nil {
			logf("put a launcher on the Desktop")
		}
	}
	addToPath(l, logf)
	return nil
}

func unplatform(l Layout, logf func(string, ...any)) error {
	menu, desktop, iconFile := entryPaths(l)
	for _, p := range []string{menu, desktop, iconFile, l.Exe, l.Exe + ".old"} {
		os.Remove(p)
	}
	logf("removed %s and its menu entry (your axon.json and axon.key stay in %s)", l.Exe, l.Dir)
	return nil
}

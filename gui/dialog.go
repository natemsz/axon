package gui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/natemsz/axon/internal/sys"
)

// The system's own file pickers, since a page cannot learn where a
// file lives: Windows' own dialogs, and elsewhere the programs every
// desktop already has (AppleScript on a Mac, zenity or kdialog on
// Linux).
type pickKind int

const (
	pickOpen pickKind = iota
	pickSave
	pickFolder
)

// errNoPicker: no picker program here; the path is typed instead.
var errNoPicker = errors.New("no file picker on this system: type the path instead")

// pick shows a picker and returns the chosen path, "" when the player
// cancels.
func pick(ctx context.Context, kind pickKind, from string) (string, error) {
	if from != "" {
		if st, err := os.Stat(from); err != nil || !st.IsDir() {
			from = filepath.Dir(from)
		}
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		switch kind {
		case pickOpen:
			return sys.PickFile("Choose an axon config", from, "", false)
		case pickSave:
			return sys.PickFile("Save the axon config as", from, "axon.json", true)
		default:
			return sys.PickFolder("Choose the EVE Chatlogs folder", from)
		}
	case "darwin":
		script := [...]string{
			`POSIX path of (choose file with prompt "Choose an axon config" of type {"public.json"})`,
			`POSIX path of (choose file name with prompt "Save the axon config as" default name "axon.json")`,
			`POSIX path of (choose folder with prompt "Choose the EVE Chatlogs folder")`,
		}[kind]
		cmd = exec.CommandContext(ctx, "osascript", "-e", script)
	default:
		if p, err := exec.LookPath("zenity"); err == nil {
			args := []string{"--file-selection"}
			switch kind {
			case pickOpen:
				args = append(args, "--title=Choose an axon config", "--file-filter=*.json")
			case pickSave:
				args = append(args, "--save", "--confirm-overwrite", "--title=Save the axon config as")
				from = filepath.Join(from, "axon.json")
			case pickFolder:
				args = append(args, "--directory", "--title=Choose the EVE Chatlogs folder")
			}
			if from != "" {
				args = append(args, "--filename="+from+string(filepath.Separator))
			}
			cmd = exec.CommandContext(ctx, p, args...)
		} else if p, err := exec.LookPath("kdialog"); err == nil {
			if from == "" {
				from = "."
			}
			args := [...][]string{
				{"--getopenfilename", from, "*.json"},
				{"--getsavefilename", filepath.Join(from, "axon.json"), "*.json"},
				{"--getexistingdirectory", from},
			}[kind]
			cmd = exec.CommandContext(ctx, p, args...)
		} else {
			return "", errNoPicker
		}
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	path := strings.TrimRight(out.String(), "\r\n")
	var exit *exec.ExitError
	if errors.As(err, &exit) && path == "" {
		return "", nil // cancelled
	}
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" && kind == pickFolder {
		path = strings.TrimSuffix(path, "/")
	}
	return path, nil
}

// OpenBrowser opens url in the system's browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

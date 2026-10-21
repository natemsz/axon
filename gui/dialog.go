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
)

// The system's own file pickers, run as the programs every desktop
// already has (PowerShell on Windows, AppleScript on a Mac, zenity or
// kdialog on Linux), since a page cannot learn where a file lives.
type pickKind int

const (
	pickOpen pickKind = iota
	pickSave
	pickFolder
)

// errNoPicker: no picker program here; the path is typed instead.
var errNoPicker = errors.New("no file picker on this system: type the path instead")

// The start folder and title reach the PowerShell script as
// environment variables, never spliced into its text.
const psPicker = `
[Console]::OutputEncoding = [Text.Encoding]::UTF8
Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form -Property @{TopMost = $true}
switch ($env:AXON_PICK) {
  'folder' {
    $d = New-Object System.Windows.Forms.FolderBrowserDialog
    $d.Description = 'The EVE Chatlogs folder'
    if ($env:AXON_FROM) { $d.SelectedPath = $env:AXON_FROM }
    if ($d.ShowDialog($owner) -eq 'OK') { [Console]::Out.Write($d.SelectedPath) }
  }
  default {
    if ($env:AXON_PICK -eq 'save') { $d = New-Object System.Windows.Forms.SaveFileDialog; $d.FileName = 'axon.json' }
    else { $d = New-Object System.Windows.Forms.OpenFileDialog }
    $d.Filter = 'Axon config (*.json)|*.json|All files (*.*)|*.*'
    if ($env:AXON_FROM) { $d.InitialDirectory = $env:AXON_FROM }
    if ($d.ShowDialog($owner) -eq 'OK') { [Console]::Out.Write($d.FileName) }
  }
}
`

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
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", psPicker)
		cmd.Env = append(os.Environ(), "AXON_PICK="+[...]string{"open", "save", "folder"}[kind], "AXON_FROM="+from)
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

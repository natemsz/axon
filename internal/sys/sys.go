// Package sys holds the few things axon asks of the operating system
// that differ between Windows, macOS and Linux: running helper
// programs without a window flashing up, starting a program that
// outlives this one, telling the player something when there is no
// console to print to, and finding a console when there is one.
package sys

import (
	"os"
	"os/exec"
	"runtime"
)

// Terminal reports whether standard input is a terminal: axon was
// started from a shell, not from a shortcut or by double-click.
func Terminal() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// Alert shows a message box with an OK button, best effort: on macOS
// through AppleScript, on Linux through zenity or kdialog when there.
func Alert(title, text string) {
	switch runtime.GOOS {
	case "windows":
		alert(title, text)
	case "darwin":
		cmd := exec.Command("osascript", "-e", "on run argv", "-e",
			"display dialog (item 2 of argv) with title (item 1 of argv) buttons {\"OK\"} default button 1 with icon caution",
			"-e", "end run", title, text)
		_ = cmd.Run()
	default:
		if p, err := exec.LookPath("zenity"); err == nil {
			_ = exec.Command(p, "--error", "--title="+title, "--text="+text, "--no-markup").Run()
		} else if p, err := exec.LookPath("kdialog"); err == nil {
			_ = exec.Command(p, "--title", title, "--error", text).Run()
		}
	}
}

//go:build !windows

package sys

import (
	"errors"
	"os/exec"
	"syscall"
)

// Hide does nothing here: helpers open no window of their own.
func Hide(*exec.Cmd) {}

// Detach starts cmd in a session of its own, so that it keeps running
// after axon exits and its terminal closes.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// AllowForeground does nothing here.
func AllowForeground() {}

// AttachConsole reports whether axon has a terminal; there is nothing
// to attach.
func AttachConsole() bool { return Terminal() }

func alert(string, string) {}

// PickFile and PickFolder are Windows' own dialogs; elsewhere the
// gui package runs the system's picker programs.
func PickFile(string, string, string, bool) (string, error) { return "", errNotHere }
func PickFolder(string, string) (string, error)             { return "", errNotHere }

var errNotHere = errors.New("not on this system")

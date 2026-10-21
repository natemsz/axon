package sys

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pMessageBoxW              = user32.NewProc("MessageBoxW")
	pAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")
	pAttachConsole            = kernel32.NewProc("AttachConsole")
)

const (
	createNoWindow  = 0x08000000
	detachedProcess = 0x00000008
	newProcessGroup = 0x00000200
	attachParent    = ^uintptr(0) // ATTACH_PARENT_PROCESS
	asfwAny         = ^uintptr(0) // ASFW_ANY
	mbOK            = 0x00000000
	mbIconWarning   = 0x00000030
	mbSetForeground = 0x00010000
)

// Hide runs a console helper (PowerShell, cmd) without the console
// window Windows would otherwise open for it, since axon has none.
func Hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// Detach starts cmd so that it keeps running after axon exits.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | newProcessGroup}
}

// AllowForeground lets another process (the Axon already running)
// bring its window to the front, which Windows only lets the program
// the player just started do.
func AllowForeground() {
	pAllowSetForegroundWindow.Call(asfwAny)
}

// AttachConsole connects a windowed axon started from a terminal to
// that terminal, so -watch and errors print there. It reports whether
// there was one.
func AttachConsole() bool {
	if r, _, _ := pAttachConsole.Call(attachParent); r == 0 {
		return false
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
	if f, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = f
	}
	return true
}

func alert(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	pMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbOK|mbIconWarning|mbSetForeground)
}

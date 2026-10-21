package sys

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// The installer's and the file pickers' Windows work, done through the
// system's own calls rather than a script: shortcuts (the shell link
// COM object), the user's PATH and the Apps & features entry (the
// registry), and the standard open, save and folder dialogs.

var (
	ole32    = syscall.NewLazyDLL("ole32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")

	pCoInitializeEx       = ole32.NewProc("CoInitializeEx")
	pCoUninitialize       = ole32.NewProc("CoUninitialize")
	pCoCreateInstance     = ole32.NewProc("CoCreateInstance")
	pCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
	pSHGetKnownFolderPath = shell32.NewProc("SHGetKnownFolderPath")
	pSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	pSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	pRegCreateKeyExW      = advapi32.NewProc("RegCreateKeyExW")
	pRegOpenKeyExW        = advapi32.NewProc("RegOpenKeyExW")
	pRegQueryValueExW     = advapi32.NewProc("RegQueryValueExW")
	pRegSetValueExW       = advapi32.NewProc("RegSetValueExW")
	pRegDeleteTreeW       = advapi32.NewProc("RegDeleteTreeW")
	pRegCloseKey          = advapi32.NewProc("RegCloseKey")
	pGetOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
	pGetSaveFileNameW     = comdlg32.NewProc("GetSaveFileNameW")
	pCommDlgExtendedError = comdlg32.NewProc("CommDlgExtendedError")
	pSendMessageTimeoutW  = user32.NewProc("SendMessageTimeoutW")
	pSendMessageW         = user32.NewProc("SendMessageW")
	pGetForegroundWindow  = user32.NewProc("GetForegroundWindow")
	browseCallback        uintptr
	browseStart           *uint16
	errCancelled                  = errors.New("cancelled")
	hkeyCurrentUser       uintptr = 0x80000001
)

type guid struct {
	a    uint32
	b, c uint16
	d    [8]byte
}

var (
	clsidShellLink   = guid{0x00021401, 0, 0, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidShellLinkW    = guid{0x000214F9, 0, 0, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidPersistFile   = guid{0x0000010B, 0, 0, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	folderDesktop    = guid{0xB4BFCC3A, 0xDB2C, 0x424C, [8]byte{0xB0, 0x29, 0x7F, 0xE9, 0x9A, 0x87, 0xC6, 0x41}}
	folderPrograms   = guid{0xA77F5D77, 0x2E2B, 0x44C3, [8]byte{0xA6, 0xA2, 0xAB, 0xA6, 0x01, 0x05, 0x4A, 0x51}}
	errNoKnownFolder = errors.New("folder not found")
)

const (
	coinitApartment = 0x2
	clsctxInproc    = 0x1
	regSZ           = 1
	regExpandSZ     = 2
	regDWORD        = 4
	keyAllAccess    = 0xF003F
	keyRead         = 0x20019
	wmSettingChange = 0x001A
	hwndBroadcast   = 0xFFFF
	smtoAbortIfHung = 0x0002
)

// com runs f on one OS thread with COM set up, as the shell's objects
// and dialogs need.
func com(f func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r, _, _ := pCoInitializeEx.Call(0, coinitApartment)
	if int32(r) >= 0 { // S_OK, or S_FALSE when already set up
		defer pCoUninitialize.Call()
	}
	return f()
}

func hresult(what string, r uintptr) error {
	if int32(r) < 0 {
		return fmt.Errorf("%s: HRESULT 0x%08X", what, uint32(r))
	}
	return nil
}

func utf16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// vcall calls method i of the COM object obj.
func vcall(obj unsafe.Pointer, i int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(i)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func knownFolder(id *guid) (string, error) {
	var p *uint16
	r, _, _ := pSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(id)), 0, 0, uintptr(unsafe.Pointer(&p)))
	if int32(r) < 0 || p == nil {
		return "", errNoKnownFolder
	}
	defer pCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return syscall.UTF16ToString(unsafe.Slice(p, 32768)), nil
}

// DesktopDir and ProgramsDir are the player's Desktop and Start menu
// Programs folders, wherever they have been moved.
func DesktopDir() (string, error)  { return knownFolder(&folderDesktop) }
func ProgramsDir() (string, error) { return knownFolder(&folderPrograms) }

// Shortcut writes a shortcut (.lnk) at path to target.
func Shortcut(path, target, workDir, icon, description string) error {
	return com(func() error {
		var link unsafe.Pointer
		r, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInproc,
			uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&link)))
		if err := hresult("making a shortcut", r); err != nil {
			return err
		}
		defer vcall(link, 2)                                        // Release
		vcall(link, 20, uintptr(unsafe.Pointer(utf16(target))))     // SetPath
		vcall(link, 9, uintptr(unsafe.Pointer(utf16(workDir))))     // SetWorkingDirectory
		vcall(link, 7, uintptr(unsafe.Pointer(utf16(description)))) // SetDescription
		vcall(link, 17, uintptr(unsafe.Pointer(utf16(icon))), 0)    // SetIconLocation
		var file unsafe.Pointer
		r = vcall(link, 0, uintptr(unsafe.Pointer(&iidPersistFile)), uintptr(unsafe.Pointer(&file))) // QueryInterface
		if err := hresult("saving a shortcut", r); err != nil {
			return err
		}
		defer vcall(file, 2)
		return hresult("saving "+path, vcall(file, 6, uintptr(unsafe.Pointer(utf16(path))), 1)) // Save
	})
}

func regOpen(key string, create bool) (uintptr, error) {
	var h uintptr
	var r uintptr
	if create {
		r, _, _ = pRegCreateKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(utf16(key))), 0, 0, 0, keyAllAccess, 0, uintptr(unsafe.Pointer(&h)), 0)
	} else {
		r, _, _ = pRegOpenKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(utf16(key))), 0, keyAllAccess, uintptr(unsafe.Pointer(&h)))
	}
	if r != 0 {
		return 0, fmt.Errorf("registry key %s: %w", key, syscall.Errno(r))
	}
	return h, nil
}

func regGetString(h uintptr, name string) (string, uint32, error) {
	var kind, size uint32
	n := utf16(name)
	r, _, _ := pRegQueryValueExW.Call(h, uintptr(unsafe.Pointer(n)), 0, uintptr(unsafe.Pointer(&kind)), 0, uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return "", 0, syscall.Errno(r)
	}
	buf := make([]uint16, size/2+1)
	r, _, _ = pRegQueryValueExW.Call(h, uintptr(unsafe.Pointer(n)), 0, uintptr(unsafe.Pointer(&kind)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return "", 0, syscall.Errno(r)
	}
	return syscall.UTF16ToString(buf), kind, nil
}

func regSetString(h uintptr, name, value string, kind uint32) error {
	v, _ := syscall.UTF16FromString(value)
	r, _, _ := pRegSetValueExW.Call(h, uintptr(unsafe.Pointer(utf16(name))), 0, uintptr(kind), uintptr(unsafe.Pointer(&v[0])), uintptr(len(v)*2))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

func regSetDWORD(h uintptr, name string, value uint32) error {
	r, _, _ := pRegSetValueExW.Call(h, uintptr(unsafe.Pointer(utf16(name))), 0, regDWORD, uintptr(unsafe.Pointer(&value)), 4)
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// UserPath adds dir to the user's PATH (add) or takes it out, keeping
// the value's type, and tells running programs. It reports whether it
// changed anything.
func UserPath(dir string, add bool) (bool, error) {
	h, err := regOpen("Environment", true)
	if err != nil {
		return false, err
	}
	defer pRegCloseKey.Call(h)
	cur, kind, err := regGetString(h, "Path")
	if err != nil {
		cur, kind = "", regExpandSZ
	}
	next, changed := editPath(cur, dir, add)
	if !changed {
		return false, nil
	}
	if err := regSetString(h, "Path", next, kind); err != nil {
		return false, err
	}
	env := utf16("Environment")
	pSendMessageTimeoutW.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, 0)
	return true, nil
}

// editPath adds dir to (or takes it out of) a PATH value, leaving the
// other entries as they are, and reports whether that changed it.
func editPath(cur, dir string, add bool) (string, bool) {
	var kept []string
	has := false
	for _, p := range strings.Split(cur, ";") {
		switch {
		case p == "":
		case strings.EqualFold(filepath.Clean(p), filepath.Clean(dir)):
			has = true
			if add {
				kept = append(kept, p)
			}
		default:
			kept = append(kept, p)
		}
	}
	if has == add {
		return cur, false
	}
	if add {
		kept = append(kept, dir)
	}
	return strings.Join(kept, ";"), true
}

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\`

// UninstallEntry lists a program in Apps & features, under id.
func UninstallEntry(id string, strs map[string]string, dwords map[string]uint32) error {
	h, err := regOpen(uninstallKey+id, true)
	if err != nil {
		return err
	}
	defer pRegCloseKey.Call(h)
	for k, v := range strs {
		if err := regSetString(h, k, v, regSZ); err != nil {
			return err
		}
	}
	for k, v := range dwords {
		if err := regSetDWORD(h, k, v); err != nil {
			return err
		}
	}
	return nil
}

// RemoveUninstallEntry takes it out again.
func RemoveUninstallEntry(id string) {
	pRegDeleteTreeW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(utf16(uninstallKey+id))))
}

const classesKey = `Software\Classes\`

// URLScheme makes links to scheme: (scheme://anything) run command,
// for this user, naming them name. command has %1 where the link goes.
func URLScheme(scheme, name, icon, command string) error {
	for _, k := range []struct{ key, name, value string }{
		{classesKey + scheme, "", "URL:" + name},
		{classesKey + scheme, "URL Protocol", ""},
		{classesKey + scheme + `\DefaultIcon`, "", icon},
		{classesKey + scheme + `\shell\open\command`, "", command},
	} {
		h, err := regOpen(k.key, true)
		if err != nil {
			return err
		}
		err = regSetString(h, k.name, k.value, regSZ)
		pRegCloseKey.Call(h)
		if err != nil {
			return err
		}
	}
	return nil
}

// URLSchemeCommand is the command links to scheme: run, "" for none.
func URLSchemeCommand(scheme string) string {
	h, err := regOpen(classesKey+scheme+`\shell\open\command`, false)
	if err != nil {
		return ""
	}
	defer pRegCloseKey.Call(h)
	s, _, _ := regGetString(h, "")
	return s
}

// RemoveURLScheme takes it out again.
func RemoveURLScheme(scheme string) {
	pRegDeleteTreeW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(utf16(classesKey+scheme))))
}

// openFileName is OPENFILENAMEW.
type openFileName struct {
	StructSize    uint32
	Owner         uintptr
	Instance      uintptr
	Filter        *uint16
	CustomFilter  *uint16
	MaxCustFilter uint32
	FilterIndex   uint32
	File          *uint16
	MaxFile       uint32
	FileTitle     *uint16
	MaxFileTitle  uint32
	InitialDir    *uint16
	Title         *uint16
	Flags         uint32
	FileOffset    uint16
	FileExtension uint16
	DefExt        *uint16
	CustData      uintptr
	Hook          uintptr
	TemplateName  *uint16
	Reserved      uintptr
	Reserved2     uint32
	FlagsEx       uint32
}

const (
	ofnOverwritePrompt = 0x00000002
	ofnNoChangeDir     = 0x00000008
	ofnPathMustExist   = 0x00000800
	ofnFileMustExist   = 0x00001000
	ofnExplorer        = 0x00080000
)

// PickFile shows the standard open (or, with save, save) dialog for a
// JSON file, starting in dir and, saving, named name. It returns ""
// when the player cancels.
func PickFile(title, dir, name string, save bool) (string, error) {
	var out string
	err := com(func() error {
		buf := make([]uint16, 32768)
		if name != "" {
			copy(buf, syscall.StringToUTF16(name))
		}
		filter := syscall.StringToUTF16("Axon config (*.json)\x00*.json\x00All files (*.*)\x00*.*\x00")
		ofn := openFileName{
			Owner:       foreground(),
			Filter:      &filter[0],
			File:        &buf[0],
			MaxFile:     uint32(len(buf)),
			Title:       utf16(title),
			DefExt:      utf16("json"),
			Flags:       ofnExplorer | ofnNoChangeDir | ofnPathMustExist,
			FilterIndex: 1,
		}
		if dir != "" {
			ofn.InitialDir = utf16(dir)
		}
		ofn.StructSize = uint32(unsafe.Sizeof(ofn))
		proc := pGetOpenFileNameW
		if save {
			proc, ofn.Flags = pGetSaveFileNameW, ofn.Flags|ofnOverwritePrompt
		} else {
			ofn.Flags |= ofnFileMustExist
		}
		if r, _, _ := proc.Call(uintptr(unsafe.Pointer(&ofn))); r == 0 {
			if code, _, _ := pCommDlgExtendedError.Call(); code != 0 {
				return fmt.Errorf("file dialog error 0x%X", code)
			}
			return nil // cancelled
		}
		out = syscall.UTF16ToString(buf)
		return nil
	})
	return out, err
}

// browseInfo is BROWSEINFOW.
type browseInfo struct {
	Owner       uintptr
	Root        uintptr
	DisplayName *uint16
	Title       *uint16
	Flags       uint32
	Callback    uintptr
	LParam      uintptr
	Image       int32
}

const (
	bifReturnOnlyFSDirs = 0x00000001
	bifNewDialogStyle   = 0x00000040
	bffmInitialized     = 1
	bffmSetSelectionW   = 0x0400 + 103
)

func init() {
	// The folder dialog's one callback, which starts it at a folder.
	browseCallback = syscall.NewCallback(func(hwnd, msg, _, _ uintptr) uintptr {
		if msg == bffmInitialized && browseStart != nil {
			pSendMessageW.Call(hwnd, bffmSetSelectionW, 1, uintptr(unsafe.Pointer(browseStart)))
		}
		return 0
	})
}

// PickFolder shows the standard folder dialog, starting at dir. It
// returns "" when the player cancels.
func PickFolder(title, dir string) (string, error) {
	var out string
	err := com(func() error {
		name := make([]uint16, 260)
		browseStart = nil
		if dir != "" {
			browseStart = utf16(dir)
		}
		bi := browseInfo{Owner: foreground(), DisplayName: &name[0], Title: utf16(title),
			Flags: bifReturnOnlyFSDirs | bifNewDialogStyle, Callback: browseCallback}
		pidl, _, _ := pSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
		if pidl == 0 {
			return nil
		}
		defer pCoTaskMemFree.Call(pidl)
		path := make([]uint16, 32768)
		if r, _, _ := pSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&path[0]))); r == 0 {
			return errors.New("that is not a folder on disk")
		}
		out = syscall.UTF16ToString(path)
		return nil
	})
	return out, err
}

// foreground is the window in front: Axon's own, which the player
// just clicked in, so a dialog opens over it.
func foreground() uintptr {
	w, _, _ := pGetForegroundWindow.Call()
	return w
}

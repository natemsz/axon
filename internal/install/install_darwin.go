package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/natemsz/axon/internal/icon"
)

// Summary says, after installing, where Axon now is.
const Summary = "EveSynapse Axon is installed in Applications (your own, in your home folder). " +
	"Closing this window leaves Axon running; open the app again to bring it back, or quit it here."

const infoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>%[1]s</string>
  <key>CFBundleDisplayName</key><string>%[1]s</string>
  <key>CFBundleIdentifier</key><string>com.natemsz.axon</string>
  <key>CFBundleExecutable</key><string>axon</string>
  <key>CFBundleIconFile</key><string>axon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>%[2]s</string>
  <key>LSUIElement</key><true/>
</dict></plist>
`

func platform(l Layout, version string, logf func(string, ...any)) error {
	contents := filepath.Join(l.App, "Contents")
	res := filepath.Join(contents, "Resources")
	if err := os.MkdirAll(res, 0o755); err != nil {
		return err
	}
	plist := fmt.Sprintf(infoPlist, AppName, strings.TrimPrefix(version, "v"))
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(res, "axon.icns"), icon.ICNS(), 0o644); err != nil {
		return err
	}
	logf("made %s", l.App)
	if err := os.MkdirAll(l.Bin, 0o755); err != nil {
		return err
	}
	link := filepath.Join(l.Bin, "axon")
	// An older link, or the install script's copy: the app is Axon now.
	os.Remove(link)
	if err := os.Symlink(l.Exe, link); err != nil {
		logf("could not link %s: %v", link, err)
	}
	addToPath(l, logf)
	return nil
}

func unplatform(l Layout, logf func(string, ...any)) error {
	if err := os.RemoveAll(l.App); err != nil {
		return err
	}
	if t, err := os.Readlink(filepath.Join(l.Bin, "axon")); err == nil && t == l.Exe {
		os.Remove(filepath.Join(l.Bin, "axon"))
	}
	logf("removed %s (your config stays in %s)", l.App, filepath.Dir(l.Config))
	return nil
}

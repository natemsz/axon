#!/bin/sh
# Install EveSynapse Axon on macOS or Linux (per user, no sudo).
#
# Run from the folder holding the downloaded axon binary, or pipe it:
#
#   curl -fsSL https://raw.githubusercontent.com/natemsz/axon/main/install.sh | sh
#
# It installs `axon` into ~/.local/bin, puts that folder on your PATH,
# detects the EVE Chatlogs folder (and records it in an existing
# axon.json), creates a launcher (an app in ~/Applications on macOS,
# a desktop entry on Linux), and opens Axon straight to its GUI.
# Pass --no-launch to install without opening it.
set -eu

APP_NAME="EveSynapse Axon"
BIN_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
EXE="$BIN_DIR/axon"
GUI_ADDR="http://127.0.0.1:47611/"
LAUNCH=1
[ "${1:-}" = "--no-launch" ] && LAUNCH=0

os="$(uname -s)"; arch="$(uname -m)"
case "$os/$arch" in
  Darwin/arm64)  asset="axon-darwin-arm64" ;;
  Darwin/x86_64) asset="axon-darwin-amd64" ;;
  Linux/x86_64)  asset="axon-linux-amd64" ;;
  *) echo "No Axon build for $os/$arch." >&2; exit 1 ;;
esac

# 1. The program: beside this script (or an `axon` already), else
#    downloaded from the latest release.
mkdir -p "$BIN_DIR"
here="$(cd "$(dirname "$0")" 2>/dev/null && pwd || echo .)"
if [ -f "$here/$asset" ]; then
  install -m 0755 "$here/$asset" "$EXE"; echo "Installed $APP_NAME from $here/$asset"
elif [ -f "$here/axon" ]; then
  install -m 0755 "$here/axon" "$EXE"; echo "Installed $APP_NAME from $here/axon"
elif [ ! -x "$EXE" ]; then
  echo "Downloading $APP_NAME..."
  curl -fsSL -o "$EXE" "https://github.com/natemsz/axon/releases/latest/download/$asset"
  chmod 0755 "$EXE"
  echo "Installed $APP_NAME to $EXE"
fi

# 2. On PATH: add ~/.local/bin to the shell's startup file if needed.
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    rc="$HOME/.bashrc"; [ "$(basename "${SHELL:-}")" = "zsh" ] || [ "$os" = "Darwin" ] && rc="$HOME/.zshrc"
    if ! grep -qs "$BIN_DIR" "$rc" 2>/dev/null; then
      printf '\n# EveSynapse Axon\nexport PATH="%s:$PATH"\n' "$BIN_DIR" >> "$rc"
      echo "Added $BIN_DIR to PATH in $rc (new terminals will see 'axon')."
    fi
    ;;
esac

# 3. Detect the EVE Chatlogs folder, the way the program itself does
#    (watch.DefaultLogDirs): Documents, or the Steam Proton prefix.
rel="EVE/logs/Chatlogs"
candidates="$HOME/Documents/$rel"
if [ "$os" = "Linux" ]; then
  candidates="$HOME/.steam/steam/steamapps/compatdata/8500/pfx/drive_c/users/steamuser/Documents/$rel
$HOME/.local/share/Steam/steamapps/compatdata/8500/pfx/drive_c/users/steamuser/Documents/$rel
$HOME/Documents/$rel"
fi
logs=""
old_ifs=$IFS; IFS='
'
for d in $candidates; do
  if [ -d "$d" ]; then logs="$d"; break; fi
done
IFS=$old_ifs
if [ -n "$logs" ]; then
  echo "Found EVE chat logs: $logs"
  cfg="$BIN_DIR/axon.json"
  if [ -f "$cfg" ] && command -v python3 >/dev/null 2>&1; then
    LOGS_DIR="$logs" CFG="$cfg" python3 - <<'PY'
import json, os
p = os.environ["CFG"]
with open(p) as f: cfg = json.load(f)
if cfg.get("logs") != os.environ["LOGS_DIR"]:
    cfg["logs"] = os.environ["LOGS_DIR"]
    with open(p, "w") as f: json.dump(cfg, f, indent=2)
    print("Recorded it as \"logs\" in " + p)
PY
  fi
else
  echo "EVE chat logs not found yet. Axon will ask or find them once the game has written logs."
fi

# 4. Launcher, per operating system, opening straight to the GUI.
if [ "$os" = "Darwin" ]; then
  app="$HOME/Applications/$APP_NAME.app"
  mkdir -p "$app/Contents/MacOS"
  cat > "$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>$APP_NAME</string>
  <key>CFBundleDisplayName</key><string>$APP_NAME</string>
  <key>CFBundleIdentifier</key><string>com.natemsz.axon</string>
  <key>CFBundleExecutable</key><string>axon-launcher</string>
  <key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
EOF
  printf '#!/bin/sh\nexec "%s"\n' "$EXE" > "$app/Contents/MacOS/axon-launcher"
  chmod 0755 "$app/Contents/MacOS/axon-launcher"
  echo "Created $app"
else
  apps_dir="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
  mkdir -p "$apps_dir"
  cat > "$apps_dir/evesynapse-axon.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=$APP_NAME
Comment=Post your EVE intel channels to EveSynapse
Exec=$EXE
Terminal=false
Categories=Game;Utility;
EOF
  if [ -d "$HOME/Desktop" ]; then
    cp "$apps_dir/evesynapse-axon.desktop" "$HOME/Desktop/"
    chmod 0755 "$HOME/Desktop/evesynapse-axon.desktop"
  fi
  echo "Created $apps_dir/evesynapse-axon.desktop"
fi

# 5. Open straight to the GUI.
if [ "$LAUNCH" = "1" ]; then
  nohup "$EXE" >/dev/null 2>&1 &
  echo "Axon is opening in your browser ($GUI_ADDR)."
fi
echo "Done. Axon's settings page runs at $GUI_ADDR whenever Axon is running."

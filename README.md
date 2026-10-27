# Axon

Posts your in-game EVE Online intel channels to [EveSynapse](https://github.com/natemsz/evesynapse) as you play, so every sighting lands on the map for your corp, alliance or group, without anyone pasting anything.

axon reads the chat logs the EVE client already writes to disk. When a new line appears in one of your intel channels, it sends that line to your EveSynapse instance. There, lines that name a system become reports: hostile counts, "clear", who said it and when. They show up on the channel page and the map, fading over half an hour, and routes steer around them.

## What it does and doesn't do

- **Reads only** your EVE chat log folder (`Documents/EVE/logs/Chatlogs`), and only the logs of channels you are watching.
- **Talks only** to the EveSynapse address in your config, over HTTPS, with a credential paired to this computer. Pairing binds it to a key that never leaves this machine, and every request is signed with it, so a copied config cannot post as you. The credential can list your channels and post lines to them — nothing else. Revoke the device on the site at any time.
- On Windows, the **overlay** sends a d-scan, a local list or an intel line whole, straight from Axon, where chat would cut it short. Only what you send goes out, through the same signed posts as a watched line.
- The overlay's **shortcuts** (Ctrl+Alt+V to send intel, Ctrl+Alt+D for the intel workbench, any chord you like) only bring things up. The clipboard is read once, when you press Send with the field empty, and never otherwise. Axon installs no keyboard hook and never watches the clipboard.
- Starts at the **end** of the logs already on disk, so old chatter is never posted (unless you run it with `-replay`).
- Sends each line **once**, even when two of your characters are in the same channel.
- Installs **per user**, with no administrator rights, and runs only while you have it open (in the tray on Windows). No service, no other network traffic (its window's page is served on your own computer only). One program; the whole source is in this repository.

## Set up

1. Download the one file for your computer from the [latest release](https://github.com/natemsz/axon/releases/latest), and run it:

   | Computer | File |
   |---|---|
   | Windows | `axon-windows-amd64.exe` |
   | Mac (Apple silicon) | `axon-darwin-arm64` |
   | Mac (Intel) | `axon-darwin-amd64` |
   | Linux | `axon-linux-amd64` |

   That file is the installer and the program in one. Run from your downloads, it installs itself and opens Axon:

   - **Windows**: into `%LOCALAPPDATA%\Programs\EveSynapse Axon`, with shortcuts on the Desktop and in the Start menu, `axon` on your PATH, and an entry in **Apps & features** to uninstall it from. The **EveSynapse Axon** link on your EveSynapse site then opens Axon straight away.
   - **macOS**: as **EveSynapse Axon** in the Applications folder in your home folder, with `axon` linked into `~/.local/bin`. (Run the download once from Terminal: `chmod +x axon-darwin-*`, then `./axon-darwin-arm64`.)
   - **Linux**: into `~/.local/bin`, with an entry in your applications menu and on the Desktop. The **EveSynapse Axon** link on your EveSynapse site then opens Axon straight away. (`chmod +x axon-linux-amd64`, then run it.)

   It finds your EVE chat logs folder, and carries over an `axon.json` (and `axon.key`) saved beside the download or left by the old install scripts. Running a newer download the same way updates Axon in place; your config stays.
2. On your EveSynapse site, open **Intel → Channels**, scroll to **EveSynapse Axon**, and click **Make a pairing code**. It is good for 10 minutes.
3. In Axon's window, enter the site address and the code under **EveSynapse**, and click **Pair**. Axon saves its credential into `axon.json` and its key beside it in `axon.key`. Then **Start watching**.

Axon's window shows which in-game channels it is watching, and a line each time it posts:

```
11:20:01 signed in as Your Pilot; watching:
11:20:01   in-game "delve intel" -> Delve Intel (#12)
11:20:01 reading chat logs in C:\Users\you\Documents\EVE\logs\Chatlogs
11:24:37 Delve Intel: 1 new report
```

## Axon's window

Run Axon (its shortcut, or `axon`) and its window opens. On Windows it is a window of Axon's own, drawn by WebView2, the Edge engine built into Windows. Elsewhere it is an app window of Chrome, Edge, Chromium or Brave (whichever is installed) with a profile of its own, so it never touches your own browsing; with none of them, the page opens in your browser. Starting Axon again while it runs just brings its window back. Once you have paired, your EveSynapse site shows an **EveSynapse Axon** link in its Intel menu and on its Channels page. On Windows and Linux, clicking it opens Axon's window, starting Axon first if it isn't running. Your browser may ask once whether to open EveSynapse Axon; say yes (and tick "always allow" if it offers). On a Mac, open Axon from your Applications folder. In the window you can:

- **pair with your EveSynapse site** using a one-time code, and **check the sign-in**, which lists the channels you can post to;
- choose the **chat log folder**, or let it be found, and see which channels were logged this week;
- **map** in-game channels to site channels, picking both from lists;
- choose, import or paste the **config file**, and save it (or save as);
- **start and stop watching**, with or without replaying today's logs, and follow what it posts.

The page is served by Axon itself, on `127.0.0.1` only. Nothing outside your computer can reach it, and it only answers requests that carry the key Axon's window was opened with.

### Closing and the tray (Windows)

Closing Axon's window does not stop Axon: it goes on watching in the tray, by the clock. Double-click the tray icon to bring the window back, or right-click it to start or stop watching, open the log file, or quit. **Quit** in the window stops it too.

`axon -tray` starts straight in the tray and begins watching with your config. Put a shortcut to `axon.exe -tray` in your Startup folder (`Win+R`, `shell:startup`) to have it running whenever you log in.

macOS and Linux have no tray icon (it would need libraries outside Go's standard library). There, closing the window leaves Axon running; open Axon again to bring the window back, and use **Quit** to stop it.

### The overlay (Windows)

The EVE client cuts a long paste dragged into a channel at its message limit, so the tail of a d-scan (the celestials that say which system it is) or a busy local never reaches the channel. The overlay is the door around that: a slim, see-through bar that stays on top of the game, with a paste field and **Send**, and nothing else.

- Press **Ctrl+Alt+V** (or your own shortcut) and the bar comes up with the keyboard in its field.
- Press **Enter** with the field empty to send what you copied, whole. That is the fast path: copy in game, shortcut, Enter.
- Or paste into the field first. The bar then shows what it reads: the type of paste (D-Scan, Local, Fleet, Probe scan, or an intel line) and the system the site would file it under. Change either if it is wrong; your choice travels with the paste, and the site still reads it itself.
- After a send the game gets the keyboard back and the bar stays where you put it, so the next send is the shortcut and Enter again. **Esc** also hands the keyboard back. Drag the bar by its dotted grip; **×** hides it until the shortcut.

A send goes through the same signed path as a watched line, as the character whose chat log changed most recently (the one you are playing), falling back to your paired character when no log says. The status line says what became of it. A failed send keeps the field as it was. The overlay posts to the channel chosen under **Overlay and shortcuts** in Axon's window, or the first channel your account may post to.

#### Shortcuts

Set under **Overlay and shortcuts**: click a box and press the keys you want; **Clear** unbinds one. They take effect when you save.

| Shortcut | Default | Does |
|---|---|---|
| Send intel | Ctrl+Alt+V | brings up the overlay |
| Intel workbench | Ctrl+Alt+D | opens the site's intel workbench in Axon's panel, on top of the game |
| Map | none | opens the site's map in the panel |
| Feed | none | opens the site's intel feed in the panel |

The panel is a window of Axon's own that stays on top of the game; sign into the site there once, as in any browser. Pressing the same shortcut again puts it away.

A shortcut needs Ctrl, Alt, Shift or Win unless it is a key nothing else uses (F13 to F24, Pause, Scroll Lock), so none fires while you type in chat. A chord another program already holds is named in the activity log.

#### What Axon reads, and when

Nothing is sent that you did not send. The clipboard is read once, at the moment you press Send with the field empty, and at no other time. There is no keyboard hook, no clipboard listener and no polling: Windows is asked to register the chords you chose, and tells Axon only when one is pressed.

### Windows SmartScreen and antivirus

Axon's releases are not yet signed with a code-signing certificate, so Windows may warn that it is from an unknown publisher, and Microsoft Defender's machine-learning detection has been known to flag new unsigned Go programs like it. The program's source is all here, and every release's `SHA256SUMS` is signed with the release key (`release-signing.pub`): check a download against it. The overlay's shortcuts are global hotkeys, which such heuristics sometimes look at twice; what they do, and when the clipboard is read, is set out under **The overlay** above, and clearing every shortcut registers none.

## Which channels it watches

A channel on the site with **the same name** as your in-game channel is matched automatically (upper and lower case don't matter). If the names differ, map them in `axon.json`:

```json
{
  "server": "https://your-evesynapse.example",
  "token": "esw_…",
  "channels": {
    "Delve.Intel": 12,
    "Pipe intel (alt)": 12
  }
}
```

The number is the site channel's number, the one in its address (`/intel/channels/12/`). Only channels your account may **post** to are used.

## Options

| | |
|---|---|
| `-config path.json` | Use another config file. By default, the installed one (beside the program on Windows and Linux, in `~/Library/Application Support/EveSynapse Axon` on macOS); a program not installed reads `axon.json` in the current folder, else beside itself. |
| `-logs folder` | The EVE `Chatlogs` folder, if it isn't found on its own. You can also set `"logs"` in the config. |
| `-replay` | Also send what the logs already hold, starting with today's. |
| `-watch` | Watch from the terminal, without a window. |
| `-tray` | Start watching in the tray (Windows; elsewhere, as without it). |
| `-install` | Install (or update) from this file, even with other options given. |
| `-portable` | Run this file where it is, without installing it. |
| `-url link` | Used when you click Axon's link on your EveSynapse site: brings up Axon's window. You never need to type it. |
| `-uninstall` | Remove the installed Axon: the program, shortcuts, PATH entry, launcher, and what makes the site's link open it. Your `axon.json`, `axon.key` and log stay. On Windows, **Apps & features** does the same. |

With `-watch`, stop it with Ctrl+C. Lines it couldn't send yet (site unreachable) are sent first.

## Build from source

```
go build .
go test ./...
```

Go 1.27 or newer; no dependencies outside the standard library.

## How it talks to the site

Axon pairs once at `POST /api/intel/v1/pair` (a one-time code from the site, plus this machine's Ed25519 public key), then uses EveSynapse's intel channel API with `Authorization: Bearer <credential>`, every request signed with the device key (`X-Axon-Timestamp`, `X-Axon-Signature` over the method, path, timestamp and body hash):

- `GET /api/intel/v1/channels` → the account's channels, and which it may post to
- `POST /api/intel/v1/channels/{id}/lines` with `{"lines": ["[ 2026.10.21 11:20:05 ] Scout > 1DQ1-A +3", …]}` → reports posted, duplicates, lines skipped

---

EVE Online and all associated material are the intellectual property of CCP Games hf. This project is not affiliated with or endorsed by CCP Games.

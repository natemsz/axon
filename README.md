# Axon

Posts your in-game EVE Online intel channels to [EveSynapse](https://github.com/natemsz/evesynapse) as you play, so every sighting lands on the map for your corp, alliance or group, without anyone pasting anything.

axon reads the chat logs the EVE client already writes to disk. When a new line appears in one of your intel channels, it sends that line to your EveSynapse instance. There, lines that name a system become reports: hostile counts, "clear", who said it and when. They show up on the channel page and the map, fading over half an hour, and routes steer around them.

## What it does and doesn't do

- **Reads only** your EVE chat log folder (`Documents/EVE/logs/Chatlogs`), and only the logs of channels you are watching.
- **Talks only** to the EveSynapse address in your config, with a posting token you made there. The token can list your channels and post lines to them — nothing else. Revoke it on the site at any time.
- Starts at the **end** of the logs already on disk, so old chatter is never posted (unless you run it with `-replay`).
- Sends each line **once**, even when two of your characters are in the same channel.
- No background service, no other network traffic (the settings page is served on your own computer only). One small program; the whole source is in this repository.

## Set up

1. On your EveSynapse site, open **Intel → Channels**, scroll to **EveSynapse Axon**, and click **Make a token**.
2. The page shows a ready-made `axon.json`. Save it next to the program (or import it on the settings page, below).
3. Install Axon from the [latest release](https://github.com/natemsz/axon/releases/latest) with the installer for your computer — it puts Axon on your PATH, finds your EVE chat logs folder, makes a shortcut, and opens Axon straight to its settings page. On Windows, right-click `install.ps1` and **Run with PowerShell** (or `irm https://raw.githubusercontent.com/natemsz/axon/main/install.ps1 | iex`). On macOS and Linux, run `sh install.sh` (or `curl -fsSL https://raw.githubusercontent.com/natemsz/axon/main/install.sh | sh`). Or just download the program:

   | Computer | Installer | Or just the program |
   |---|---|
   | Windows | `install.ps1` | `axon-windows-amd64.exe` |
   | Mac (Apple silicon) | `install.sh` | `axon-darwin-arm64` |
   | Mac (Intel) | `install.sh` | `axon-darwin-amd64` |
   | Linux | `install.sh` | `axon-linux-amd64` |

4. Run it — double-click the shortcut, or type `axon`. It opens straight to its settings page in your browser; press **Start watching** there when your config is ready.

It prints which in-game channels it is watching, and a line each time it posts:

```
11:20:01 signed in as Your Pilot; watching:
11:20:01   in-game "delve intel" -> Delve Intel (#12)
11:20:01 reading chat logs in C:\Users\you\Documents\EVE\logs\Chatlogs
11:24:37 Delve Intel: 1 new report
```

## The settings page

Run `axon` (or `axon -gui`) and it opens straight to a settings page in your browser, always at the same address: `http://127.0.0.1:47611/`. Starting Axon again while it runs just reopens that page. Your EveSynapse site links to it from its Intel menu and Channels page while Axon is running. On it you can:

- **choose the config file**: browse for one, type its path, import a downloaded `axon.json`, or paste the one the site showed you;
- set the **site address and token**, and **check the sign-in**, which lists the channels the token can post to;
- choose the **chat log folder**, or let it be found, and see which channels were logged this week;
- **map** in-game channels to site channels, picking both from lists;
- **save** (or save as), **start and stop watching**, with or without replaying today's logs, and follow what it posts.

The page is served by axon itself, on `127.0.0.1` only. Nothing outside your computer can reach it, and it only accepts requests that carry the key in the address it opened.

### The tray (Windows)

On Windows, axon keeps an icon in the tray, by the clock, while the settings page is open. Click **Minimize to tray** on the page to hide axon's window; you can then close the browser tab. Double-click the icon to open the settings again, or right-click it to start or stop watching, show the log window, or quit.

`axon -tray` starts straight in the tray and begins watching with your config. Make a shortcut to `axon-windows-amd64.exe -tray` in your Startup folder (`Win+R`, `shell:startup`) to have it running whenever you log in.

macOS and Linux have no tray icon (it would need libraries outside Go's standard library); there, `-tray` behaves like `-gui` and starts watching.

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
| `-config path.json` | Use another config file. By default, `axon.json` in the current folder, else next to the program. |
| `-logs folder` | The EVE `Chatlogs` folder, if it isn't found on its own. You can also set `"logs"` in the config. |
| `-replay` | Also send what the logs already hold, starting with today's. |
| `-gui` | Open the settings page (the default when you just run `axon`). |
| `-watch` | Watch from the console, without the settings page. |
| `-tray` | Start watching from a tray icon (Windows; elsewhere, as `-gui`). |

Stop it with Ctrl+C. Lines it couldn't send yet (site unreachable) are sent first.

## Build from source

```
go build .
go test ./...
```

Go 1.27 or newer; no dependencies outside the standard library.

## How it talks to the site

axon uses EveSynapse's intel channel API with `Authorization: Bearer <token>`:

- `GET /api/intel/v1/channels` → the account's channels, and which it may post to
- `POST /api/intel/v1/channels/{id}/lines` with `{"lines": ["[ 2026.10.21 11:20:05 ] Scout > 1DQ1-A +3", …]}` → reports posted, duplicates, lines skipped

---

EVE Online and all associated material are the intellectual property of CCP Games hf. This project is not affiliated with or endorsed by CCP Games.

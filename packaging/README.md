# DarkServe as an AppImage

One file that carries the whole server — the binary, the web UI, ffmpeg and
yt-dlp. Copy it to a USB stick, send it to a friend, run it on any x86_64 or
arm64 Linux machine. Nothing to install.

```
./DarkServe-0.3.0-x86_64.AppImage
```

The server starts in the background and the browser opens on
<http://localhost:7676>.

## Build it

```sh
./packaging/build-appimage.sh                  # x86_64
./packaging/build-appimage.sh --arch aarch64   # arm64 (Raspberry Pi)
./packaging/build-appimage.sh --arch all       # both
./packaging/build-appimage.sh --no-tools       # without ffmpeg and yt-dlp
```

The result lands in `dist/`. Everything that is downloaded (the AppImage
runtime, ffmpeg, yt-dlp) is cached in `.appimage-cache/`, so a rebuild only
repeats what changed.

Requirements on the build machine: `go`, `mksquashfs`, `curl` or `wget`.

## What is inside

```
AppRun                  the launcher: starts the server, opens the browser
darkserve.desktop       menu entry, with Start / Open / Stop / Kill / Log
darkserve.png           the icon
usr/bin/darkserve       the server (static Go binary, no libc needed)
usr/bin/ffmpeg          static ffmpeg, for compatibility streaming
usr/bin/ffprobe         static ffprobe, for media info
usr/bin/yt-dlp          yt-dlp, for downloads
usr/share/metainfo/     AppStream data for software centres
```

ffmpeg and yt-dlp come from johnvansickle.com and the yt-dlp releases. They
are static builds, so the AppImage does not depend on what the host has
installed.

## Using it

| Command | What it does |
| --- | --- |
| `DarkServe.AppImage` | start the server, open the browser |
| `DarkServe.AppImage --cli start` | start the server only |
| `DarkServe.AppImage --cli open` | open the browser |
| `DarkServe.AppImage --cli stop` | stop the server |
| `DarkServe.AppImage --cli kill` | stop it the hard way |
| `DarkServe.AppImage --cli status` | is it running? |
| `DarkServe.AppImage --cli log` | the last log lines |
| `DarkServe.AppImage --panel` | the yad control panel (needs `yad`) |

The port comes from `DARKSERVE_PORT`, the default is 7676.

## Where the data lives

Nothing is written inside the AppImage — it stays read-only. Everything goes
into the home directory of the user who runs it:

```
~/.local/share/darkserve/         the database, the index, the log
~/.local/share/darkserve/runtime/ the server and its tools, copied out
~/.cache/darkserve/                artwork and other cached files
```

The server is copied out of the AppImage on the first start, because the
AppImage is unmounted as soon as the launcher ends and a server running from
that mount would die with it. The same happens with ffmpeg, ffprobe and
yt-dlp — they are copied in the background while the browser is already
open, which takes a few seconds and about 240 MB. Later starts reuse them.

So every user on a machine gets their own library. To share one server
between several people, run it once and let them connect to the address the
server prints on start.

## Stopping the server

The server keeps running after the launcher exits — that is the point, it is
a server. Stop it in one of three ways:

- the **Shut down server** button in the settings page (admin only),
- `DarkServe.AppImage --cli stop`,
- the *Stop Server* entry in the right-click menu of the launcher icon.

## Notes for the people you hand it to

- **FUSE**: the AppImage mounts itself with FUSE. On a system without
  `libfuse2` (Ubuntu 24.04 and newer, for example) install
  `libfuse2t64` or run it with `--appimage-extract-and-run`.
- **Make it executable**: `chmod +x DarkServe.AppImage` after copying it.
- **First run**: the browser asks you to create the admin account. That
  account can change every setting and stop the server.
- **USB drives**: the server watches `/media`, `/mnt` and `/run/media`. Point
  it somewhere else in the settings.
- **Drives that do not mount themselves**: Kali and other forensic
  distributions turn automount off, so a plugged-in drive stays invisible.
  The *Drives* page lists drives that are plugged in but not mounted, and
  the admin can mount them from there.

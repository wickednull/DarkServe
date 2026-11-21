# DarkServe

A media server you can carry on a USB stick.

DarkServe indexes the films, series and music already on your disks and serves
them to every browser in the house — no account in the cloud, no internet
required. The whole server is one Go binary with the web UI compiled in, and it
also ships as a single-file AppImage that bundles `ffmpeg`, `ffprobe` and
`yt-dlp`, so a machine that has never heard of DarkServe can run it by
double-clicking one file.

```
./DarkServe-0.3.0-x86_64.AppImage
→ DarkServe up → http://localhost:7676
```

## What it does

- **Libraries** — point it at folders or whole drives; it walks them, groups
  films and episodes, and fetches artwork and metadata.
- **Plays almost anything** — direct play where the browser can, and a
  compatibility stream (HLS, transcoded on the fly by ffmpeg) where it cannot.
- **USB drives** — watched at `/media`, `/mnt` and `/run/media`. On systems
  with automount turned off (Kali, forensic distros) the *Drives* page lists
  plugged-in but unmounted drives, and an admin can mount them from there.
- **Downloads** — anything `yt-dlp` understands, into the library.
- **Subtitles** — search and fetch, OpenSubtitles key optional.
- **IPTV** — M3U playlists alongside the local library.
- **Continue watching, favourites, duplicates** — per user, kept in SQLite.
- **Users** — an admin account, ordinary accounts, and a password reset that
  keeps everybody's watch progress.
- **Casting** — Chromecast support, off by default.
- **Runs headless** — start it once and it stays up; stop it from the settings
  page, the CLI, or the tray menu.

## Quick start

### The AppImage (nothing to install)

Grab `DarkServe-<version>-x86_64.AppImage` (or `-aarch64` for ARM boards) from
the [Releases](../../releases) page, then:

```sh
chmod +x DarkServe-*-x86_64.AppImage
./DarkServe-*-x86_64.AppImage
```

The binaries are attached to a release rather than committed, because the
x86_64 file is just over GitHub's 100 MiB limit for a tracked file. Running the
*release* workflow (Actions → release → Run workflow) builds both architectures
and attaches them; pushing a `v*` tag does the same thing.

The first start asks you to create the admin account. On systems without
`libfuse2` (Ubuntu 24.04 and newer) install `libfuse2t64` or run it with
`--appimage-extract-and-run`.

Build one yourself with `./packaging/build-appimage.sh` — see
[packaging/README.md](packaging/README.md) for the details, the CLI flags and
what ends up inside the file.

### From source

```sh
git clone <this repo> darkserve && cd darkserve
go build -o darkserve ./cmd
./start.sh          # starts it in the background, opens nothing
./stop.sh
```

`start.sh` keeps its database and cache inside the checkout (`data/`, `cache/`),
which is convenient while developing. `go run ./cmd` works too and stays in the
foreground.

Requires Go 1.21 or newer. The only dependency of note is
`modernc.org/sqlite`, a pure-Go SQLite, so the binary is static and needs no
cgo.

## Configuration

DarkServe reads a small YAML file; every value has a default, and the settings
page edits the same options at runtime.

```yaml
addr: ":7676"                    # listen address
data_dir: "/path/to/data"        # database, index, log, pid file
cache_dir: "/path/to/cache"      # artwork and transcode scratch

usb_paths: ["/media", "/mnt", "/run/media"]
local_paths: ["$HOME/Videos", "$HOME/Music"]
min_size_mb: 1

artwork_online: true             # fetch posters from the internet
tmdb_key: ""                     # optional, improves matching
os_sub_key: ""                   # optional, OpenSubtitles
cast_enabled: false
ytdlp_path: ""                   # bundled one is found automatically
```

Run it with `./darkserve -config darkserve.conf`. Without a config file it uses
`~/.local/share/darkserve` and `~/.cache/darkserve`.

## Project layout

```
cmd/            entry point, flags, daemon mode
internal/
  auth/         accounts, password hashing, sessions
  config/       the YAML config and its defaults
  db/           schema and migrations (SQLite)
  scanner/      walking the libraries, grouping episodes
  server/       HTTP handlers and the API
  usb/          drive watching, unmounted-drive detection
  ytdlp/        downloads
web/            templates and static assets, compiled into the binary
packaging/      AppRun, build script, desktop entry, AppStream data
launcher/       desktop launcher and the yad control panel
```

## Where the data lives

Nothing is written next to the binary. The AppImage keeps the server and its
tools in `~/.local/share/darkserve/runtime/` and the library in
`~/.local/share/darkserve/`; artwork goes to `~/.cache/darkserve/`.

## License

No license has been chosen, so the usual default applies: the source is here
to read and the AppImage is here to run, but no rights to copy, modify or
redistribute the code are granted. Ask if you want to build on it.

Third-party components keep their own licenses: SQLite (via
`modernc.org/sqlite`), hls.js, ffmpeg, ffprobe and yt-dlp.

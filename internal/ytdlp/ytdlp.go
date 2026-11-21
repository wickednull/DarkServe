package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// lastLine returns the most informative (last) stderr line of a run.
func lastLine(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// Finder locates a working yt-dlp binary. An empty path means "look on PATH".
type Finder struct {
	Path string
}

// Locate returns the yt-dlp executable to use, or "" when none is available.
func (f Finder) Locate() string {
	if strings.TrimSpace(f.Path) != "" {
		if p, err := exec.LookPath(strings.TrimSpace(f.Path)); err == nil {
			return p
		}
		// The setting may already be an absolute path.
		if strings.HasPrefix(f.Path, "/") {
			return f.Path
		}
		return ""
	}
	if p, err := exec.LookPath("yt-dlp"); err == nil {
		return p
	}
	if p, err := exec.LookPath("youtube-dl"); err == nil {
		return p
	}
	return ""
}

// Version reports the tool version, used by the settings page.
func (f Finder) Version() string {
	bin := f.Locate()
	if bin == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Format picks what to download.
type Format struct {
	// Kind is "video" or "audio".
	Kind string
	// Quality is a yt-dlp format selector ("best", "bestvideo[height<=?1080]+bestaudio")
	// or an audio bitrate ("320", "192", "0" for best).
	Quality string
	// AudioContainer is used when Kind is "audio": mp3, m4a, opus, flac, wav.
	AudioContainer string
	// Subtitles downloads and embeds sidecar subtitles when ffmpeg is present.
	Subtitles bool
	// Playlist downloads every entry instead of only the first.
	Playlist bool
}

// Job is one running or finished download. It is safe for concurrent use.
type Job struct {
	ID       int
	URL      string
	DestDir  string
	Library  int
	Format   Format
	Title    string
	File     string
	Status   string // queued, downloading, finished, error, cancelled
	Percent  float64
	Speed    float64
	Bytes    int64
	Total    int64
	ETA      int64
	Error    string
	Started  time.Time
	Finished time.Time

	mu     sync.Mutex
	cancel context.CancelFunc
	cmd    *exec.Cmd
}

// Downloader owns the job set and hands out ids.
type Downloader struct {
	Finder Finder
	mu     sync.Mutex
	jobs   map[int]*Job
	next   int

	// MaxConcurrent caps how many downloads run at once so a slow drive is
	// not saturated. Extra jobs wait in the queue.
	MaxConcurrent int
}

func New(finder Finder) *Downloader {
	return &Downloader{Finder: finder, jobs: map[int]*Job{}, next: 1}
}

// SetPath updates the yt-dlp location, so a change in Settings applies to
// downloads started afterwards without a restart.
func (d *Downloader) SetPath(p string) {
	d.mu.Lock()
	d.Finder.Path = strings.TrimSpace(p)
	d.mu.Unlock()
}

// Jobs returns a snapshot of every job, newest first.
// Snapshot returns a copy of the fields the API needs, safe to read while the
// download is still running.
func (j *Job) Snapshot() map[string]interface{} {
	j.mu.Lock()
	defer j.mu.Unlock()
	return map[string]interface{}{
		"id": j.ID, "url": j.URL, "title": j.Title, "file": j.File,
		"status": j.Status, "percent": j.Percent, "speed": j.Speed,
		"bytes": j.Bytes, "total": j.Total, "eta": j.ETA,
		"error": j.Error, "started": j.Started, "finished": j.Finished,
		"library": j.Library, "kind": j.Format.Kind,
	}
}

func (d *Downloader) Jobs() []*Job {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*Job, 0, len(d.jobs))
	for _, j := range d.jobs {
		out = append(out, j)
	}
	return out
}

// Job returns one job by id.
func (d *Downloader) Job(id int) *Job {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.jobs[id]
}

// Cancel stops a running job and removes its partial file when no other job
// is writing to the same folder.
func (d *Downloader) Cancel(id int) bool {
	d.mu.Lock()
	j := d.jobs[id]
	d.mu.Unlock()
	if j == nil {
		return false
	}
	j.mu.Lock()
	done := j.cancel != nil
	if done {
		j.cancel()
		j.Status = "cancelled"
	}
	destDir := j.DestDir
	j.mu.Unlock()
	if done {
		d.cleanPartials(destDir)
	}
	return done
}

// cleanPartials removes yt-dlp leftover partial files (.part, .ytdl) from a
// download folder, but only while nothing else is downloading into it.
func (d *Downloader) cleanPartials(dir string) {
	d.mu.Lock()
	for _, other := range d.jobs {
		other.mu.Lock()
		busy := other.cancel != nil && other.Status == "downloading" && other.DestDir == dir
		other.mu.Unlock()
		if busy {
			d.mu.Unlock()
			return
		}
	}
	d.mu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".part") || strings.HasSuffix(n, ".ytdl") || strings.Contains(n, ".part-Frag") {
			os.Remove(filepath.Join(dir, n))
		}
	}
}

// BuildArgs assembles the yt-dlp command line for a job.
func BuildArgs(f Format, destDir string) []string {
	args := []string{
		// --progress is required for the template to emit at all.
		"--newline", "--no-warnings", "--progress",
		"--progress-template",
		"PROG|%(progress.downloaded_bytes)s|%(progress.total_bytes)s|%(progress.speed)s|%(progress.eta)s|%(progress.status)s",
		"--print", "after_move:FILEPATH|%(filepath)s",
		"-P", destDir,
	}
	if f.Kind == "audio" {
		args = append(args, "-x")
		if c := f.AudioContainer; c != "" {
			args = append(args, "--audio-format", c)
		}
		if q := strings.TrimSpace(f.Quality); q != "" {
			args = append(args, "--audio-quality", q)
		}
	} else {
		q := strings.TrimSpace(f.Quality)
		if q == "" {
			q = "best"
		}
		// A height-capped selector keeps downloads usable on phones.
		if q == "best" || q == "bestaudio" {
			args = append(args, "-f", "bv*+ba/b")
		} else {
			args = append(args, "-f", fmt.Sprintf("bv*[height<=?%s]+ba/b[height<=?%s]/b", q, q))
		}
		args = append(args, "--merge-output-format", "mp4")
	}
	if f.Subtitles {
		args = append(args, "--write-subs", "--sub-langs", "en.*,en", "--embed-subs")
	}
	if f.Playlist {
		args = append(args, "--yes-playlist")
	} else {
		args = append(args, "--no-playlist")
	}
	return args
}

// Start launches a download. It returns once the process is running; progress
// is streamed in the background and onDone runs when the job leaves the queue.
// Start queues a download and starts it when a slot is free.
func (d *Downloader) Start(url, destDir string, f Format, library int, onDone func(*Job)) (*Job, error) {
	if d.Finder.Locate() == "" {
		return nil, fmt.Errorf("yt-dlp not found — install it or set its path in Settings")
	}
	d.mu.Lock()
	id := d.next
	d.next++
	j := &Job{
		ID: id, URL: url, DestDir: destDir, Library: library, Format: f,
		Status: "queued", Started: time.Now(),
	}
	d.jobs[id] = j
	d.mu.Unlock()

	d.pump(func(j *Job) { d.launch(j, url, destDir, f, onDone) })
	return j, nil
}

// pump starts queued jobs while fewer than MaxConcurrent are running.
func (d *Downloader) pump(launch func(*Job)) {
	if d.MaxConcurrent < 1 {
		d.MaxConcurrent = 2
	}
	running := 0
	for _, j := range d.Jobs() {
		if j.Status == "downloading" {
			running++
		}
	}
	for _, j := range d.Jobs() {
		if running >= d.MaxConcurrent {
			return
		}
		if j.Status != "queued" {
			continue
		}
		j.mu.Lock()
		j.Status = "downloading"
		j.mu.Unlock()
		running++
		launch(j)
	}
}

func (d *Downloader) launch(j *Job, url, destDir string, f Format, onDone func(*Job)) {
	bin := d.Finder.Locate()
	if bin == "" {
		j.fail("yt-dlp not found — install it or set its path in Settings")
		if onDone != nil {
			onDone(j)
		}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, append(BuildArgs(f, destDir), url)...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		j.fail(err.Error())
		return
	}
	j.cancel = cancel

	if err := cmd.Start(); err != nil {
		j.fail(err.Error())
		cancel()
		if onDone != nil {
			onDone(j)
		}
		return
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			parseLine(j, sc.Text())
		}
		waitErr := cmd.Wait()
		j.mu.Lock()
		switch {
		case j.Status == "cancelled":
			// leave as is
		case waitErr != nil:
			j.Status = "error"
			msg := lastLine(errBuf.String())
			if msg == "" {
				msg = waitErr.Error()
			}
			j.Error = msg
		default:
			j.Status = "finished"
		}
		j.Finished = time.Now()
		j.Percent = 100
		j.mu.Unlock()
		if onDone != nil {
			onDone(j)
		}
		// A slot just opened up: start the next queued download.
		d.pump(func(next *Job) { d.launch(next, next.URL, next.DestDir, next.Format, onDone) })
	}()
}

// parseLine reads yt-dlp's machine-readable progress and the final file path.
func parseLine(j *Job, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if strings.HasPrefix(line, "FILEPATH|") {
		j.mu.Lock()
		j.File = strings.TrimPrefix(line, "FILEPATH|")
		j.mu.Unlock()
		return
	}
	if strings.HasPrefix(line, "[download] Destination:") {
		j.mu.Lock()
		j.File = strings.TrimSpace(strings.TrimPrefix(line, "[download] Destination:"))
		if j.Status == "downloading" {
			j.Title = fileName(j.File)
		}
		j.mu.Unlock()
		return
	}
	if strings.HasPrefix(line, "PROG|") {
		// Template order: downloaded, total, speed, eta, status.
		parts := strings.Split(strings.TrimPrefix(line, "PROG|"), "|")
		if len(parts) < 5 {
			return
		}
		j.mu.Lock()
		j.Bytes = atoi64(parts[0])
		j.Total = atoi64(parts[1])
		j.Speed = atof64(parts[2])
		j.ETA = atoi64(parts[3])
		switch parts[4] {
		case "finished":
			j.Percent = 100
			j.Status = "finished"
		case "error":
			j.Status = "error"
		default:
			switch {
			case j.Total > 0 && j.Bytes > 0:
				j.Percent = float64(j.Bytes) / float64(j.Total) * 100
			case j.Total > 0:
				j.Percent = -1 // total known, bytes not reported yet
			case j.Bytes > 0:
				j.Percent = -1 // indeterminate
			}
		}
		j.mu.Unlock()
	}
}

func (j *Job) fail(msg string) {
	j.mu.Lock()
	j.Status = "error"
	j.Error = msg
	j.Finished = time.Now()
	j.mu.Unlock()
}

func fileName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 && i+1 < len(p) {
		return p[i+1:]
	}
	return p
}

func atoi64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "NA" || s == "Inf" {
		return 0
	}
	n, _ := strconv.ParseFloat(s, 64)
	return int64(n)
}

func atof64(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "NA" || s == "Inf" {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

package usb

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Drive struct {
	Mount  string
	Label  string
	FSType string
	Size   uint64
	Used   uint64
}

type Detector struct {
	mu        sync.Mutex
	paths     []string
	lastDrive []Drive
}

func NewDetector(paths []string) *Detector {
	return &Detector{paths: paths}
}

// SetPaths updates the scan roots live (used when admin saves settings).
func (d *Detector) SetPaths(p []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paths = p
}

func (d *Detector) Scan() []Drive {
	d.mu.Lock()
	defer d.mu.Unlock()
	drives := []Drive{}
	seen := map[string]bool{}
	for _, root := range d.paths {
		drives = append(drives, d.scanLevel(root, seen, 0)...)
	}
	// Remember this result so the next Watch poll can compare against it.
	// Watch must read the previous value before calling Scan, or it ends up
	// comparing a result against itself and never sees a change.
	d.lastDrive = drives
	return drives
}

// lastDrives returns the result of the previous scan.
func (d *Detector) lastDrives() []Drive {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastDrive
}

// scanLevel walks up to depth 2 so nested user layouts like
// /media/<user>/<volume> and /run/media/<user>/<volume> are covered.
func (d *Detector) scanLevel(root string, seen map[string]bool, depth int) []Drive {
	var out []Drive
	if depth > 2 {
		return out
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name())
		if seen[p] {
			continue
		}
		if isMountpoint(p) {
			seen[p] = true
			if drv, ok := probeMount(p); ok {
				out = append(out, drv)
			}
			continue // children of a mountpoint belong to the same fs
		}
		// Not a mountpoint itself — descend to catch /media/<user>/<vol>.
		out = append(out, d.scanLevel(p, seen, depth+1)...)
	}
	return out
}

// isMountpoint returns true when the target lives on a different device
// than its parent directory (st_dev comparison). This kills false
// positives like /media/cdrom (an empty folder that is not a mount).
func isMountpoint(path string) bool {
	pfi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return false
	}
	cfi, err := os.Stat(path)
	if err != nil {
		return false
	}
	pdev, ok1 := devOf(pfi)
	cdev, ok2 := devOf(cfi)
	if !ok1 || !ok2 {
		return false
	}
	return pdev != cdev
}

func devOf(fi os.FileInfo) (uint64, bool) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Dev, true
	}
	return 0, false
}

func probeMount(path string) (Drive, bool) {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return Drive{}, false
	}
	size := st.Blocks * uint64(st.Bsize)
	used := (st.Blocks - st.Bfree) * uint64(st.Bsize)
	if size == 0 {
		return Drive{}, false
	}
	return Drive{
		Mount:  path,
		Label:  filepath.Base(path),
		FSType: fsTypeName(int64(st.Type)),
		Size:   size,
		Used:   used,
	}, true
}

func fsTypeName(t int64) string {
	switch t {
	case 0x4d44:
		return "vfat"
	case 0x6969:
		return "nfs"
	case 0xEF53:
		return "ext4"
	case 0x5346544e:
		return "ntfs"
	case 0x9123683e:
		return "btrfs"
	case 0xc364:
		return "exfat"
	default:
		return "unknown"
	}
}

func (d *Detector) Watch(ctx context.Context, onChange func([]Drive)) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Capture the last poll before Scan overwrites it, so a drive
			// appearing or disappearing after startup is actually noticed.
			prev := d.lastDrives()
			drives := d.Scan()
			if !sameDrives(drives, prev) && onChange != nil {
				onChange(drives)
			}
		}
	}
}

func sameDrives(a, b []Drive) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Mount != b[i].Mount {
			return false
		}
	}
	return true
}

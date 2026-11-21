package usb

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Device is a storage partition the system knows about but has not mounted.
//
// Kali and other forensic distributions switch automount off, so a drive
// that is plugged in stays invisible until somebody mounts it. DarkServe
// only ever sees mounted drives, so it offers to mount the rest.
type Device struct {
	Path   string `json:"path"`   // /dev/sda1
	Label  string `json:"label"`  // USB DISK
	Size   uint64 `json:"size"`   // bytes
	Parent string `json:"parent"` // /dev/sda
}

// devicePath keeps only plain device nodes, so nothing can point the mount
// command somewhere else.
var devicePath = regexp.MustCompile(`^/dev/[a-zA-Z0-9]+$`)

// Unmounted lists the partitions of USB devices that are not mounted yet.
func Unmounted() []Device {
	out := []Device{}
	mounted := mountedDevices()
	labels := diskLabels()

	blocks, err := os.ReadDir("/sys/block")
	if err != nil {
		return out
	}
	for _, b := range blocks {
		name := b.Name()
		// Only real disks: sd*, mmcblk*, hd* — no loop, ram or zram.
		if !strings.HasPrefix(name, "sd") && !strings.HasPrefix(name, "mmcblk") &&
			!strings.HasPrefix(name, "hd") {
			continue
		}
		dir := filepath.Join("/sys/block", name)
		// USB only. The kernel path of a USB disk runs through a usb bus.
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || !strings.Contains(real, "/usb") {
			continue
		}
		dev := "/dev/" + name
		// The partitions of the disk are what carries the files, the disk
		// itself has no filesystem — so only the partitions are listed.
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, p := range entries {
			pname := p.Name()
			if !strings.HasPrefix(pname, name) {
				continue
			}
			// "partition" is the marker the kernel uses for partitions.
			if _, err := os.Stat(filepath.Join(dir, pname, "partition")); err != nil {
				continue
			}
			pdev := "/dev/" + pname
			if mounted[pdev] {
				continue
			}
			out = append(out, Device{
				Path:   pdev,
				Label:  labels[pdev],
				Size:   partSize(dir, pname),
				Parent: dev,
			})
		}
	}
	return out
}

// Mount mounts a partition through udisks, which asks the desktop for
// permission. It returns the path the device was mounted at.
func Mount(path string) (string, error) {
	if !devicePath.MatchString(path) {
		return "", errors.New("that is not a device")
	}
	bin, err := exec.LookPath("udisksctl")
	if err != nil {
		return "", errors.New("udisksctl is not installed, so drives cannot be mounted from here")
	}
	out, err := exec.Command(bin, "mount", "-b", path).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	// "Mounted /dev/sda1 at /run/media/user/USB DISK"
	text := string(out)
	if i := strings.Index(text, " at "); i >= 0 {
		return strings.TrimSpace(text[i+4:]), nil
	}
	return "", nil
}

// mountedDevices reads the devices that are in use right now.
func mountedDevices() map[string]bool {
	out := map[string]bool{}
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return out
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		out[parts[0]] = true
	}
	return out
}

// diskLabels maps /dev/sda1 to the label of that partition, using the
// symlinks udisks keeps in /dev/disk/by-label.
func diskLabels() map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir("/dev/disk/by-label")
	if err != nil {
		return out
	}
	for _, e := range entries {
		target, err := filepath.EvalSymlinks(filepath.Join("/dev/disk/by-label", e.Name()))
		if err != nil {
			continue
		}
		out[target] = unescapeLabel(e.Name())
	}
	return out
}

// unescapeLabel undoes the escaping udisks uses for the symlink names: a
// label with a space becomes "USB\x20DISK".
func unescapeLabel(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if name[i] == '\\' && i+3 < len(name) && name[i+1] == 'x' {
			if v, err := strconv.ParseUint(name[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(name[i])
	}
	return b.String()
}

// partSize reads the size of a disk or partition from sysfs, in bytes.
func partSize(dir, name string) uint64 {
	data, err := os.ReadFile(filepath.Join(dir, name, "size"))
	if err != nil {
		return 0
	}
	var sectors uint64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &sectors); err != nil {
		return 0
	}
	return sectors * 512
}

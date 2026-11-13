package usbsetup

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"howett.net/plist"
)

type Drive struct {
	Serial      string   `json:"-"`
	ID          string   `json:"id"`
	Device      string   `json:"device"`
	Name        string   `json:"name"`
	Size        uint64   `json:"size"`
	Mount       string   `json:"mount"`
	Partition   string   `json:"partition"`
	Filesystem  string   `json:"filesystem"`
	Eligible    bool     `json:"eligible"`
	Reason      string   `json:"reason"`
	Fingerprint string   `json:"-"`
	ReadOnly    bool     `json:"readOnly"`
	Mounts      []string `json:"-"`
}
type RunFunc func(context.Context, string, []string, string) ([]byte, error)

func Run(ctx context.Context, name string, args []string, input string) ([]byte, error) {
	c := exec.CommandContext(ctx, name, args...)
	if input != "" {
		c.Stdin = strings.NewReader(input)
	}
	out, err := c.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w: %.2048s", filepath.Base(name), err, out)
	}
	return out, nil
}
func identify(d *Drive, serial string) {
	d.Serial = serial
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", d.Device, serial, d.Size)))
	d.ID = fmt.Sprintf("%x", sum[:12])
	d.Fingerprint = fmt.Sprintf("%x", sum[:])
}
func checkDrive(d *Drive, mbr, fat, first bool) {
	switch {
	case d.ReadOnly:
		d.Reason = "Drive is read-only"
	case d.Size < 64<<20 || d.Size > 2<<40:
		d.Reason = "Use a USB between 64 MiB and 2 TiB"
	case !mbr:
		d.Reason = "Needs an MBR partition table"
	case !first:
		d.Reason = "Needs a first partition"
	case !fat:
		d.Reason = "Needs FAT32"
	case d.Mount == "":
		d.Reason = "Mount the first FAT32 partition, then refresh"
	default:
		d.Eligible = true
	}
}
func Discover(ctx context.Context) ([]Drive, error) { return discover(ctx, runtime.GOOS, Run) }
func discover(ctx context.Context, platform string, run RunFunc) ([]Drive, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch platform {
	case "linux":
		out, e := run(ctx, "lsblk", []string{"--json", "--bytes", "--paths", "-o", "NAME,TYPE,TRAN,RM,RO,FSTYPE,FSVER,PTTYPE,PARTN,MOUNTPOINTS,UUID,LABEL,SIZE,SERIAL,LOG-SEC"}, "")
		if e != nil {
			return nil, e
		}
		return linuxDrives(out)
	case "windows":
		out, e := run(ctx, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", windowsInventory}, "")
		if e != nil {
			return nil, e
		}
		return windowsDrives(out)
	case "darwin":
		return darwinDrives(ctx, run)
	default:
		return nil, fmt.Errorf("USB discovery is unsupported on %s", platform)
	}
}

type block struct {
	Name, Type, Tran, FSType, FSVer, PTType, UUID, Label, Serial string
	RM, RO                                                       bool
	Size                                                         uint64
	PartN                                                        *int      `json:"partn"`
	Sector                                                       int       `json:"log-sec"`
	Mounts                                                       []*string `json:"mountpoints"`
	Children                                                     []block
}

func linuxDrives(data []byte) ([]Drive, error) {
	var listing struct {
		Devices []block `json:"blockdevices"`
	}
	if e := json.Unmarshal(data, &listing); e != nil {
		return nil, e
	}
	out := []Drive{}
	for _, b := range listing.Devices {
		if b.Type != "disk" || b.Tran != "usb" || !b.RM {
			continue
		}
		d := Drive{Device: b.Name, Name: b.Label, Size: b.Size, ReadOnly: b.RO}
		if d.Name == "" {
			d.Name = filepath.Base(b.Name)
		}
		var collect func(block)
		collect = func(n block) {
			for _, m := range n.Mounts {
				if m != nil && *m != "" {
					d.Mounts = append(d.Mounts, *m)
				}
			}
			for _, c := range n.Children {
				collect(c)
			}
		}
		collect(b)
		protected := false
		for _, m := range d.Mounts {
			if protectedMount(m) {
				protected = true
			}
		}
		if protected {
			continue
		}
		first, fat := false, false
		for _, c := range b.Children {
			if c.Type == "part" && c.PartN != nil && *c.PartN == 1 {
				first = true
				d.Partition = c.Name
				d.Filesystem = c.FSType
				fat = c.FSType == "vfat" && c.FSVer == "FAT32"
				d.ReadOnly = d.ReadOnly || c.RO
				for _, m := range c.Mounts {
					if m != nil && *m != "" {
						d.Mount = *m
						break
					}
				}
				if c.Label != "" {
					d.Name = c.Label
				}
			}
		}
		checkDrive(&d, b.PTType == "dos", fat, first)
		if b.Sector != 512 {
			d.ReadOnly = true
			d.Eligible = false
			d.Reason = "Only 512-byte sector USB devices are supported"
		}
		identify(&d, b.Serial)
		out = append(out, d)
	}
	return out, nil
}

const windowsInventory = `$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new(); $result=@(); Get-Disk | Where-Object {$_.BusType -eq 'USB' -and -not $_.IsBoot -and -not $_.IsSystem} | ForEach-Object { $d=$_; $parts=@(); Get-Partition -DiskNumber $d.Number -ErrorAction SilentlyContinue | ForEach-Object { $p=$_; $v=$p | Get-Volume -ErrorAction SilentlyContinue; $parts+=@{number=$p.PartitionNumber; letter=[string]$p.DriveLetter; fs=[string]$v.FileSystem; label=[string]$v.FileSystemLabel; readonly=[bool]$p.IsReadOnly; system=[bool]($p.IsBoot -or $p.IsSystem)} }; $result+=@{device=[string]$d.Number; name=$d.FriendlyName; serial=[string]$d.SerialNumber; size=$d.Size; style=[string]$d.PartitionStyle; readonly=[bool]$d.IsReadOnly; sector=$d.LogicalSectorSize; parts=$parts} }; ConvertTo-Json -InputObject @($result) -Depth 5 -Compress`

type winDisk struct {
	Device, Name, Serial, Style string
	Size                        uint64
	ReadOnly                    bool
	Sector                      int
	Parts                       []struct {
		Number            int
		Letter, FS, Label string
		ReadOnly, System  bool
	}
}

func windowsDrives(data []byte) ([]Drive, error) {
	var disks []winDisk
	if e := json.Unmarshal([]byte(strings.TrimPrefix(string(data), "\ufeff")), &disks); e != nil {
		return nil, e
	}
	out := []Drive{}
	for _, b := range disks {
		d := Drive{Device: b.Device, Name: b.Name, Size: b.Size, ReadOnly: b.ReadOnly}
		first, fat, protected := false, false, false
		for _, p := range b.Parts {
			if p.System {
				protected = true
			}
			if p.Letter != "" {
				d.Mounts = append(d.Mounts, p.Letter+`:\`)
			}
			if p.Number == 1 {
				first = true
				d.Partition = b.Device + ":1"
				d.Filesystem = p.FS
				fat = p.FS == "FAT32"
				d.ReadOnly = d.ReadOnly || p.ReadOnly
				if p.Letter != "" {
					d.Mount = p.Letter + `:\`
				}
				if p.Label != "" {
					d.Name = p.Label
				}
			}
		}
		if protected {
			continue
		}
		if _, e := strconv.ParseUint(b.Device, 10, 32); e != nil {
			return nil, e
		}
		checkDrive(&d, b.Style == "MBR", fat, first)
		if b.Sector != 512 {
			d.ReadOnly = true
			d.Eligible = false
			d.Reason = "Only 512-byte sector USB devices are supported"
		}
		identify(&d, b.Serial)
		out = append(out, d)
	}
	return out, nil
}

func protectedMount(m string) bool {
	if m == "/" || m == "[SWAP]" {
		return true
	}
	for _, prefix := range []string{"/boot", "/home", "/root", "/usr", "/etc", "/var", "/opt", "/bin", "/sbin", "/lib"} {
		if m == prefix || strings.HasPrefix(m, prefix+"/") {
			return true
		}
	}
	return false
}

type macDisk struct {
	DeviceIdentifier, Content string
	Size                      uint64
	Partitions                []struct {
		DeviceIdentifier, Content, MountPoint, VolumeName string
		Size                                              uint64
	}
}
type macInfo struct {
	Internal, Writable, RemovableMedia                 bool
	BusProtocol, DeviceTreePath, MediaName, VolumeUUID string
	DeviceBlockSize                                    int
}

func darwinDrives(ctx context.Context, run RunFunc) ([]Drive, error) {
	data, e := run(ctx, "/usr/sbin/diskutil", []string{"list", "-plist", "external", "physical"}, "")
	if e != nil {
		return nil, e
	}
	var list struct{ AllDisksAndPartitions []macDisk }
	if _, e = plist.Unmarshal(data, &list); e != nil {
		return nil, e
	}
	boot, e := darwinBootDisks(ctx, run)
	if e != nil {
		return nil, e
	}
	out := []Drive{}
	for _, b := range list.AllDisksAndPartitions {
		if boot[b.DeviceIdentifier] {
			continue
		}
		data, e = run(ctx, "/usr/sbin/diskutil", []string{"info", "-plist", b.DeviceIdentifier}, "")
		if e != nil {
			return nil, e
		}
		var info macInfo
		if _, e = plist.Unmarshal(data, &info); e != nil {
			return nil, e
		}
		if info.Internal || info.BusProtocol != "USB" || !info.RemovableMedia {
			continue
		}
		d := Drive{Device: "/dev/" + b.DeviceIdentifier, Size: b.Size, Name: info.MediaName, ReadOnly: !info.Writable}
		first, fat := false, false
		for _, p := range b.Partitions {
			if p.MountPoint != "" {
				d.Mounts = append(d.Mounts, p.MountPoint)
			}
			if p.DeviceIdentifier == b.DeviceIdentifier+"s1" {
				first = true
				d.Partition = "/dev/" + p.DeviceIdentifier
				d.Mount = p.MountPoint
				d.Filesystem = p.Content
				fat = p.Content == "Windows_FAT_32" || p.Content == "DOS_FAT_32"
				if p.VolumeName != "" {
					d.Name = p.VolumeName
				}
			}
		}
		checkDrive(&d, b.Content == "FDisk_partition_scheme", fat, first)
		if info.DeviceBlockSize != 512 {
			d.ReadOnly = true
			d.Eligible = false
			d.Reason = "Only 512-byte sector USB devices are supported"
		}
		identify(&d, info.DeviceTreePath)
		out = append(out, d)
	}
	return out, nil
}

func findDrive(ctx context.Context, id string) (Drive, error) {
	drives, e := Discover(ctx)
	if e != nil {
		return Drive{}, e
	}
	for _, d := range drives {
		if d.ID == id {
			return d, nil
		}
	}
	return Drive{}, fmt.Errorf("USB changed or was removed; refresh the drive list")
}
func openDrive(d Drive) (*os.Root, error) {
	if !d.Eligible || d.Mount == "" {
		return nil, fmt.Errorf("USB is not ready: %s", d.Reason)
	}
	info, e := os.Lstat(d.Mount)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("USB mount is not a real directory")
	}
	return os.OpenRoot(d.Mount)
}

// APFS boot volumes may live on a synthesized disk. Resolve their physical stores.
func darwinBootDisks(ctx context.Context, run RunFunc) (map[string]bool, error) {
	data, e := run(ctx, "/usr/sbin/diskutil", []string{"info", "-plist", "/"}, "")
	if e != nil {
		return nil, e
	}
	var root struct{ ParentWholeDisk, APFSContainerReference string }
	if _, e = plist.Unmarshal(data, &root); e != nil {
		return nil, e
	}
	boot := map[string]bool{}
	if root.ParentWholeDisk == "" {
		return nil, fmt.Errorf("cannot identify the macOS system disk")
	}
	boot[root.ParentWholeDisk] = true
	if root.APFSContainerReference != "" {
		data, e = run(ctx, "/usr/sbin/diskutil", []string{"apfs", "list", "-plist"}, "")
		if e != nil {
			return nil, e
		}
		var apfs struct {
			Containers []struct {
				ContainerReference string
				PhysicalStores     []struct{ DeviceIdentifier string }
			}
		}
		if _, e = plist.Unmarshal(data, &apfs); e != nil {
			return nil, e
		}
		found := false
		for _, c := range apfs.Containers {
			if c.ContainerReference == root.APFSContainerReference {
				for _, store := range c.PhysicalStores {
					disk := regexp.MustCompile(`^disk[0-9]+`).FindString(store.DeviceIdentifier)
					if disk != "" {
						boot[disk] = true
						found = true
					}
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("cannot resolve the macOS boot disk's physical stores")
		}
	}
	return boot, nil
}

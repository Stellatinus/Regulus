package agent

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
)

// diskIdentityVersion is the version of the physical-disk part of a report
// (MetricsReport.disk_identity_version, #148): 1 is the fields of
// PhysicalDisk. It moves only when a field changes meaning.
const diskIdentityVersion = 1

// Where the identifiers of a drive are. Both are read without privileges: the
// files of /sys/block are world readable, and udev's database is too.
const (
	sysBlockDir = "/sys/block"
	udevDataDir = "/run/udev/data"
)

// skipDisk lists the block devices that are not a physical drive: loop
// devices, RAM and compressed-RAM disks, device-mapper and software RAID
// volumes (their members are reported on their own), optical drives, floppy,
// network block devices.
var skipDisk = []string{"loop", "ram", "zram", "dm-", "md", "sr", "fd", "nbd", "drbd", "rbd", "zd"}

// readPhysicalDisks lists the physical disks under sysBlock, with what each
// reports about itself, sorted by device name. udevData may be empty. It
// returns nil when sysBlock cannot be read. It never guesses: an identifier a
// drive does not report stays empty, and deciding which identifier is the
// identity is the hub's job (internal/diskid).
func readPhysicalDisks(sysBlock, udevData string) []*agentv1.PhysicalDisk {
	entries, err := os.ReadDir(sysBlock)
	if err != nil {
		return nil
	}
	var disks []*agentv1.PhysicalDisk
	for _, e := range entries {
		name := e.Name()
		if skipped(name) {
			continue
		}
		dir := filepath.Join(sysBlock, name)
		sectors, _ := strconv.ParseUint(readTrim(filepath.Join(dir, "size")), 10, 64)
		if sectors == 0 {
			continue // no medium: an empty card reader
		}
		d := &agentv1.PhysicalDisk{Device: name, SizeBytes: sectors * 512}
		udev := readUdev(udevData, readTrim(filepath.Join(dir, "dev")))

		d.Vendor = firstNonEmpty(readTrim(filepath.Join(dir, "device", "vendor")), udev["ID_VENDOR"])
		d.Model = firstNonEmpty(readTrim(filepath.Join(dir, "device", "model")), udev["ID_MODEL"])
		d.Serial = firstNonEmpty(readTrim(filepath.Join(dir, "device", "serial")), vpdSerial(filepath.Join(dir, "device", "vpd_pg80")), udev["ID_SERIAL_SHORT"])
		d.Transport, d.Virtual = transportOf(dir, udev["ID_BUS"], d.Vendor, d.Model)
		d.Wwn, d.WwnKind = worldWideNameOf(dir, udev)
		disks = append(disks, d)
	}
	sort.Slice(disks, func(i, j int) bool { return disks[i].Device < disks[j].Device })
	return disks
}

func skipped(name string) bool {
	for _, p := range skipDisk {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func readTrim(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// readUdev returns the E: properties udev holds for the block device major:minor
// (/run/udev/data/b8:0), or nil.
func readUdev(dir, majMin string) map[string]string {
	if dir == "" || majMin == "" {
		return nil
	}
	f, err := os.Open(filepath.Join(dir, "b"+majMin))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	props := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if kv, ok := strings.CutPrefix(sc.Text(), "E:"); ok {
			if k, v, ok := strings.Cut(kv, "="); ok {
				props[k] = v
			}
		}
	}
	return props
}

// vpdSerial reads the unit serial number from a SCSI VPD page 0x80 (a
// binary file: device type, page code 0x80, a 16-bit length, then the serial
// in ASCII, padded with spaces).
func vpdSerial(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 4 || data[1] != 0x80 {
		return ""
	}
	n := int(data[2])<<8 | int(data[3])
	if n > len(data)-4 {
		n = len(data) - 4
	}
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return -1
		}
		return r
	}, string(data[4:4+n])))
}

// transportOf says how the disk is attached, from the device's path in sysfs
// (which names the bus between the machine and the drive) or, failing that,
// from udev. virtual is true for a disk a hypervisor presents.
func transportOf(dir, udevBus, vendor, model string) (transport string, virtual bool) {
	path, err := filepath.EvalSymlinks(dir)
	if err != nil {
		path = dir
	}
	switch {
	case strings.Contains(path, "/usb"):
		transport = "usb"
	case strings.Contains(path, "/nvme"):
		transport = "nvme"
	case strings.Contains(path, "/virtio"):
		transport, virtual = "virtio", true
	case strings.Contains(path, "/end_device-") || strings.Contains(path, "/port-"):
		transport = "sas"
	case strings.Contains(path, "/ata"):
		transport = "sata"
	case udevBus == "ata":
		transport = "sata"
	case udevBus == "usb" || udevBus == "scsi" || udevBus == "nvme":
		transport = udevBus
	}
	text := strings.ToLower(vendor + " " + model)
	for _, h := range []string{"qemu", "vmware", "vbox", "virtualbox", "msft", "xen ", "virtual disk", "virtual hd"} {
		if strings.Contains(text, h) {
			virtual = true
		}
	}
	return transport, virtual
}

// worldWideNameOf finds the WWN, EUI-64 or NGUID of the drive, as lowercase
// hexadecimal, and says which it is. The kernel's own file comes first, udev's
// copy second. A value of all zeros is not an identifier (many NVMe drives
// report a zero NGUID) and is passed over for the next one.
func worldWideNameOf(dir string, udev map[string]string) (hex, kind string) {
	type candidate struct{ value, kind string }
	var list []candidate
	for _, f := range []string{filepath.Join(dir, "wwid"), filepath.Join(dir, "device", "wwid")} {
		w := readTrim(f)
		switch {
		case strings.HasPrefix(w, "eui."):
			list = append(list, candidate{strings.TrimPrefix(w, "eui."), "eui64"})
		case strings.HasPrefix(w, "naa."):
			list = append(list, candidate{strings.TrimPrefix(w, "naa."), "wwn"})
		}
	}
	list = append(list,
		candidate{readTrim(filepath.Join(dir, "eui")), "eui64"},
		candidate{readTrim(filepath.Join(dir, "nguid")), "nguid"},
	)
	if w := udev["ID_WWN"]; w != "" {
		kind := "wwn"
		if strings.HasPrefix(w, "eui.") {
			kind = "eui64"
		}
		list = append(list, candidate{strings.TrimPrefix(strings.TrimPrefix(w, "eui."), "0x"), kind})
	}
	for _, c := range list {
		if v := compactHex(c.value); v != "" {
			return v, c.kind
		}
	}
	return "", ""
}

// compactHex lowercases a hexadecimal identifier and removes separators. It
// returns "" for an empty value, a value that is not hexadecimal, and zeros.
func compactHex(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '-' || r == ':' || r == ' ':
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			b.WriteRune(r)
		default:
			return ""
		}
	}
	if strings.Trim(b.String(), "0") == "" {
		return ""
	}
	return b.String()
}

package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeSys builds a /sys/block with the kinds of disk the code has to read.
func fakeSys(t *testing.T) (sys, udev string) {
	t.Helper()
	root := t.TempDir()
	sys, udev = filepath.Join(root, "sys", "block"), filepath.Join(root, "udev")
	// The real tree: /sys/block/<name> is a link to the device under /sys/devices.
	dev := func(name, path string) string {
		real := filepath.Join(root, "sys", "devices", path, "block", name)
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(sys, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(sys, name)); err != nil {
			t.Fatal(err)
		}
		return real
	}

	// A SATA disk: WWN from the SCSI wwid, serial from the VPD page.
	sda := dev("sda", "pci0000:00/0000:00:1f.2/ata1/host0/target0:0:0/0:0:0:0")
	writeFile(t, filepath.Join(sda, "size"), "7814037168\n")
	writeFile(t, filepath.Join(sda, "dev"), "8:0\n")
	writeFile(t, filepath.Join(sda, "device", "vendor"), "ATA     \n")
	writeFile(t, filepath.Join(sda, "device", "model"), "ST4000VN008-2DR1\n")
	writeFile(t, filepath.Join(sda, "device", "wwid"), "naa.5000c500a1b2c3d4\n")
	writeFile(t, filepath.Join(sda, "device", "vpd_pg80"), "\x00\x80\x00\x08ZA1ABC23")

	// An NVMe disk: a zero NGUID is passed over for the EUI-64.
	nvme := dev("nvme0n1", "pci0000:00/0000:00:1d.0/nvme/nvme0")
	writeFile(t, filepath.Join(nvme, "size"), "1953525168\n")
	writeFile(t, filepath.Join(nvme, "nguid"), "00000000000000000000000000000000\n")
	writeFile(t, filepath.Join(nvme, "eui"), "00 25 38 5b 71 b0 c7 e3\n")
	writeFile(t, filepath.Join(nvme, "device", "model"), "Samsung SSD 980 PRO 1TB\n")
	writeFile(t, filepath.Join(nvme, "device", "serial"), "S5GXNF0R123456A\n")

	// A virtio disk of a VM: virtual, serial from udev, no WWN.
	vda := dev("vda", "pci0000:00/0000:00:04.0/virtio1")
	writeFile(t, filepath.Join(vda, "size"), "41943040\n")
	writeFile(t, filepath.Join(vda, "dev"), "252:0\n")
	writeFile(t, filepath.Join(udev, "b252:0"), "S:disk/by-id/virtio-abc\nE:ID_SERIAL_SHORT=abc\nE:ID_MODEL=ignored\n")

	// A USB disk whose serial and WWN come from udev.
	sdb := dev("sdb", "pci0000:00/0000:00:14.0/usb2/2-1/2-1:1.0/host6/target6:0:0/6:0:0:0")
	writeFile(t, filepath.Join(sdb, "size"), "976773168\n")
	writeFile(t, filepath.Join(sdb, "dev"), "8:16\n")
	writeFile(t, filepath.Join(sdb, "device", "model"), "Portable SSD\n")
	writeFile(t, filepath.Join(udev, "b8:16"), "E:ID_SERIAL_SHORT=2023ABC\nE:ID_WWN=0x5000000000000001\nE:ID_BUS=usb\n")

	// Not physical, or no medium.
	for _, name := range []string{"loop0", "dm-0", "md127", "sr0", "ram0", "zram0"} {
		d := dev(name, "virtual/block")
		writeFile(t, filepath.Join(d, "size"), "1000000\n")
	}
	empty := dev("sdc", "pci0000:00/usb3/3-1/host7/target7:0:0/7:0:0:0")
	writeFile(t, filepath.Join(empty, "size"), "0\n")
	return sys, udev
}

func TestReadPhysicalDisks(t *testing.T) {
	sys, udev := fakeSys(t)
	disks := readPhysicalDisks(sys, udev)
	byName := map[string]int{}
	for i, d := range disks {
		byName[d.Device] = i
	}
	if len(disks) != 4 {
		t.Fatalf("got %d disks, want sda, sdb, nvme0n1, vda: %v", len(disks), disks)
	}
	for _, name := range []string{"loop0", "dm-0", "md127", "sr0", "ram0", "zram0", "sdc"} {
		if _, ok := byName[name]; ok {
			t.Errorf("%s is not a physical disk with a medium and was reported", name)
		}
	}

	sda := disks[byName["sda"]]
	if sda.Vendor != "ATA" || sda.Model != "ST4000VN008-2DR1" || sda.Serial != "ZA1ABC23" || sda.Wwn != "5000c500a1b2c3d4" || sda.WwnKind != "wwn" || sda.Transport != "sata" || sda.Virtual || sda.SizeBytes != 7814037168*512 {
		t.Errorf("sda = %v", sda)
	}
	nvme := disks[byName["nvme0n1"]]
	if nvme.Wwn != "0025385b71b0c7e3" || nvme.WwnKind != "eui64" || nvme.Serial != "S5GXNF0R123456A" || nvme.Transport != "nvme" {
		t.Errorf("nvme0n1 = %v", nvme)
	}
	vda := disks[byName["vda"]]
	if !vda.Virtual || vda.Transport != "virtio" || vda.Serial != "abc" || vda.Wwn != "" || vda.WwnKind != "" {
		t.Errorf("vda = %v", vda)
	}
	sdb := disks[byName["sdb"]]
	if sdb.Transport != "usb" || sdb.Serial != "2023ABC" || sdb.Wwn != "5000000000000001" || sdb.WwnKind != "wwn" {
		t.Errorf("sdb = %v", sdb)
	}
	for i := 1; i < len(disks); i++ {
		if disks[i-1].Device > disks[i].Device {
			t.Error("disks are not sorted by device")
		}
	}
}

func TestReadPhysicalDisksWithoutSysfs(t *testing.T) {
	if got := readPhysicalDisks(filepath.Join(t.TempDir(), "missing"), ""); got != nil {
		t.Errorf("got %v for a missing /sys/block", got)
	}
}

func TestCompactHex(t *testing.T) {
	for in, want := range map[string]string{
		"0x5000C500A1B2C3D4":      "5000c500a1b2c3d4",
		"00 25 38 5b":             "0025385b",
		"00:25:38:5b":             "0025385b",
		"0000000000000000":        "",
		"":                        "",
		"vol0123456789abcdef0123": "",
	} {
		if got := compactHex(in); got != want {
			t.Errorf("compactHex(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVPDSerialIgnoresOtherPagesAndShortFiles(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"short": "\x00\x80", "other": "\x00\x83\x00\x04ABCD", "long": "\x00\x80\xff\xffAB"} {
		writeFile(t, filepath.Join(dir, name), content)
	}
	if got := vpdSerial(filepath.Join(dir, "short")); got != "" {
		t.Errorf("short = %q", got)
	}
	if got := vpdSerial(filepath.Join(dir, "other")); got != "" {
		t.Errorf("other page = %q", got)
	}
	if got := vpdSerial(filepath.Join(dir, "long")); got != "AB" {
		t.Errorf("a length past the end = %q, want AB", got)
	}
}

// The collector puts the disks in the report only when it was given a sysfs.
func TestCollectorReportsPhysicalDisks(t *testing.T) {
	sys, udev := fakeSys(t)
	proc := t.TempDir()
	writeProc(t, proc, map[string]string{"stat": statSample, "meminfo": meminfoSample})
	c := newLinuxCollector(proc, func(string) (uint64, uint64, error) { return 0, 0, nil })
	rep, err := c.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.DiskIdentityVersion != 0 || len(rep.PhysicalDisks) != 0 {
		t.Fatalf("a collector without sysfs reported disks: %v", rep.PhysicalDisks)
	}
	c.sysBlock, c.udevData = sys, udev
	rep, err = c.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.DiskIdentityVersion != diskIdentityVersion || len(rep.PhysicalDisks) != 4 {
		t.Fatalf("version %d, %d disks", rep.DiskIdentityVersion, len(rep.PhysicalDisks))
	}
}

package diskid

import "testing"

func TestKeyPreferenceAndNormalization(t *testing.T) {
	for name, tc := range map[string]struct {
		d    Disk
		kind Kind
		key  string
	}{
		"wwn wins over serial": {Disk{Model: "ST4000", Serial: "ZA1", WWN: "0x5000C500A1B2C3D4", WWNKind: "wwn"}, KindWWN, "wwn:5000c500a1b2c3d4"},
		"naa prefix":           {Disk{WWN: "naa.5000c500a1b2c3d4"}, KindWWN, "wwn:5000c500a1b2c3d4"},
		"nvme eui64":           {Disk{WWN: "eui.0025385b71b0c7e3", WWNKind: "eui64"}, KindEUI64, "eui64:0025385b71b0c7e3"},
		"nvme nguid":           {Disk{WWN: "0025385b71b0c7e30025385b71b0c7e3", WWNKind: "nguid"}, KindNGUID, "nguid:0025385b71b0c7e30025385b71b0c7e3"},
		"separators":           {Disk{WWN: "50:00:c5:00:a1:b2:c3:d4"}, KindWWN, "wwn:5000c500a1b2c3d4"},
		"model and serial":     {Disk{Model: "  Samsung  SSD 870 ", Serial: "S5Y1NX0R123456A"}, KindModelSerial, "model-serial:samsung ssd 870|s5y1nx0r123456a"},
		"same on every case":   {Disk{Model: "SAMSUNG SSD 870", Serial: "s5y1nx0r123456a"}, KindModelSerial, "model-serial:samsung ssd 870|s5y1nx0r123456a"},
	} {
		t.Run(name, func(t *testing.T) {
			got := identifyOne(tc.d)
			if got.Kind != tc.kind || got.Key != tc.key || !got.Identifiable() {
				t.Fatalf("got %+v, want %s %q", got, tc.kind, tc.key)
			}
		})
	}
}

// The device name is not an input: the same drive under another name has the
// same key.
func TestTheDeviceNameIsNotPartOfTheKey(t *testing.T) {
	a := identifyOne(Disk{Device: "sda", Model: "M", Serial: "S1"})
	b := identifyOne(Disk{Device: "sdq", Model: "M", Serial: "S1"})
	if a.Key == "" || a.Key != b.Key {
		t.Fatalf("keys %q and %q differ", a.Key, b.Key)
	}
}

func TestNotIdentifiable(t *testing.T) {
	for name, tc := range map[string]struct {
		d      Disk
		reason string
	}{
		"nothing":             {Disk{Device: "sda"}, "no-identifier"},
		"no serial":           {Disk{Model: "M"}, "no-identifier"},
		"no model":            {Disk{Serial: "S"}, "no-identifier"},
		"virtual disk":        {Disk{Model: "QEMU HARDDISK", Serial: "QM00001", Virtual: true}, "virtual"},
		"usb bridge":          {Disk{Model: "External", Serial: "20230101", Transport: "usb"}, "usb-bridge"},
		"zero serial":         {Disk{Model: "M", Serial: "0"}, "placeholder-serial"},
		"repeated characters": {Disk{Model: "M", Serial: "000000000000"}, "placeholder-serial"},
		"filler text":         {Disk{Model: "M", Serial: "To Be Filled By O.E.M."}, "placeholder-serial"},
		"zero wwn":            {Disk{WWN: "0x0000000000000000"}, "no-identifier"},
		"short wwn":           {Disk{WWN: "5000c5"}, "no-identifier"},
		"not hex":             {Disk{WWN: "serial-number-here"}, "no-identifier"},
		"eui64 of wrong size": {Disk{WWN: "5000c500a1b2c3d4c500a1b2", WWNKind: "eui64"}, "no-identifier"},
	} {
		t.Run(name, func(t *testing.T) {
			got := identifyOne(tc.d)
			if got.Identifiable() || got.Kind != KindNone || got.Reason != tc.reason {
				t.Fatalf("got %+v, want not identifiable (%s)", got, tc.reason)
			}
		})
	}
}

// A virtual or USB disk with a real WWN is identifiable: the WWN is the
// drive's, whatever carries it.
func TestAWWNIsTrustedOnAnyTransport(t *testing.T) {
	for _, d := range []Disk{
		{WWN: "5000c500a1b2c3d4", Virtual: true},
		{WWN: "5000c500a1b2c3d4", Transport: "usb"},
	} {
		if !identifyOne(d).Identifiable() {
			t.Errorf("%+v is not identifiable", d)
		}
	}
}

func TestDuplicatesAreFlaggedAndNotIdentified(t *testing.T) {
	got := Identify([]Disk{
		{Device: "sda", Model: "M", Serial: "SAME"},
		{Device: "sdb", Model: "M", Serial: "SAME"},
		{Device: "sdc", Model: "M", Serial: "OTHER"},
		{Device: "sdd", WWN: "5000c500a1b2c3d4"},
		{Device: "sde", WWN: "5000c500a1b2c3d4"},
	})
	for i, wantConflict := range []bool{true, true, false, true, true} {
		if got[i].Conflict != wantConflict {
			t.Errorf("disk %d: conflict = %v, want %v", i, got[i].Conflict, wantConflict)
		}
		if wantConflict && (got[i].Identifiable() || got[i].Reason != "duplicate") {
			t.Errorf("disk %d in conflict has %+v", i, got[i])
		}
	}
	if !got[2].Identifiable() {
		t.Error("the disk without a duplicate lost its identity")
	}
}

func FuzzIdentify(f *testing.F) {
	f.Add("sda", "ATA", "Samsung SSD", "S1", "0x5000c500a1b2c3d4", "wwn", "sata")
	f.Add("", "", "", "", "", "", "")
	f.Fuzz(func(t *testing.T, dev, vendor, model, serial, wwn, kind, transport string) {
		d := Disk{Device: dev, Vendor: vendor, Model: model, Serial: serial, WWN: wwn, WWNKind: kind, Transport: transport}
		got := Identify([]Disk{d, d})
		if got[0].Key != "" || got[1].Key != "" {
			t.Fatalf("two identical disks kept a key: %+v", got)
		}
		one := Identify([]Disk{d})[0]
		if one.Identifiable() == (one.Reason != "") {
			t.Fatalf("identifiable %v with reason %q", one.Identifiable(), one.Reason)
		}
	})
}

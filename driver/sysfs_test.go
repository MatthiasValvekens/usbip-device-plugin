package driver

import (
	"testing"
	"testing/fstest"

	"github.com/efficientgo/core/errors"
)

const (
	statusHeader = "hub port sta spd dev      sockfd local_busid\n"
)

func compareSlots(t *testing.T, driver VHCIDriver, expectedSlots map[int]VHCISlot) {
	slots := driver.GetDeviceSlots()
	for i, slot := range expectedSlots {
		if slots[i] != slot {
			t.Errorf("port %d: got %v; want %v", i, slots[i], slot)
		}
	}

	for i, slot := range slots {
		_, isExpected := expectedSlots[i]
		if !slot.IsEmpty() && !isExpected {
			t.Errorf("port %d: status is %d, expected null", i, slot.Status)
		}
	}
}

func TestSlotEnumeration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fs    fstest.MapFS
		slots map[int]VHCISlot
		err   error
	}{
		{
			name: "sysfs unreadable",
			fs:   fstest.MapFS{},
			err:  errors.New("failed to read nports attribute"),
		},
		{
			name: "detect",
			fs: fstest.MapFS{
				"bus/platform/devices/vhci_hcd.0/nports": {Data: []byte("4\n")},
				"bus/platform/devices/vhci_hcd.0/status": {Data: []byte(
					statusHeader +
						"hs  0000 006 002 00010002 000010 2-1\n" +
						"hs  0001 004 000 00000000 000000 0-0\n" +
						"hs  0002 004 000 00000000 000000 0-0\n" +
						"ss  0003 006 002 00080002 000011 2-2\n",
				)},
				"bus/usb/devices/2-1/idVendor":  {Data: []byte("dead\n")},
				"bus/usb/devices/2-1/idProduct": {Data: []byte("beef\n")},
				"bus/usb/devices/2-1/busnum":    {Data: []byte("02\n")},
				"bus/usb/devices/2-1/devnum":    {Data: []byte("33\n")},
				"bus/usb/devices/2-2/idVendor":  {Data: []byte("dead\n")},
				"bus/usb/devices/2-2/idProduct": {Data: []byte("beef\n")},
				"bus/usb/devices/2-2/busnum":    {Data: []byte("02\n")},
				"bus/usb/devices/2-2/devnum":    {Data: []byte("34\n")},
			},
			slots: map[int]VHCISlot{
				0: {
					HubSpeed:        HubSpeedHigh,
					Port:            VirtualPort(0),
					Status:          VDevStatusUsed,
					DeviceID:        0x00010002,
					SysPath:         "bus/usb/devices/2-1",
					DevMountPath:    "/dev/bus/usb/002/033",
					LocalDeviceInfo: USBDevice{USBID(0xdead), USBID(0xbeef), "2-1"},
				},
				3: {
					HubSpeed:        HubSpeedSuper,
					Port:            VirtualPort(3),
					Status:          VDevStatusUsed,
					DeviceID:        0x00080002,
					SysPath:         "bus/usb/devices/2-2",
					DevMountPath:    "/dev/bus/usb/002/034",
					LocalDeviceInfo: USBDevice{USBID(0xdead), USBID(0xbeef), "2-2"},
				},
			},
		},
		{
			name: "handle partially missing data",
			fs: fstest.MapFS{
				"bus/platform/devices/vhci_hcd.0/nports": {Data: []byte("4\n")},
				"bus/platform/devices/vhci_hcd.0/status": {Data: []byte(
					statusHeader +
						"hs  0000 006 002 00010002 000010 2-1\n" +
						"hs  0001 004 000 00000000 000000 0-0\n" +
						"hs  0002 004 000 00000000 000000 0-0\n" +
						"ss  0003 006 002 00080002 000011 2-2\n",
				)},
				"bus/usb/devices/2-1/idVendor":  {Data: []byte("dead\n")},
				"bus/usb/devices/2-1/idProduct": {Data: []byte("beef\n")},
				"bus/usb/devices/2-1/busnum":    {Data: []byte("02\n")},
				"bus/usb/devices/2-1/devnum":    {Data: []byte("33\n")},
				"bus/usb/devices/2-2/idVendor":  {Data: []byte("dead\n")},
				"bus/usb/devices/2-2/idProduct": {Data: []byte("beef\n")},
			},
			err: errors.New("failed to describe device"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver, err := NewSysfsVHCIDriver(tc.fs)
			if (err != nil) != (tc.err != nil) {
				t.Errorf("expected error %v; got %v", tc.err, err)
			}
			if err != nil {
				return
			}
			compareSlots(t, driver, tc.slots)
		})
	}
}

func TestDetachUpdate(t *testing.T) {
	var fsys = fstest.MapFS{
		"bus/platform/devices/vhci_hcd.0/nports": {Data: []byte("4\n")},
		"bus/platform/devices/vhci_hcd.0/status": {Data: []byte(
			statusHeader +
				"hs  0000 006 002 00010002 000010 2-1\n" +
				"hs  0001 004 000 00000000 000000 0-0\n" +
				"hs  0002 004 000 00000000 000000 0-0\n" +
				"ss  0003 006 002 00080002 000011 2-2\n",
		)},
		"bus/usb/devices/2-1/idVendor":  {Data: []byte("dead\n")},
		"bus/usb/devices/2-1/idProduct": {Data: []byte("beef\n")},
		"bus/usb/devices/2-1/busnum":    {Data: []byte("02\n")},
		"bus/usb/devices/2-1/devnum":    {Data: []byte("33\n")},
		"bus/usb/devices/2-2/idVendor":  {Data: []byte("dead\n")},
		"bus/usb/devices/2-2/idProduct": {Data: []byte("beef\n")},
		"bus/usb/devices/2-2/busnum":    {Data: []byte("02\n")},
		"bus/usb/devices/2-2/devnum":    {Data: []byte("34\n")},
	}

	driver, err := NewSysfsVHCIDriver(fsys)

	if err != nil {
		t.Fatal(err)
	}

	delete(fsys, "bus/usb/devices/2-2/idVendor")
	delete(fsys, "bus/usb/devices/2-2/idProduct")
	delete(fsys, "bus/usb/devices/2-2/busnum")
	delete(fsys, "bus/usb/devices/2-2/devnum")
	fsys["bus/platform/devices/vhci_hcd.0/status"] = &fstest.MapFile{Data: []byte(
		statusHeader +
			"hs  0000 006 002 00010002 000010 2-1\n" +
			"hs  0001 004 000 00000000 000000 0-0\n" +
			"hs  0002 004 000 00000000 000000 0-0\n" +
			"ss  0003 004 000 00080000 000000 0-0\n",
	),
	}

	err = driver.UpdateAttachedDevices()
	if err != nil {
		t.Fatal(err)
	}

	expectedSlots := map[int]VHCISlot{
		0: {
			HubSpeed:        HubSpeedHigh,
			Port:            VirtualPort(0),
			Status:          VDevStatusUsed,
			DeviceID:        0x00010002,
			SysPath:         "bus/usb/devices/2-1",
			DevMountPath:    "/dev/bus/usb/002/033",
			LocalDeviceInfo: USBDevice{USBID(0xdead), USBID(0xbeef), "2-1"},
		},
	}

	compareSlots(t, driver, expectedSlots)
}

func TestAttachUpdate(t *testing.T) {
	var fsys = fstest.MapFS{
		"bus/platform/devices/vhci_hcd.0/nports": {Data: []byte("4\n")},
		"bus/platform/devices/vhci_hcd.0/status": {Data: []byte(
			statusHeader +
				"hs  0000 006 002 00010002 000010 2-1\n" +
				"hs  0001 004 000 00000000 000000 0-0\n" +
				"hs  0002 004 000 00000000 000000 0-0\n" +
				"ss  0003 004 000 00080000 000000 0-0\n",
		)},
		"bus/usb/devices/2-1/idVendor":  {Data: []byte("dead\n")},
		"bus/usb/devices/2-1/idProduct": {Data: []byte("beef\n")},
		"bus/usb/devices/2-1/busnum":    {Data: []byte("02\n")},
		"bus/usb/devices/2-1/devnum":    {Data: []byte("33\n")},
	}

	driver, err := NewSysfsVHCIDriver(fsys)

	if err != nil {
		t.Fatal(err)
	}

	fsys["bus/platform/devices/vhci_hcd.0/status"] = &fstest.MapFile{Data: []byte(
		statusHeader +
			"hs  0000 006 002 00010002 000010 2-1\n" +
			"hs  0001 004 000 00000000 000000 0-0\n" +
			"hs  0002 004 000 00000000 000000 0-0\n" +
			"ss  0003 006 002 00080002 000011 2-2\n",
	),
	}
	fsys["bus/usb/devices/2-2/idVendor"] = &fstest.MapFile{Data: []byte("dead\n")}
	fsys["bus/usb/devices/2-2/idProduct"] = &fstest.MapFile{Data: []byte("beef\n")}
	fsys["bus/usb/devices/2-2/busnum"] = &fstest.MapFile{Data: []byte("02\n")}
	fsys["bus/usb/devices/2-2/devnum"] = &fstest.MapFile{Data: []byte("34\n")}

	err = driver.UpdateAttachedDevices()
	if err != nil {
		t.Fatal(err)
	}

	expectedSlots := map[int]VHCISlot{
		0: {
			HubSpeed:        HubSpeedHigh,
			Port:            VirtualPort(0),
			Status:          VDevStatusUsed,
			DeviceID:        0x00010002,
			SysPath:         "bus/usb/devices/2-1",
			DevMountPath:    "/dev/bus/usb/002/033",
			LocalDeviceInfo: USBDevice{USBID(0xdead), USBID(0xbeef), "2-1"},
		},
		3: {
			HubSpeed:        HubSpeedSuper,
			Port:            VirtualPort(3),
			Status:          VDevStatusUsed,
			DeviceID:        0x00080002,
			SysPath:         "bus/usb/devices/2-2",
			DevMountPath:    "/dev/bus/usb/002/034",
			LocalDeviceInfo: USBDevice{USBID(0xdead), USBID(0xbeef), "2-2"},
		},
	}

	compareSlots(t, driver, expectedSlots)
}

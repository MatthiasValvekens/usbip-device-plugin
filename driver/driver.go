// SPDX-License-Identifier: GPL-2.0-only

package driver

// # include "vhci_driver.h"
// # include "usbip_common.h"
// #cgo LDFLAGS: -ludev
import "C"
import (
	"bytes"
	"encoding/binary"
	"net"
	"unsafe"

	"github.com/efficientgo/core/errors"
)

type USBIPDeviceDescription struct {
	Path                     [256]byte
	BusId                    [32]byte
	BusNum                   uint32
	DevNum                   uint32
	Speed                    uint32
	Vendor                   uint16
	Product                  uint16
	BCDDevice                uint16
	DeviceClass              uint8
	DeviceSubClass           uint8
	DeviceProtocol           uint8
	DeviceConfigurationValue uint8
	NumConfigurations        uint8
	NumInterfaces            uint8
}

type USBIPAttachedDevice struct {
	Description  *USBIPDeviceDescription
	RemoteBusNum uint32
	RemoteDevNum uint32
	Port         uint8
}

func DriverOpen() error {
	if C.usbip_vhci_driver_open() != 0 {
		return errors.New("usbip_vhci_driver_open failed")
	}

	return nil
}

func DriverClose() {
	C.usbip_vhci_driver_close()
}

func GetFreePort(speed uint32) (uint8, error) {
	port := C.usbip_vhci_get_free_port(C.uint32_t(speed))
	if port == -1 {
		return 255, errors.New("usbip_vhci_get_free_port failed")
	}
	return uint8(port), nil
}

func AttachDevice(port uint8, conn *net.TCPConn, devid uint32, speed uint32) error {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return errors.Wrap(err, "failed to access raw connection")
	}
	var attachErr error = nil
	err = rawConn.Control(
		func(fd uintptr) {
			retval := C.usbip_vhci_attach_device2(C.uint8_t(port), C.int(fd), C.uint32_t(devid), C.uint32_t(speed))
			if retval != 0 {
				attachErr = errors.New("failed to attach device")
			}
		},
	)
	if attachErr != nil {
		return attachErr
	}
	if err != nil {
		return errors.Wrap(err, "raw I/O to attach device failed")
	}

	return nil
}

func DescribeAttached(port uint8) (*USBIPDeviceDescription, error) {
	var udevRawPtr = C.usbip_vhci_attached_to(C.uint8_t(port))
	if udevRawPtr == nil {
		return nil, errors.New("failed to locate attached device")
	}
	// cgo can't do packed structs by itself
	var udevPtr = unsafe.Pointer(udevRawPtr)
	var udevBytes = C.GoBytes(udevPtr, C.sizeof_struct_usbip_usb_device)
	var goDescr = USBIPDeviceDescription{}
	err := binary.Read(
		bytes.NewBuffer(udevBytes),
		binary.NativeEndian,
		&goDescr,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load usbip device")
	}

	return &goDescr, nil
}

func DescribeAllAttached() ([]*USBIPAttachedDevice, error) {
	// first enumerate occupied ports, then call DescribeAttached.
	// This isn't the most efficient way, but it avoids pushing more complexity
	// to the C layer while evading the difficulties of working with CGO and
	// packed structs. Since this routine is only needed on startup, that's good enough.

	vhciDriver := C.vhci_driver

	devices := make([]*USBIPAttachedDevice, 0)

	// cgo can't deal with flexible trailing arrays in structs, so we have to do some voodoo
	// to copy the data into a Go array.
	var rawIdevsPtr = C.usbip_vhci_imported_devices()
	if rawIdevsPtr == nil {
		return nil, errors.New("vhci_driver not loaded")
	}
	// ports are addressed with uint8_t, so this is good enough
	var idevs = (*[256]C.struct_usbip_imported_device)(unsafe.Pointer(rawIdevsPtr))

	for i := 0; i < int(vhciDriver.nports); i++ {
		idev := idevs[i]

		if idev.status == C.VDEV_ST_NOTASSIGNED || idev.status == C.VDEV_ST_USED {
			devices = append(devices, &USBIPAttachedDevice{
				RemoteBusNum: uint32(idev.busnum),
				RemoteDevNum: uint32(idev.devnum),
				Port:         uint8(idev.port),
			})
		}
	}

	for _, dev := range devices {
		description, err := DescribeAttached(dev.Port)
		if err != nil {
			return nil, errors.Wrapf(err, "Error while retrieving details for device on port %d", dev.Port)
		}
		dev.Description = description
	}
	return devices, nil
}

func DetachDevice(port uint8) error {
	retval := C.usbip_vhci_detach_device(C.uint8_t(port))
	if retval != 0 {
		return errors.New("usbip_vhci_detach_device failed")
	}
	return nil
}

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

func USBIP_VHCIDriverOpen() error {
	if C.usbip_vhci_driver_open() != 0 {
		return errors.New("usbip_vhci_driver_open failed")
	}

	return nil
}

func USBIP_VHCIDriverClose() {
	C.usbip_vhci_driver_close()
}

func USBIP_VHCIRefreshDeviceList() error {
	if C.usbip_vhci_refresh_device_list() != 0 {
		return errors.New("usbip_vhci_refresh_device_list failed")
	}
	return nil
}

func USBIP_VHCIGetFreePort(speed uint32) (uint8, error) {
	port := C.usbip_vhci_get_free_port(C.uint32_t(speed))
	if port == -1 {
		return 255, errors.New("usbip_vhci_get_free_port failed")
	}
	return uint8(port), nil
}

func USBIP_VHCIAttachDevice(port uint8, conn *net.TCPConn, devid uint32, speed uint32) error {
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

func USBIP_VHCIDescribeAttached(port uint8) (*USBIPDeviceDescription, error) {

	var importedDevPtr = C.usbip_vhci_attached_to(C.uint8_t(port))

	if importedDevPtr == nil {
		return nil, errors.New("failed to determine device attached to port")
	}

	var importedDev C.struct_usbip_imported_device = *importedDevPtr

	// 0x06
	if importedDev.status != C.VDEV_ST_USED {
		return nil, errors.New("vhci_driver device not used")
	}

	// cgo can't do packed structs by itself
	var idevPtr = unsafe.Pointer(importedDevPtr)
	var udevPtr = unsafe.Pointer(uintptr(idevPtr) + C.sizeof_struct_usbip_imported_device - C.sizeof_struct_usbip_usb_device)
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

	//var goDescr = &USBIPDeviceDescription{
	//	descr.path,
	//	descr.busid,
	//	descr.busnum,
	//	descr.devnum,
	//	descr.speed,
	//	descr.idVendor,
	//	descr.idProduct,
	//	descr.bcdDevice,
	//	descr.bDeviceClass,
	//	descr.bDeviceSubClass,
	//	descr.bDeviceProtocol,
	//	descr.bConfigurationValue,
	//	descr.bNumConfigurations,
	//	descr.bNumInterfaces,
	//}
	return &goDescr, nil
}

func USBIP_VHCIDetachDevice(port uint8) error {
	retval := C.usbip_vhci_detach_device(C.uint8_t(port))
	if retval != 0 {
		return errors.New("usbip_vhci_detach_device failed")
	}
	return nil
}

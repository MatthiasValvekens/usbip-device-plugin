package usbip

import (
	"net"
)

// USBID is a representation of a platform or vendor ID under the USB standard (see gousb.ID)
type USBID uint16

type Target struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type Connection struct {
	Target     Target
	connection net.Conn
}

type Device struct {
	// Vendor is the USB Vendor ID of the device.
	Vendor USBID `json:"vendor"`
	// Product is the USB Product ID of the device.
	Product USBID `json:"product"`
	// BusId describes USB Bus ID of the device.
	BusId string `json:"bus_id"`
}

type AttachedDevice struct {
	Device
	Target       Target      `json:"target"`
	Port         VirtualPort `json:"vhc_port"`
	DevMountPath string      `json:"dev_mount_path"`
}

type usbipHeader struct {
	Version uint16
	Code    uint16
	Status  uint32
}

type usbipInterfaceDescription struct {
	InterfaceClass    uint8
	InterfaceSubClass uint8
	InterfaceProtocol uint8
	_                 uint8
}

type VirtualPort uint8

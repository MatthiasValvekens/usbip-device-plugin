// SPDX-License-Identifier: Apache-2.0

package driver

import "net"

type USBDeviceSpeed uint32

const (
	USBSpeedUnknown USBDeviceSpeed = iota
	USBSpeedLow
	USBSpeedFull
	USBSpeedHigh
	USBSpeedWireless
	USBSpeedSuper
)

const (
	VHCIControllerBusType    = "platform"
	VHCIControllerDeviceName = "vhci_hcd.0"
)

type HubSpeed uint8

const (
	HubSpeedHigh HubSpeed = iota
	HubSpeedSuper
)

type USBIPStatus uint32
type USBID uint16

const (
	SDevStatusUndefined USBIPStatus = iota
	SDevStatusAvailable
	SDevStatusUsed
	SDevStatusError
	VDevStatusNull
	VDevStatusNotAssigned
	VDevStatusUsed
	VDevStatusError
)

type VirtualPort uint8

type USBDevice struct {
	// Vendor is the USB Vendor ID of the device.
	Vendor USBID `json:"vendor"`
	// Product is the USB Product ID of the device.
	Product USBID `json:"product"`
	// BusId describes USB Bus ID of the device.
	BusId string `json:"bus_id"`
}

type VHCISlot struct {
	HubSpeed HubSpeed
	Port     VirtualPort
	Status   USBIPStatus

	DeviceID        uint32
	SysPath         string
	DevMountPath    string
	LocalDeviceInfo USBDevice
}

type VHCIDriver interface {
	AttachDevice(conn *net.TCPConn, deviceId uint32, speed USBDeviceSpeed) (VirtualPort, error)
	DetachDevice(port VirtualPort) error
	UpdateAttachedDevices() error
	GetDeviceSlots() []VHCISlot
}

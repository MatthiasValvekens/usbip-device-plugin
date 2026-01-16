package driver

type USBDeviceSpeed uint32

const (
	USBSpeedUnknown USBDeviceSpeed = iota
	USBSpeedLow
	USBSpeedFull
	USBSpeedHigh
	USBSpeedWireless
	USBSpeedSuper
)

type USBIPDeviceDescription struct {
	Path                     [256]byte
	BusId                    [32]byte
	BusNum                   uint32
	DevNum                   uint32
	Speed                    USBDeviceSpeed
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

type USBIPAttachedDevice struct {
	HubSpeed HubSpeed
	Port     VirtualPort
	Status   USBIPStatus

	DeviceID uint32

	Description *USBIPDeviceDescription
}

type VHCIDriver struct {
	hostController *UdevDevice

	AvailableControllers uint

	AttachedDevices []USBIPAttachedDevice
}

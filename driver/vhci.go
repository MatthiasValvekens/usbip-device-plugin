package driver

import (
	"github.com/efficientgo/core/errors"
)

func NewVHCIDriver() (VHCIDriver, error) {
	udev := NewContext()
	if udev == nil {
		return nil, errors.New("failed to open udev")
	}

	hostController, err := udev.NewDeviceFromSubsystemSysname(
		VHCIControllerBusType,
		VHCIControllerDeviceName,
	)

	nPorts, err := countPorts(hostController)
	if err != nil {
		return nil, err
	}

	nControllers, err := countControllers(hostController)
	if err != nil {
		return nil, err
	}

	driver := &libUdevVHCIDriver{
		hostController:       hostController,
		AvailableControllers: nControllers,
		AttachedDevices:      make([]USBIPAttachedDevice, nPorts),
	}

	err = driver.UpdateAttachedDevices()
	if err != nil {
		return nil, err
	}

	return driver, nil
}

func DescribeAttached(port VirtualPort, vhci VHCIDriver) (*USBIPDeviceDescription, error) {
	var devices = vhci.GetDeviceSlots()
	var description *USBIPDeviceDescription
	if int(port) > len(devices) {
		return nil, errors.Newf("port number %d out of bounds", port)
	}
	description = devices[port].Description

	if description == nil {
		return nil, errors.Newf("no device attached to port %d", port)
	}

	return description, nil
}

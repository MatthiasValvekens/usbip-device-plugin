package driver

import (
	"github.com/efficientgo/core/errors"
)

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

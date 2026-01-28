package driver

import (
	"github.com/efficientgo/core/errors"
)

func DescribeAttached(port VirtualPort, vhci VHCIDriver) (*VHCISlot, error) {
	var devices = vhci.GetDeviceSlots()
	if int(port) > len(devices) {
		return nil, errors.Newf("port number %d out of bounds", port)
	}
	attachedDevice := devices[port]
	if attachedDevice.Status != VDevStatusUsed {
		return nil, errors.Newf("no device attached to port %d", port)
	}

	return &attachedDevice, nil
}

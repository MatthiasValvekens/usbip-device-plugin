// SPDX-License-Identifier: Apache-2.0

package driver

import (
	"github.com/efficientgo/core/errors"
)

func (p *VHCISlot) IsDeviceConnected() bool {
	return p.Status == VDevStatusUsed
}

func (p *VHCISlot) IsEmpty() bool {
	return p.Status == VDevStatusNull
}

func DescribeAttached(port VirtualPort, vhci VHCIDriver) (*VHCISlot, error) {
	var devices = vhci.GetDeviceSlots()
	if int(port) > len(devices) {
		return nil, errors.Newf("port number %d out of bounds", port)
	}
	slot := devices[port]
	if !slot.IsDeviceConnected() {
		return nil, errors.Newf("no device attached to port %d", port)
	}

	return &slot, nil
}

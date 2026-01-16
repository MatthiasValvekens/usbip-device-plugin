package usbip

import "github.com/MatthiasValvekens/usbip-device-plugin/driver"

func Detach(port driver.VirtualPort) error {
	vhci, err := driver.NewVHCIDriver()
	if err != nil {
		return err
	}
	defer vhci.Close()
	return vhci.DetachDevice(port)
}

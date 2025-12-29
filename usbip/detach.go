package usbip

import "github.com/MatthiasValvekens/usbip-device-plugin/driver"

func Detach(port VirtualPort) error {
	err := driver.DriverOpen()
	if err != nil {
		return err
	}
	defer driver.DriverClose()
	return driver.DetachDevice(uint8(port))
}

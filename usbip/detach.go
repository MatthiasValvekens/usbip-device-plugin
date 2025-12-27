package usbip

import "github.com/MatthiasValvekens/usbip-device-plugin/driver"

func Detach(port VHCPort) error {
	err := driver.USBIP_VHCIDriverOpen()
	if err != nil {
		return err
	}
	defer driver.USBIP_VHCIDriverClose()
	return driver.USBIP_VHCIDetachDevice(uint8(port))
}

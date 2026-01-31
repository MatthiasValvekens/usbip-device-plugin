package usbip

import (
	"encoding/binary"
	"time"

	"github.com/MatthiasValvekens/usbip-device-plugin/driver"
	"github.com/efficientgo/core/errors"
)

const (
	waitForDeviceReadyStep     = 3 * time.Second
	waitForDeviceReadyAttempts = 5
)

type usbipImportRequest struct {
	usbipHeader
	BusId [32]byte
}

type usbipImportResponse struct {
	usbipHeader
	DeviceDescription
}

func (c *Connection) ImportRequest(busId string) (*DeviceDescription, error) {
	var now = time.Now()
	var busIdBin [32]byte
	copy(busIdBin[:], busId)

	conn := c.connection

	err := conn.SetReadDeadline(now.Add(5 * time.Second))
	if err != nil {
		return nil, err
	}

	err = binary.Write(
		conn, binary.BigEndian,
		usbipImportRequest{
			usbipHeader{0x0111, 0x8003, 0},
			busIdBin,
		},
	)

	if err != nil {
		return nil, errors.Wrap(err, "failed to write import command")
	}

	resp := usbipImportResponse{}
	err = binary.Read(conn, binary.BigEndian, &resp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read import response")
	}
	if resp.Status != 0 {
		return nil, errors.New("import command returned error")
	}

	if resp.BusId != busIdBin {
		return nil, errors.New("import command returned unexpected busId")
	}

	return &resp.DeviceDescription, nil
}

func Import(busId string, t Target, vhci driver.VHCIDriver, dialer Dialer) (*AttachedDevice, error) {
	c, err := dialer.Dial(t)
	if err != nil {
		return nil, err
	}

	defer c.Close()

	resp, err := c.ImportRequest(busId)
	if err != nil {
		return nil, err
	}

	port, err := attachImported(c, *resp, vhci)
	if err != nil {
		return nil, errors.Wrap(err, "failed to attach imported device")
	}
	var slot *driver.VHCISlot
	for i := 0; i < waitForDeviceReadyAttempts; i++ {
		if err = vhci.UpdateAttachedDevices(); err != nil {
			break
		}
		if slot, err = driver.DescribeAttached(port, vhci); err == nil {
			break
		}
		time.Sleep(waitForDeviceReadyStep)
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to describe attached device")
	}
	attachedDev := &AttachedDevice{
		USBDevice: driver.USBDevice{
			Vendor:  driver.USBID(resp.Vendor),
			Product: driver.USBID(resp.Product),
			BusId:   busId,
		},
		Target:       c.GetTarget(),
		Port:         port,
		DevMountPath: slot.DevMountPath,
	}

	return attachedDev, nil
}

func Detach(port driver.VirtualPort, vhci driver.VHCIDriver) error {
	err := vhci.DetachDevice(port)
	if err != nil {
		return err
	}

	for i := 0; i < waitForDeviceReadyAttempts; i++ {
		if err = vhci.UpdateAttachedDevices(); err != nil {
			break
		}
		if vhci.GetDeviceSlots()[port].IsEmpty() {
			break
		}
		time.Sleep(waitForDeviceReadyStep)
	}
	return err
}

func attachImported(c Client, resp DeviceDescription, vhci driver.VHCIDriver) (driver.VirtualPort, error) {
	port, err := vhci.AttachDevice(
		c.getConnection(),
		resp.BusNum<<16|resp.DevNum,
		resp.Speed,
	)
	if err != nil {
		return driver.VirtualPort(0), err
	}

	return port, err
}

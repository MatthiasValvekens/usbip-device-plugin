package usbip

import (
	"bytes"
	"encoding/binary"
	"time"

	"github.com/MatthiasValvekens/usbip-device-plugin/driver"
	"github.com/efficientgo/core/errors"
)

type usbipDevlistResponseHeader struct {
	usbipHeader
	NumDevices uint32
}

func (c *Connection) ListRequest() ([]Device, error) {
	var conn = c.connection
	var now = time.Now()

	err := conn.SetReadDeadline(now.Add(5 * time.Second))
	if err != nil {
		return nil, err
	}

	err = binary.Write(
		conn, binary.BigEndian,
		usbipHeader{0x0111, 0x8005, 0},
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to write devlist command")
	}

	hdr := usbipDevlistResponseHeader{}
	err = binary.Read(conn, binary.BigEndian, &hdr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response to devlist command")
	}

	if hdr.Status != 0 {
		return nil, errors.New("devlist command returned error")
	}

	devices := make([]Device, hdr.NumDevices)
	dev := driver.USBIPDeviceDescription{}
	var tmpBuf = [1024]byte{}
	for devIx := 0; devIx < int(hdr.NumDevices); devIx++ {
		err = binary.Read(conn, binary.BigEndian, &dev)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read devices in devlist response")
		}
		devices[devIx] = Device{
			Vendor:  USBID(dev.Vendor),
			Product: USBID(dev.Product),
			BusId:   string(dev.BusId[:bytes.IndexByte(dev.BusId[:], 0)]),
		}
		// skip over the interface sections
		var bytesToSkip = 4 * int(dev.NumInterfaces)
		if bytesToSkip > 1024 {
			return nil, errors.New("unexpected number of interfaces in devlist response")
		}
		_, err := conn.Read(tmpBuf[:bytesToSkip])
		if err != nil {
			return nil, errors.Wrap(err, "devlist entry ended early")
		}
	}

	return devices, nil
}

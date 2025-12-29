package usbip

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path"
	"strings"
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
	driver.USBIPDeviceDescription
}

func (t Target) Import(busId string) (*AttachedDevice, error) {
	c, err := t.Dial()
	if err != nil {
		return nil, err
	}
	var conn = c.connection
	var now = time.Now()
	var busIdBin [32]byte
	copy(busIdBin[:], busId)

	defer c.Close()

	err = conn.SetReadDeadline(now.Add(5 * time.Second))
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

	port, err := c.attachImported(resp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to attach imported device")
	}
	var description *driver.USBIPDeviceDescription
	for i := 0; i < waitForDeviceReadyAttempts; i++ {
		if description, err = c.describeAttached(port); err == nil {
			break
		}
		time.Sleep(waitForDeviceReadyStep)
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to describe attached device")
	}
	devName, err := FindDevMountPath(description)
	if err != nil {
		return nil, errors.Wrap(err, "failed to find device mount path")
	}
	attachedDev := &AttachedDevice{
		Device: Device{
			Vendor:  USBID(resp.Vendor),
			Product: USBID(resp.Product),
			BusId:   busId,
		},
		Target:       c.Target,
		Port:         port,
		DevMountPath: path.Join("/dev", devName),
	}

	return attachedDev, nil
}

func FindDevMountPath(description *driver.USBIPDeviceDescription) (string, error) {
	parent := string(description.Path[:bytes.IndexByte(description.Path[:], 0)])
	ueventPath := path.Join(parent, "uevent")

	inf, err := os.Open(ueventPath)
	if err != nil {
		return "", errors.Wrap(err, "failed to open uevent file")
	}
	defer func(inf *os.File) {
		_ = inf.Close()
	}(inf)

	reader := bufio.NewReader(inf)
	var devName string
	var wasDevName bool
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			return "", errors.Newf("failed to determine device mount; no DEVNAME in %s", ueventPath)
		} else if err != nil {
			return "", errors.Wrapf(err, "failed to determine device mount from %s", ueventPath)
		}

		devName, wasDevName = strings.CutPrefix(line, "DEVNAME=")
		devName = strings.TrimSpace(devName)
		if wasDevName {
			break
		}
	}

	return devName, nil

}

func (c *Connection) attachImported(resp usbipImportResponse) (VirtualPort, error) {
	err := driver.DriverOpen()
	if err != nil {
		return VirtualPort(0), err
	}
	defer driver.DriverClose()

	port, err := driver.GetFreePort(resp.Speed)
	if err != nil {
		return VirtualPort(0), err
	}

	err = driver.AttachDevice(
		port,
		c.connection.(*net.TCPConn),
		resp.BusNum<<16|resp.DevNum,
		resp.Speed,
	)

	return VirtualPort(port), err
}

func (c *Connection) describeAttached(port VirtualPort) (*driver.USBIPDeviceDescription, error) {
	err := driver.DriverOpen()
	if err != nil {
		return nil, err
	}
	defer driver.DriverClose()

	var description *driver.USBIPDeviceDescription
	description, err = driver.DescribeAttached(uint8(port))

	if err != nil {
		return nil, err
	}

	return description, nil
}

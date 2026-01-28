package driver

import (
	baseerrors "errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path"
	"strings"

	"github.com/efficientgo/core/errors"
)

type sysfsVHCIDriver struct {
	fsys fs.FS

	AvailableControllers uint

	AttachedDevices []VHCISlot
}

const (
	sysBus = "bus"
)

func hostControllerPath() string {
	return path.Join(sysBus, VHCIControllerBusType, "devices", VHCIControllerDeviceName)
}

func usbSysPath(busId string) string {
	return path.Join(sysBus, "usb", "devices", busId)
}

func (d *sysfsVHCIDriver) GetDeviceSlots() []VHCISlot {
	return d.AttachedDevices
}

func (d *sysfsVHCIDriver) readDeviceAttribute(sysPath string, attributeName string) (string, error) {
	content, err := fs.ReadFile(d.fsys, path.Join(sysPath, attributeName))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}

func (d *sysfsVHCIDriver) readDeviceUint8HexAttribute(sysPath string, attributeName string) (uint8, error) {
	attrStr, err := d.readDeviceAttribute(sysPath, attributeName)
	if err != nil {
		return 0, err
	}
	var result uint8 = 0
	_, err = fmt.Sscanf(attrStr, "%02x", &result)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to read device attribute %s", attributeName)
	}
	return result, nil
}

func (d *sysfsVHCIDriver) readDeviceUint16HexAttribute(sysPath string, attributeName string) (uint16, error) {
	attrStr, err := d.readDeviceAttribute(sysPath, attributeName)
	if err != nil {
		return 0, err
	}
	var result uint16 = 0
	_, err = fmt.Sscanf(attrStr, "%04x", &result)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to read device attribute %s", attributeName)
	}
	return result, nil
}

func (d *sysfsVHCIDriver) initPorts() error {
	nportsStr, err := d.readDeviceAttribute(hostControllerPath(), "nports")
	if err != nil {
		return errors.New("failed to read nports attribute")
	}
	var nports uint32 = 0
	_, err = fmt.Sscanf(nportsStr, "%d", &nports)
	if err != nil {
		return errors.New("failed to parse nports attribute")
	}
	if nports <= 0 {
		return errors.New("VHCI host controller does not have any ports available")
	}

	d.AttachedDevices = make([]VHCISlot, nports)
	return nil
}

func (d *sysfsVHCIDriver) countControllers() error {
	// count controllers
	var count uint = 0
	devicesDir := path.Join(sysBus, VHCIControllerBusType, "devices")
	files, err := fs.ReadDir(d.fsys, devicesDir)
	if err != nil {
		return errors.Wrap(err, "failed to read platform sysdir")
	}
	for _, file := range files {
		if file.IsDir() && strings.HasPrefix(file.Name(), "vhci_hcd.") {
			count++
		}
	}

	d.AvailableControllers = count
	return nil
}

func (d *sysfsVHCIDriver) describeUsbFromBusId(attachedDevice *VHCISlot, busId string) error {
	sysPath := usbSysPath(busId)

	vendor, vendErr := d.readDeviceUint16HexAttribute(sysPath, "idVendor")
	product, prodErr := d.readDeviceUint16HexAttribute(sysPath, "idProduct")

	totalErr := baseerrors.Join(vendErr, prodErr)

	if totalErr != nil {
		return errors.Wrap(totalErr, "failed to describe device")
	}

	attachedDevice.LocalDeviceInfo = USBDevice{
		BusId:   busId,
		Vendor:  USBID(vendor),
		Product: USBID(product),
	}
	return nil
}

func (d *sysfsVHCIDriver) updateDevicesFromControllerStatus(statusContent string) error {
	lines := strings.Split(statusContent, "\n")

	var port VirtualPort
	var deviceId uint32
	var speed int
	var status USBIPStatus
	var fd uint // ignored
	var hubSpeed string
	var busId string
	for i, line := range lines[1:] {
		_, err := fmt.Sscanf(
			line,
			"%2s  %d %d %d %x %d %31s",
			&hubSpeed, &port, &status, &speed, &deviceId, &fd, &busId,
		)
		if err != nil {
			return errors.Wrapf(err, "failed to parse status line %d: %s", i, line)
		}

		if int(port) > len(d.AttachedDevices) {
			return errors.Newf("failed to parse status line %d: port %d out of range", i, port)
		}

		var device = &d.AttachedDevices[port]

		switch hubSpeed {
		case "hs":
			device.HubSpeed = HubSpeedHigh
		default:
			device.HubSpeed = HubSpeedSuper
		}

		device.Port = port
		device.Status = status
		device.DeviceID = deviceId
		device.SysPath = usbSysPath(busId)

		if status == VDevStatusNull || status == VDevStatusNotAssigned {
			device.LocalDeviceInfo = USBDevice{}
		} else {
			err = d.describeUsbFromBusId(device, busId)
			if err != nil {
				return errors.Wrapf(err, "failed to describe device %s", busId)
			}
		}

		device.DevMountPath, err = d.findDevMountPath(device.SysPath)
		if err != nil {
			return errors.Wrapf(err, "failed to find device mount path for %s", busId)
		}
	}
	return nil
}

func (d *sysfsVHCIDriver) UpdateAttachedDevices() error {
	var i uint
	for i = 0; i < d.AvailableControllers; i++ {
		var name string
		if i == 0 {
			name = "status"
		} else {
			name = fmt.Sprintf("status.%d", i)
		}
		status, err := d.readDeviceAttribute(hostControllerPath(), name)
		if err != nil {
			return errors.Newf("failed to get status of controller %d", i)
		}
		err = d.updateDevicesFromControllerStatus(status)
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *sysfsVHCIDriver) GetFreePort(speed USBDeviceSpeed) (VirtualPort, error) {
	for _, device := range d.AttachedDevices {
		// Exclusively pair super devices with super ports and vice versa
		if (device.HubSpeed == HubSpeedSuper) != (speed == USBSpeedSuper) {
			continue
		}

		if device.Status == VDevStatusNull {
			return device.Port, nil
		}
	}
	return 0, errors.New("failed to find free port")
}

func (d *sysfsVHCIDriver) AttachDevice(conn *net.TCPConn, deviceId uint32, speed USBDeviceSpeed) (VirtualPort, error) {
	port, err := d.GetFreePort(speed)
	if err != nil {
		return 0, err
	}

	rawConn, err := conn.SyscallConn()
	if err != nil {
		return 0, errors.Wrap(err, "failed to access raw connection")
	}
	var attachErr error = nil
	err = rawConn.Control(
		func(fd uintptr) {
			attachErr = d.doAttachDevice(port, uint(fd), deviceId, speed)
		},
	)
	if attachErr != nil {
		return 0, attachErr
	}
	if err != nil {
		return 0, errors.Wrap(err, "raw I/O to attach device failed")
	}

	return port, nil
}

func (d *sysfsVHCIDriver) doAttachDevice(port VirtualPort, fd uint, deviceId uint32, speed USBDeviceSpeed) error {
	attachPath := path.Join(hostControllerPath(), "attach")
	attachStr := fmt.Sprintf("%d %d %d %d\n", port, fd, deviceId, speed)
	return writeStringToFile(attachPath, attachStr)
}

func (d *sysfsVHCIDriver) DetachDevice(port VirtualPort) error {
	if int(port) > len(d.AttachedDevices) {
		return errors.Newf("port number %d out of bounds", port)
	}
	detachPath := path.Join(hostControllerPath(), "detach")
	detachStr := fmt.Sprintf("%d\n", port)
	return writeStringToFile(detachPath, detachStr)
}

func writeStringToFile(path string, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return errors.Wrapf(err, "failed to open %s for writing", path)
	}
	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	_, err = f.WriteString(content)
	if err != nil {
		return errors.Wrapf(err, "failed to write command to %s", path)
	}
	return nil
}

func NewSysfsVHCIDriver(fsys fs.FS) (VHCIDriver, error) {
	driver := &sysfsVHCIDriver{
		fsys: fsys,
	}

	err := driver.initPorts()
	if err != nil {
		return nil, err
	}

	err = driver.countControllers()
	if err != nil {
		return nil, err
	}

	err = driver.UpdateAttachedDevices()
	if err != nil {
		return nil, err
	}

	return driver, nil
}

func (d *sysfsVHCIDriver) findDevMountPath(sysPath string) (string, error) {
	ueventPath := path.Join(sysPath, "uevent")

	contents, err := fs.ReadFile(d.fsys, ueventPath)
	if err != nil {
		return "", errors.Wrap(err, "failed to open uevent file")
	}
	lines := strings.Split(string(contents), "\n")
	var devName string
	var wasDevName bool
	for _, line := range lines {
		devName, wasDevName = strings.CutPrefix(line, "DEVNAME=")
		devName = strings.TrimSpace(devName)
		if wasDevName {
			break
		}
	}

	return path.Join("/dev", devName), nil
}

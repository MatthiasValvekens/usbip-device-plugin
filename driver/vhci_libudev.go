package driver

import (
	"fmt"
	"net"
	"os"
	"path"
	"strings"

	"github.com/efficientgo/core/errors"
)

type libUdevVHCIDriver struct {
	hostController *UdevDevice

	AvailableControllers uint

	AttachedDevices []USBIPAttachedDevice
}

func (d *libUdevVHCIDriver) GetDeviceSlots() []USBIPAttachedDevice {
	return d.AttachedDevices
}

func countPorts(hostController *UdevDevice) (uint32, error) {

	nportsStr, err := hostController.readDeviceAttribute("nports")
	if err != nil {
		return 0, errors.New("failed to read nports attribute")
	}
	var nports uint32 = 0
	_, err = fmt.Sscanf(nportsStr, "%d", &nports)
	if err != nil {
		return 0, errors.New("failed to parse nports attribute")
	}
	if nports <= 0 {
		return 0, errors.New("VHCI host controller does not have any ports available")
	}

	return nports, nil
}

func countControllers(hostController *UdevDevice) (uint, error) {
	// count controllers
	var count uint = 0
	platformDevice, err := hostController.OpenParent()
	if err != nil {
		return 0, err
	}
	defer platformDevice.Close()
	files, err := os.ReadDir(platformDevice.sysPath)
	if err != nil {
		return 0, errors.Wrap(err, "failed to read platform sysdir")
	}
	for _, file := range files {
		if file.IsDir() && strings.HasPrefix(file.Name(), "vhci_hcd.") {
			count++
		}
	}

	return count, nil
}

func (d *libUdevVHCIDriver) describeUsbFromBusId(busId string) (*USBIPDeviceDescription, error) {
	udev, err := d.hostController.context.NewDeviceFromSubsystemSysname("usb", busId)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open device %s", busId)
	}
	defer udev.Close()

	return udev.Describe()
}

func (d *libUdevVHCIDriver) updateDevicesFromControllerStatus(statusContent string) error {
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

		if status == VDevStatusNull || status == VDevStatusNotAssigned {
			device.Description = nil
		} else {
			device.Description, err = d.describeUsbFromBusId(busId)
			if err != nil {
				return errors.Wrapf(err, "failed to describe device %s", busId)
			}
		}
	}
	return nil
}

func (d *libUdevVHCIDriver) UpdateAttachedDevices() error {
	var i uint
	for i = 0; i < d.AvailableControllers; i++ {
		var name string
		if i == 0 {
			name = "status"
		} else {
			name = fmt.Sprintf("status.%d", i)
		}
		status, err := d.hostController.readDeviceAttribute(name)
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

func (d *libUdevVHCIDriver) GetFreePort(speed USBDeviceSpeed) (VirtualPort, error) {
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

func (d *libUdevVHCIDriver) AttachDevice(conn *net.TCPConn, deviceId uint32, speed USBDeviceSpeed) (VirtualPort, error) {
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

func (d *libUdevVHCIDriver) doAttachDevice(port VirtualPort, fd uint, deviceId uint32, speed USBDeviceSpeed) error {
	attachPath := path.Join(d.hostController.sysPath, "attach")
	attachStr := fmt.Sprintf("%d %d %d %d", port, fd, deviceId, speed)
	return writeStringToFile(attachPath, attachStr)
}

func (d *libUdevVHCIDriver) DetachDevice(port VirtualPort) error {
	if int(port) > len(d.AttachedDevices) {
		return errors.Newf("port number %d out of bounds", port)
	}
	detachPath := path.Join(d.hostController.sysPath, "detach")
	detachStr := fmt.Sprintf("%d", port)
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

func (d *libUdevVHCIDriver) Close() {
	d.hostController.Close()
	d.hostController.context.Close()
}

func NewLibUdevVHCIDriver() (VHCIDriver, error) {
	udev := NewContext()
	if udev == nil {
		return nil, errors.New("failed to open udev")
	}

	hostController, err := udev.NewDeviceFromSubsystemSysname(
		VHCIControllerBusType,
		VHCIControllerDeviceName,
	)

	nPorts, err := countPorts(hostController)
	if err != nil {
		return nil, err
	}

	nControllers, err := countControllers(hostController)
	if err != nil {
		return nil, err
	}

	driver := &libUdevVHCIDriver{
		hostController:       hostController,
		AvailableControllers: nControllers,
		AttachedDevices:      make([]USBIPAttachedDevice, nPorts),
	}

	err = driver.UpdateAttachedDevices()
	if err != nil {
		return nil, err
	}

	return driver, nil
}

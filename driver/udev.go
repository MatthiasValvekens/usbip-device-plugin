package driver

// #include <libudev.h>
// #include <stdlib.h>
// #include <string.h>
// #cgo LDFLAGS: -ludev
import "C"
import (
	"fmt"
	"unsafe"

	baseerrors "errors"

	"github.com/efficientgo/core/errors"
)

type UdevContext struct {
	udev *C.struct_udev
}

type UdevDevice struct {
	context *UdevContext
	device  *C.struct_udev_device
	sysPath string
}

func NewContext() *UdevContext {
	var udev = C.udev_new()

	if udev == nil {
		return nil
	}

	return &UdevContext{udev}
}

func (ctx *UdevContext) NewDeviceFromSubsystemSysname(subsystem string, sysname string) (*UdevDevice, error) {

	cSubsys := C.CString(subsystem)
	cSysname := C.CString(sysname)

	var devPtr *C.struct_udev_device = C.udev_device_new_from_subsystem_sysname(
		ctx.udev,
		cSubsys,
		cSysname,
	)
	C.free(unsafe.Pointer(cSubsys))
	C.free(unsafe.Pointer(cSysname))

	if devPtr == nil {
		return nil, errors.Newf("failed to open udev device %s in subsystem %s", sysname, subsystem)
	}

	sysPath := C.udev_device_get_syspath(devPtr)
	if sysPath == nil {
		return nil, errors.Newf("failed to get syspath for udev device %s in subsystem %s", sysname, subsystem)
	}

	result := &UdevDevice{
		context: ctx,
		device:  devPtr,
		sysPath: C.GoString(sysPath),
	}
	return result, nil
}

func (ctx *UdevContext) Close() {
	if ctx.udev == nil {
		return
	}
	C.udev_unref(ctx.udev)
	ctx.udev = nil
}

func (d *UdevDevice) readDeviceAttribute(attributeName string) (string, error) {
	cName := C.CString(attributeName)

	attr := C.udev_device_get_sysattr_value(d.device, cName)
	C.free(unsafe.Pointer(cName))

	if attr == nil {
		return "", errors.Newf("failed to read device attribute %s", attributeName)
	}
	return C.GoString(attr), nil
}

func (d *UdevDevice) readDeviceUint8HexAttribute(attributeName string) (uint8, error) {
	attrStr, err := d.readDeviceAttribute(attributeName)
	if err != nil {
		return 0, err
	}
	var result uint8 = 0
	_, err = fmt.Sscanf(attrStr, "%02x\n", &result)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to read device attribute %s", attributeName)
	}
	return result, nil
}

func (d *UdevDevice) readDeviceUint16HexAttribute(attributeName string) (uint16, error) {
	attrStr, err := d.readDeviceAttribute(attributeName)
	if err != nil {
		return 0, err
	}
	var result uint16 = 0
	_, err = fmt.Sscanf(attrStr, "%04x\n", &result)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to read device attribute %s", attributeName)
	}
	return result, nil
}

func (d *UdevDevice) readSpeed() USBDeviceSpeed {
	speedStr, err := d.readDeviceAttribute("speed")
	if err != nil {
		return USBSpeedUnknown
	}

	switch speedStr {
	case "1.5":
		return USBSpeedLow
	case "12":
		return USBSpeedFull
	case "480":
		return USBSpeedHigh
	case "53.3-480":
		return USBSpeedWireless
	case "5000":
		return USBSpeedSuper
	default:
		return USBSpeedUnknown
	}
}

func (d *UdevDevice) Describe() (*USBIPDeviceDescription, error) {

	cName := C.udev_device_get_sysname(d.device)

	var path = [256]byte{}
	var busid = [32]byte{}

	copy(path[:], d.sysPath[:255])
	path[255] = 0
	C.strncpy(cName, (*C.char)(&busid[0]), 31)
	busid[31] = 0

	var busnum, devnum uint32

	_, busnumErr := fmt.Sscanf(C.GoString(cName), "%d-%d", &busnum, &devnum)
	speed := d.readSpeed()

	vendor, vendErr := d.readDeviceUint16HexAttribute("idVendor")
	product, prodErr := d.readDeviceUint16HexAttribute("idProduct")
	bcdDevice, bcdErr := d.readDeviceUint16HexAttribute("bcdDevice")

	deviceClass, dcErr := d.readDeviceUint8HexAttribute("bDeviceClass")
	deviceSubClass, dscErr := d.readDeviceUint8HexAttribute("bDeviceSubClass")
	deviceProtocol, dpErr := d.readDeviceUint8HexAttribute("bDeviceProtocol")

	numConfigs, numCfgsErr := d.readDeviceUint8HexAttribute("bNumConfigurations")

	// these values can be unavailable before the device is attached, and that's OK
	deviceConfig, _ := d.readDeviceUint8HexAttribute("bDeviceConfig")
	numInterfaces, _ := d.readDeviceUint8HexAttribute("bNumInterfaces")

	totalErr := baseerrors.Join(
		busnumErr, vendErr, prodErr, bcdErr, dcErr, dscErr, dpErr, numCfgsErr,
	)

	if totalErr != nil {
		return nil, errors.Wrap(totalErr, "failed to describe device")
	}

	description := USBIPDeviceDescription{
		Path:                     path,
		BusId:                    busid,
		BusNum:                   busnum,
		DevNum:                   devnum,
		Speed:                    speed,
		Vendor:                   vendor,
		Product:                  product,
		BCDDevice:                bcdDevice,
		DeviceClass:              deviceClass,
		DeviceSubClass:           deviceSubClass,
		DeviceProtocol:           deviceProtocol,
		DeviceConfigurationValue: deviceConfig,
		NumConfigurations:        numConfigs,
		NumInterfaces:            numInterfaces,
	}
	return &description, nil
}

func (d *UdevDevice) OpenParent() (*UdevDevice, error) {
	var parentDevice *C.struct_udev_device = C.udev_device_get_parent(d.device)

	if parentDevice == nil {
		return nil, errors.Newf("failed to get parent of device at %s", d.sysPath)
	}

	var cPath = C.udev_device_get_syspath(parentDevice)
	if cPath == nil {
		return nil, errors.Newf("failed to get sys path of parent of device at %s", d.sysPath)
	}
	var path = C.GoString(cPath)

	result := UdevDevice{
		context: d.context,
		device:  parentDevice,
		sysPath: path,
	}

	return &result, nil
}

func (d *UdevDevice) Close() {
	if d.device == nil {
		return
	}
	C.udev_device_unref(d.device)
	d.device = nil
}

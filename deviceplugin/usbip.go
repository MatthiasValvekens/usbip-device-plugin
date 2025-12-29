package deviceplugin

// Copyright 2020 the generic-device-plugin authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/MatthiasValvekens/usbip-device-plugin/driver"
	"github.com/MatthiasValvekens/usbip-device-plugin/usbip"
	"github.com/efficientgo/core/errors"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	v1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

const (
	deviceCheckInterval = 30 * time.Second
)

type KnownDevice struct {
	Target         usbip.Target         `json:"target"`
	Selector       usbip.Device         `json:"selector"`
	ExtraDevices   []v1beta1.DeviceSpec `json:"extras"`
	readProperties usbip.Device
	available      bool
}

func (kd *KnownDevice) SelectorMatches(cand usbip.Device) bool {
	selector := kd.Selector
	return (selector.BusId == "" || cand.BusId == "" || selector.BusId == cand.BusId) &&
		(selector.Vendor == 0 || selector.Vendor == cand.Vendor) &&
		(selector.Product == 0 || selector.Product == cand.Product)
}

type USBIPPlugin struct {
	v1beta1.UnimplementedDevicePluginServer
	resource        string
	knownDevices    map[string]*KnownDevice
	attachedDevices map[string]*usbip.AttachedDevice
	kubeletSocket   string
	logger          log.Logger
	mu              sync.Mutex

	// metrics
	availableDeviceGauge prometheus.Gauge
	attachedDeviceGauge  prometheus.Gauge
	allocationsCounter   prometheus.Counter
}

func (up *USBIPPlugin) Targets() []usbip.Target {
	targetsSeen := map[usbip.Target]bool{}
	targets := make([]usbip.Target, 0)
	for _, dev := range up.knownDevices {
		_, seen := targetsSeen[dev.Target]
		if !seen {
			targetsSeen[dev.Target] = true
			targets = append(targets, dev.Target)
		}
	}
	return targets
}

func NewPluginForDeviceGroup(knownDevices []*KnownDevice, resourceName string, pluginDir string, podResourcesSocket string, logger log.Logger, reg prometheus.Registerer) Plugin {
	if logger == nil {
		logger = log.NewNopLogger()
	}

	devices := make(map[string]*KnownDevice)
	for _, devPtr := range knownDevices {
		if devPtr == nil {
			continue
		}
		dev := *devPtr
		idJson, err := json.Marshal(dev)
		if err != nil {
			panic(fmt.Errorf("failed to marshal device %v: %v", dev, err))
			return nil
		}
		id := fmt.Sprintf("%x", sha256.Sum256(idJson))
		devices[id] = devPtr
	}
	p := &USBIPPlugin{
		resource:        resourceName,
		knownDevices:    devices,
		attachedDevices: map[string]*usbip.AttachedDevice{},
		kubeletSocket:   podResourcesSocket,
		logger:          logger,
		availableDeviceGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "usbip_device_plugin_available_devices",
			Help: "The number of devices managed by this device plugin.",
		}),
		attachedDeviceGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "usbip_device_plugin_attached_devices",
			Help: "The number of devices attached to this node by this device plugin.",
		}),
		allocationsCounter: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "usbip_device_plugin_allocations_total",
			Help: "The total number of device allocations made by this device plugin.",
		}),
	}

	for {
		_ = logger.Log("msg", "Refreshing USB/IP devices...")
		if _, err := p.refreshDevices(); err == nil {
			break
		}
		_ = logger.Log("msg", "Device refresh failed, sleeping for a while...")
		time.Sleep(10 * time.Second)
	}

	_ = logger.Log("msg", "Enumerating attached devices...")
	if err := p.enumerateAttachedDevices(); err != nil {
		_ = logger.Log("msg", "Failed to enumerate attached devices. Assuming none.", "err", err)
	}

	_ = logger.Log("msg", "Preparing device plugin...")
	if reg != nil {
		reg.MustRegister(p.availableDeviceGauge, p.allocationsCounter, p.attachedDeviceGauge)
	}

	return NewPlugin(resourceName, pluginDir, p, logger, prometheus.WrapRegistererWithPrefix("usbip_", reg))
}

func (up *USBIPPlugin) refreshTarget(target usbip.Target) (bool, error) {
	conn, err := target.Dial()

	if err != nil {
		return true, err
	}

	defer conn.Close()

	lst, err := conn.List()

	changed := false
	for devId, kd := range up.knownDevices {
		_, attached := up.attachedDevices[devId]
		// no use checking the returned devices for one that is already attached to us,
		// it won't be part of the response anyway
		if attached {
			continue
		}

		if kd.Target != target {
			continue
		}

		selector := kd.Selector
		found := false
		for _, cand := range lst {
			if !kd.SelectorMatches(cand) {
				continue
			}
			found = true
			devChanged := kd.readProperties != cand
			if devChanged {
				_ = up.logger.Log("msg", "found device or device changed properties", "target", kd.Target, "selector", selector, "found", cand, "previous", kd.readProperties)
			}
			changed = changed || devChanged
			kd.readProperties = cand
			break
		}
		wasAvailable := kd.available
		changed = changed || (wasAvailable != found)
		kd.available = found
		if wasAvailable && !found {
			_ = up.logger.Log("msg", "previously available device no longer available (in use by another node?)", "target", kd.Target, "selector", selector)
			kd.readProperties = usbip.Device{}
		}
	}

	return !changed, err
}

func (up *USBIPPlugin) enumerateAttachedDevices() error {
	err := driver.DriverOpen()
	if err != nil {
		return err
	}
	defer driver.DriverClose()

	attached, err := driver.DescribeAllAttached()
	if err != nil {
		return err
	}

	for _, attachedDev := range attached {
		_ = up.logger.Log("msg", "attempting to pair attached USB/IP device with known device...", "port", attachedDev.Port, "device", *attachedDev)
		dev := usbip.Device{
			Vendor:  usbip.USBID(attachedDev.Description.Vendor),
			Product: usbip.USBID(attachedDev.Description.Product),
			// we intentionally do _not_ set BusId since the bus ID on the remote is not part of the data
			// available to us
			// (TODO: try to figure out if it's somewhere else in sysfs)
			BusId: "",
		}
		found := false
		for devId, kd := range up.knownDevices {
			if !kd.SelectorMatches(dev) {
				continue
			}
			_ = up.logger.Log("msg", "attached device matched with known device", "port", attachedDev.Port, "matched", devId)
			var mountPath string
			mountPath, err = usbip.FindDevMountPath(attachedDev.Description)
			if err != nil {
				// TODO this log can be confusing since this routine is invoked separately for each resource
				//  -> all the more reason to try to refactor things so we can manage all the resources in one structure
				_ = up.logger.Log("msg", "failed to find path to device", "port", attachedDev.Port, "matched", devId, "err", err)
				break
			}
			found = true
			up.attachedDevices[devId] = &usbip.AttachedDevice{
				Device:       dev,
				Target:       kd.Target,
				Port:         usbip.VirtualPort(attachedDev.Port),
				DevMountPath: mountPath,
			}
			break
		}
		if !found {
			_ = up.logger.Log("msg", "failed to pair device with config; ignoring...", "port", attachedDev.Port)
		}
	}
	return nil
}

func (up *USBIPPlugin) releaseDevices() error {
	if len(up.attachedDevices) == 0 {
		// nothing to do
		return nil
	}
	conn, err := kubeletClient(up.kubeletSocket)
	if err != nil {
		return fmt.Errorf("failed to connect to kubelet: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := v1.NewPodResourcesListerClient(conn)
	usage, err := client.List(context.TODO(), &v1.ListPodResourcesRequest{})
	if err != nil {
		return fmt.Errorf("failed to interrogate kubelet about resource usage: %v", err)
	}
	witnesses := make(map[string]string, len(up.attachedDevices))
	for _, podResources := range usage.GetPodResources() {
		for _, containerResources := range podResources.GetContainers() {
			for _, containerDevices := range containerResources.GetDevices() {
				if containerDevices.ResourceName != up.resource {
					continue
				}
				for _, devId := range containerDevices.DeviceIds {
					// record the pod of which the container that holds the device is part
					// so we can log it later if it's one of ours
					witnesses[devId] = fmt.Sprintf("%s/%s", podResources.Namespace, podResources.Name)
				}
			}
		}
	}

	toRemove := make([]string, 0, len(witnesses))

	for devId, attachedDevice := range up.attachedDevices {
		podRef, inUse := witnesses[devId]
		if inUse {
			_ = up.logger.Log("msg", fmt.Sprintf("device %s still in use by %s", devId, podRef))
		} else {
			_ = up.logger.Log("msg", fmt.Sprintf("detaching device %s used by %s", devId, podRef))
			err = usbip.Detach(attachedDevice.Port)
			if err != nil {
				_ = up.logger.Log("msg", fmt.Sprintf("failed to detach %s used by %s", devId, podRef), "err", err)
				continue
			}
			toRemove = append(toRemove, devId)
		}
	}

	for _, devId := range toRemove {
		delete(up.attachedDevices, devId)
	}

	if err != nil {
		return errors.Wrap(err, "There were errors detaching some devices")
	}

	return nil
}

// refreshDevices updates the devices available to the
// USB/IP device plugin and returns a boolean indicating
// if the state is the same as before.
func (up *USBIPPlugin) refreshDevices() (bool, error) {
	up.mu.Lock()
	defer up.mu.Unlock()
	changed := false
	// FIXME detect attached devices across restarts
	err := up.releaseDevices()
	if err != nil {
		_ = up.logger.Log("msg", fmt.Sprintf("failed to release device %s of resource %s", up.resource, up.resource), "err", err)
	}
	// even if the release fails, go on
	// FIXME decouple this per-target refresh from the plugin registration in kubelet
	//  so we only have to hit each target once
	for _, target := range up.Targets() {
		var targetUnchanged bool
		targetUnchanged, err = up.refreshTarget(target)

		if err != nil {
			_ = up.logger.Log("warn", fmt.Sprintf("skipping target %s:%d, failed to connect", target.Host, target.Port))
		} else {
			changed = changed || !targetUnchanged
		}
	}

	availableCount := 0

	for _, dev := range up.knownDevices {
		if dev.available {
			availableCount += 1
		}
	}

	up.availableDeviceGauge.Set(float64(availableCount))
	up.attachedDeviceGauge.Set(float64(len(up.attachedDevices)))

	return !changed, err
}

// GetDeviceState always returns healthy.
func (up *USBIPPlugin) GetDeviceState(_ string) string {
	return v1beta1.Healthy
}

// Allocate assigns USB/IP devices to a Pod.
func (up *USBIPPlugin) Allocate(_ context.Context, req *v1beta1.AllocateRequest) (*v1beta1.AllocateResponse, error) {
	up.mu.Lock()
	defer up.mu.Unlock()
	res := &v1beta1.AllocateResponse{
		ContainerResponses: make([]*v1beta1.ContainerAllocateResponse, 0, len(req.ContainerRequests)),
	}
	for _, r := range req.ContainerRequests {
		resp := new(v1beta1.ContainerAllocateResponse)
		for _, id := range r.DevicesIds {
			_, ok := up.knownDevices[id]
			if !ok {
				return nil, fmt.Errorf("requested device does not exist %s", id)
			}
			dev, ok := up.knownDevices[id]
			if !dev.available {
				return nil, fmt.Errorf("requested device %s is not available", id)
			}
		}
		for _, id := range r.DevicesIds {
			dev, _ := up.knownDevices[id]
			attachedDevice, alreadyAttached := up.attachedDevices[id]
			if !alreadyAttached {
				attachedDeviceRef, err := dev.Target.Import(dev.readProperties.BusId)
				if err != nil {
					return nil, err
				}
				attachedDevice = attachedDeviceRef
				up.attachedDevices[id] = attachedDeviceRef
				_ = up.logger.Log("msg", "Attached device", "details", attachedDevice)
			}
			resp.Devices = append(
				resp.Devices,
				&v1beta1.DeviceSpec{
					ContainerPath: attachedDevice.DevMountPath,
					HostPath:      attachedDevice.DevMountPath,
					Permissions:   "mrw",
				},
			)
			for _, extraDev := range dev.ExtraDevices {
				resp.Devices = append(resp.Devices, &extraDev)
			}
		}
		res.ContainerResponses = append(res.ContainerResponses, resp)
	}
	up.allocationsCounter.Add(float64(len(res.ContainerResponses)))
	return res, nil
}

// GetDevicePluginOptions always returns an empty response.
func (up *USBIPPlugin) GetDevicePluginOptions(_ context.Context, _ *v1beta1.Empty) (*v1beta1.DevicePluginOptions, error) {
	return &v1beta1.DevicePluginOptions{}, nil
}

// ListAndWatch lists all devices and then refreshes every deviceCheckInterval.
func (up *USBIPPlugin) ListAndWatch(_ *v1beta1.Empty, stream v1beta1.DevicePlugin_ListAndWatchServer) error {
	_ = level.Info(up.logger).Log("msg", "starting listwatch")
	if _, err := up.refreshDevices(); err != nil {
		return err
	}
	ok := false
	var err error
	for {
		if !ok {
			res := new(v1beta1.ListAndWatchResponse)
			for devId, dev := range up.knownDevices {
				if dev.available {
					res.Devices = append(res.Devices, &v1beta1.Device{ID: devId, Health: v1beta1.Healthy})
				}
			}
			if err := stream.Send(res); err != nil {
				return err
			}
		}
		<-time.After(deviceCheckInterval)
		ok, err = up.refreshDevices()
		if err != nil {
			return err
		}
	}
}

// PreStartContainer always returns an empty response.
func (up *USBIPPlugin) PreStartContainer(_ context.Context, _ *v1beta1.PreStartContainerRequest) (*v1beta1.PreStartContainerResponse, error) {
	return &v1beta1.PreStartContainerResponse{}, nil
}

// GetPreferredAllocation always returns an empty response.
func (up *USBIPPlugin) GetPreferredAllocation(context.Context, *v1beta1.PreferredAllocationRequest) (*v1beta1.PreferredAllocationResponse, error) {
	return &v1beta1.PreferredAllocationResponse{}, nil
}

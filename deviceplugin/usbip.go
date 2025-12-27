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

	"github.com/MatthiasValvekens/usbip-device-plugin/usbip"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	deviceCheckInterval = 5 * time.Second
)

// device wraps the v1.beta1.Device type to add context about
// the device needed by the USBIPPlugin.
type deviceState struct {
	*v1beta1.Device
	deviceSpecs []*v1beta1.DeviceSpec
	mounts      []*v1beta1.Mount
}

type KnownDevice struct {
	Target         usbip.Target `json:"target"`
	Selector       usbip.Device `json:"selector"`
	readProperties usbip.Device
	available      bool
}

type USBIPPlugin struct {
	v1beta1.UnimplementedDevicePluginServer
	knownDevices    map[string]KnownDevice
	attachedDevices map[string]usbip.AttachedDevice
	logger          log.Logger
	mu              sync.Mutex

	// metrics
	deviceGauge        prometheus.Gauge
	allocationsCounter prometheus.Counter
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

func NewPluginForDeviceGroup(knownDevices []*KnownDevice, resourceName string, pluginDir string, logger log.Logger, reg prometheus.Registerer) Plugin {
	if logger == nil {
		logger = log.NewNopLogger()
	}

	devices := make(map[string]KnownDevice)
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
		devices[id] = dev
	}

	p := &USBIPPlugin{
		knownDevices:    devices,
		attachedDevices: map[string]usbip.AttachedDevice{},
		logger:          logger,
		deviceGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "usbip_device_plugin_available_devices",
			Help: "The number of devices managed by this device plugin.",
		}),
		allocationsCounter: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "usbip_device_plugin_allocations_total",
			Help: "The total number of device allocations made by this device plugin.",
		}),
	}

	if reg != nil {
		reg.MustRegister(p.deviceGauge, p.allocationsCounter)
	}

	return NewPlugin(resourceName, pluginDir, p, logger, prometheus.WrapRegistererWithPrefix("generic_", reg))
}

func (up *USBIPPlugin) refreshTarget(target usbip.Target) (bool, error) {
	conn, err := target.Dial()

	if err != nil {
		return true, err
	}

	defer conn.Close()

	lst, err := conn.List()

	changed := false
	for _, kd := range up.knownDevices {
		if kd.Target != target {
			continue
		}

		selector := kd.Selector
		found := false
		for _, cand := range lst {
			if selector.BusId != "" && selector.BusId != cand.BusId {
				continue
			}
			if selector.Vendor != 0 && selector.Vendor != cand.Vendor {
				continue
			}
			if selector.Product != 0 && selector.Product != cand.Product {
				continue
			}
			found = true
			changed = changed || (kd.readProperties != cand)
			kd.readProperties = cand
			break
		}
		wasAvailable := kd.available
		changed = changed || (wasAvailable != found)
		kd.available = found
	}

	return !changed, err
}

// refreshDevices updates the devices available to the
// generic device plugin and returns a boolean indicating
// if everything is OK, i.e. if the devices are the same ones as before.
func (up *USBIPPlugin) refreshDevices() (bool, error) {
	up.mu.Lock()
	defer up.mu.Unlock()
	changed := false
	var err error = nil
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

	up.deviceGauge.Set(float64(availableCount))

	return true, err
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
				attachedDevice = *attachedDeviceRef
			}
			resp.Devices = append(
				resp.Devices,
				&v1beta1.DeviceSpec{
					ContainerPath: attachedDevice.DevMountPath,
					HostPath:      attachedDevice.DevMountPath,
					Permissions:   "mrw",
				},
			)
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

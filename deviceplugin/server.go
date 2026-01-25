// SPDX-License-Identifier: GPL-2.0-only

package deviceplugin

// This project is GPL-2.0, but this file contains code from generic-device-plugin.
// Original license notice below.
//
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
	"fmt"
	"os"
	"time"

	"github.com/MatthiasValvekens/usbip-device-plugin/usbip"
	"github.com/efficientgo/core/errors"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	waitForDevNodesReadyStep = 3 * time.Second
	waitForDevNodesAttempts  = 5
)

type USBIPPlugin struct {
	v1beta1.UnimplementedDevicePluginServer
	resource          string
	selectableDevices map[string]*KnownDevice
	manager           *DeviceManager
	logger            log.Logger
	refreshChan       chan []string

	// metrics
	availableDeviceGauge prometheus.Gauge
	attachedDeviceGauge  prometheus.Gauge
	allocationsCounter   prometheus.Counter
}

func NewPluginForDeviceGroup(deviceIds []string, dm *DeviceManager, resourceName string, pluginDir string, logger log.Logger, reg prometheus.Registerer) Plugin {
	if logger == nil {
		logger = log.NewNopLogger()
	}

	selectableDevices := make(map[string]*KnownDevice, len(deviceIds))
	for _, id := range deviceIds {
		devPtr, ok := dm.knownDevices[id]
		if !ok {
			// should never happen
			panic(fmt.Errorf("device %s not found in known devices", id))
		}
		selectableDevices[id] = devPtr
	}

	p := &USBIPPlugin{
		resource:          resourceName,
		selectableDevices: selectableDevices,
		manager:           dm,
		logger:            logger,
		refreshChan:       make(chan []string),
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
	dm.subscribers = append(dm.subscribers, p.refreshChan)

	_ = logger.Log("msg", "Preparing device plugin...")
	if reg != nil {
		reg.MustRegister(p.availableDeviceGauge, p.allocationsCounter, p.attachedDeviceGauge)
	}

	return NewPlugin(resourceName, pluginDir, p, logger, prometheus.WrapRegistererWithPrefix("usbip_", reg))
}

// GetDeviceState always returns healthy.
func (up *USBIPPlugin) GetDeviceState(_ string) string {
	return v1beta1.Healthy
}

// Allocate assigns USB/IP devices to a Pod.
func (up *USBIPPlugin) Allocate(_ context.Context, req *v1beta1.AllocateRequest) (*v1beta1.AllocateResponse, error) {
	up.manager.mu.Lock()
	defer up.manager.mu.Unlock()
	res := &v1beta1.AllocateResponse{
		ContainerResponses: make([]*v1beta1.ContainerAllocateResponse, 0, len(req.ContainerRequests)),
	}
	for _, r := range req.ContainerRequests {
		resp := new(v1beta1.ContainerAllocateResponse)
		for _, id := range r.DevicesIds {
			dev, ok := up.selectableDevices[id]
			if !ok {
				return nil, fmt.Errorf("requested device does not exist %s", id)
			}
			if !dev.available {
				return nil, fmt.Errorf("requested device %s is not available", id)
			}
		}
		for _, id := range r.DevicesIds {
			dev, _ := up.selectableDevices[id]
			attachedDevice, alreadyAttached := up.manager.attachedDevices[id]
			if !alreadyAttached {
				attachedDeviceRef, err := dev.Target.Import(dev.readProperties.BusId, up.manager.vhciDriver)
				if err != nil {
					return nil, err
				}
				err = waitForDevNodes(dev, attachedDeviceRef)
				if err != nil {
					return nil, err
				}
				attachedDevice = attachedDeviceRef
				up.manager.attachedDevices[id] = attachedDeviceRef
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

func checkDevNodeAvailability(devConfig *KnownDevice, device *usbip.AttachedDevice) error {

	if _, err := os.Stat(device.DevMountPath); err != nil {
		return errors.Wrapf(err, "Main device node at %s not found", err)
	}
	for _, extraDev := range devConfig.ExtraDevices {
		if _, err := os.Stat(extraDev.HostPath); err != nil {
			return errors.Wrapf(err, "Extra device at %s not found", extraDev.HostPath)
		}
	}
	return nil
}

func waitForDevNodes(devConfig *KnownDevice, device *usbip.AttachedDevice) error {
	var latestErr error
	for i := 0; i < waitForDevNodesAttempts; i++ {
		if latestErr = checkDevNodeAvailability(devConfig, device); latestErr == nil {
			break
		}
		time.Sleep(waitForDevNodesReadyStep)
	}

	return latestErr
}

// GetDevicePluginOptions always returns an empty response.
func (up *USBIPPlugin) GetDevicePluginOptions(_ context.Context, _ *v1beta1.Empty) (*v1beta1.DevicePluginOptions, error) {
	return &v1beta1.DevicePluginOptions{}, nil
}

func (up *USBIPPlugin) updateCounters() {
	availableCount := 0
	attachedCount := 0
	for devId, dev := range up.selectableDevices {
		if dev.available {
			availableCount += 1
		}
		_, attached := up.manager.attachedDevices[devId]
		if attached {
			attachedCount += 1
		}
	}

	up.availableDeviceGauge.Set(float64(availableCount))
	up.attachedDeviceGauge.Set(float64(attachedCount))

}

// ListAndWatch lists all devices and then refreshes every deviceCheckInterval.
func (up *USBIPPlugin) ListAndWatch(_ *v1beta1.Empty, stream v1beta1.DevicePlugin_ListAndWatchServer) error {
	_ = level.Info(up.logger).Log("msg", "starting listwatch")
	var changedDevices []string
	changeRelevant := true
	for {
		if changeRelevant {
			up.updateCounters()
			res := new(v1beta1.ListAndWatchResponse)
			for devId, dev := range up.selectableDevices {
				if dev.available {
					res.Devices = append(res.Devices, &v1beta1.Device{ID: devId, Health: v1beta1.Healthy})
				}
			}
			_ = level.Info(up.logger).Log("msg", "emitting device status update")
			if err := stream.Send(res); err != nil {
				return err
			}
		}
		changedDevices = <-up.refreshChan
		changeRelevant = false
		for _, devId := range changedDevices {
			_, ok := up.selectableDevices[devId]
			if ok {
				changeRelevant = true
				break
			}
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

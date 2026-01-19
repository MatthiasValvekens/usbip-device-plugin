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
	"github.com/oklog/run"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	v1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

const (
	deviceCheckInterval = 10 * time.Second
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

type DeviceManager struct {
	vhciDriver         driver.VHCIDriver
	knownDevices       map[string]*KnownDevice
	attachedDevices    map[string]*usbip.AttachedDevice
	podResourcesSocket string
	logger             log.Logger
	mu                 sync.Mutex
	subscribers        []chan []string
}

func NewDeviceManager(podResourcesSocket string, logger log.Logger, vhci driver.VHCIDriver) *DeviceManager {
	if logger == nil {
		logger = log.NewNopLogger()
	}
	return &DeviceManager{
		knownDevices:       make(map[string]*KnownDevice),
		attachedDevices:    make(map[string]*usbip.AttachedDevice),
		podResourcesSocket: podResourcesSocket,
		logger:             logger,
		subscribers:        make([]chan []string, 0),
		vhciDriver:         vhci,
	}
}

func (dm *DeviceManager) Targets() []usbip.Target {
	targetsSeen := map[usbip.Target]bool{}
	targets := make([]usbip.Target, 0)
	for _, dev := range dm.knownDevices {
		_, seen := targetsSeen[dev.Target]
		if !seen {
			targetsSeen[dev.Target] = true
			targets = append(targets, dev.Target)
		}
	}
	return targets
}

func (dm *DeviceManager) Register(resourceName string, knownDevices []*KnownDevice) ([]string, error) {
	devices := dm.knownDevices
	ids := make([]string, len(knownDevices))
	for ix, devPtr := range knownDevices {
		if devPtr == nil {
			continue
		}
		dev := *devPtr
		idJson, err := json.Marshal(dev)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal device %v: %v", dev, err)
		}
		id := fmt.Sprintf("%s_%x", resourceName, sha256.Sum256(idJson))
		ids[ix] = id
		devices[id] = devPtr
	}
	return ids, nil
}

func (dm *DeviceManager) AddRefreshJob(group *run.Group) {
	cancel := make(chan struct{})
	group.Add(
		func() error {
			_ = dm.logger.Log("msg", "starting refresh job")
			for {
				select {
				case <-time.After(deviceCheckInterval):
					_ = level.Debug(dm.logger).Log("msg", "scheduled device refresh...")
					changedDevices, err := dm.refreshDevices()
					if err != nil {
						_ = dm.logger.Log("msg", "error refreshing devices", "err", err)
						return err
					}
					for _, sub := range dm.subscribers {
						// we assume this doesn't block _too_ much
						sub <- changedDevices
					}
				case <-cancel:
					return nil
				}
			}
		},
		func(error) {
			close(cancel)
			for _, sub := range dm.subscribers {
				close(sub)
			}
		},
	)
}

func (dm *DeviceManager) Start() error {
	for i := 0; i < 10; i++ {
		_ = dm.logger.Log("msg", "Refreshing USB/IP devices...")
		if _, err := dm.refreshDevices(); err == nil {
			break
		}
		_ = dm.logger.Log("msg", "Device refresh failed, sleeping for a while...")
		time.Sleep(10 * time.Second)
	}

	_ = dm.logger.Log("msg", "Enumerating attached devices...")
	if err := dm.enumerateAttachedDevices(); err != nil {
		return errors.Wrapf(err, "Failed to enumerate attached devices.")
	}

	_ = dm.logger.Log("msg", "device manager ready")
	return nil
}

func (dm *DeviceManager) refreshTarget(target usbip.Target) ([]string, error) {
	conn, err := target.Dial()

	if err != nil {
		return nil, err
	}

	defer conn.Close()

	lst, err := conn.List()

	changed := make([]string, 0)
	for devId, kd := range dm.knownDevices {
		_, attached := dm.attachedDevices[devId]
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
		devChanged := false
		for _, cand := range lst {
			if !kd.SelectorMatches(cand) {
				continue
			}
			found = true
			devChanged = kd.readProperties != cand
			if devChanged {
				_ = dm.logger.Log("msg", "found device or device changed properties", "target", kd.Target, "selector", selector, "found", cand, "previous", kd.readProperties)
			}
			kd.readProperties = cand
			break
		}
		wasAvailable := kd.available
		if devChanged || (wasAvailable != found) {
			changed = append(changed, devId)
		}
		kd.available = found
		if wasAvailable && !found {
			_ = dm.logger.Log("msg", "previously available device no longer available (in use by another node?)", "target", kd.Target, "selector", selector)
			kd.readProperties = usbip.Device{}
		}
	}

	return changed, err
}

func (dm *DeviceManager) enumerateAttachedDevices() error {
	vhci := dm.vhciDriver

	for _, attachedDev := range vhci.GetDeviceSlots() {
		_ = dm.logger.Log("msg", "attempting to pair attached USB/IP device with known device...", "port", attachedDev.Port, "device", attachedDev)
		dev := usbip.Device{
			Vendor:  usbip.USBID(attachedDev.Description.Vendor),
			Product: usbip.USBID(attachedDev.Description.Product),
			// we intentionally do _not_ set BusId since the bus ID on the remote is not part of the data
			// available to us
			// (TODO: try to figure out if it's somewhere else in sysfs)
			// FIXME this means that device IDs can get desynced between runs, which is bad. May need to store
			//  the remote bus ID ourselves (like the usbip user-space tools do)
			BusId: "",
		}
		found := false
		for devId, kd := range dm.knownDevices {
			if !kd.SelectorMatches(dev) {
				continue
			}
			_ = dm.logger.Log("msg", "attached device matched with known device", "port", attachedDev.Port, "matched", devId)
			var mountPath string
			mountPath, err := usbip.FindDevMountPath(attachedDev.Description)
			if err != nil {
				_ = dm.logger.Log("msg", "failed to find path to device", "port", attachedDev.Port, "matched", devId, "err", err)
				break
			}
			found = true
			dm.attachedDevices[devId] = &usbip.AttachedDevice{
				Device:       dev,
				Target:       kd.Target,
				Port:         attachedDev.Port,
				DevMountPath: mountPath,
			}
			break
		}
		if !found {
			_ = dm.logger.Log("msg", "failed to pair device with config; ignoring...", "port", attachedDev.Port)
		}
	}
	return nil
}

func (dm *DeviceManager) releaseDevices() error {
	if len(dm.attachedDevices) == 0 {
		// nothing to do
		return nil
	}
	conn, err := kubeletClient(dm.podResourcesSocket)
	if err != nil {
		return fmt.Errorf("failed to connect to kubelet: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := v1.NewPodResourcesListerClient(conn)
	usage, err := client.List(context.TODO(), &v1.ListPodResourcesRequest{})
	if err != nil {
		return fmt.Errorf("failed to interrogate kubelet about resource usage: %v", err)
	}
	witnesses := make(map[string]string, len(dm.attachedDevices))
	for _, podResources := range usage.GetPodResources() {
		for _, containerResources := range podResources.GetContainers() {
			for _, containerDevices := range containerResources.GetDevices() {
				for _, devId := range containerDevices.DeviceIds {
					// record the pod of which the container that holds the device is part
					// so we can log it later if it's one of ours
					witnesses[devId] = fmt.Sprintf("%s/%s", podResources.Namespace, podResources.Name)
				}
			}
		}
	}

	toRemove := make([]string, 0, len(witnesses))

	for devId, attachedDevice := range dm.attachedDevices {
		podRef, inUse := witnesses[devId]
		if inUse {
			_ = level.Debug(dm.logger).Log("msg", "device still in use", "devId", devId, "podRef", podRef)
		} else {
			_ = dm.logger.Log("msg", fmt.Sprintf("detaching device %s used", devId))
			err = usbip.Detach(attachedDevice.Port, dm.vhciDriver)
			if err != nil {
				_ = dm.logger.Log("msg", fmt.Sprintf("failed to detach %s", devId), "err", err)
				continue
			}
			toRemove = append(toRemove, devId)
		}
	}

	for _, devId := range toRemove {
		delete(dm.attachedDevices, devId)
	}

	if err != nil {
		return errors.Wrap(err, "There were errors detaching some devices")
	}

	return nil
}

// refreshDevices updates the devices available to the
// USB/IP device plugin and returns a boolean indicating
// if the state is the same as before.
func (dm *DeviceManager) refreshDevices() ([]string, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	changed := make([]string, 0)
	err := dm.releaseDevices()
	if err != nil {
		_ = dm.logger.Log("msg", "failed to release devices", "err", err)
	}
	// even if the release fails, go on
	for _, target := range dm.Targets() {
		var changedForTarget []string
		changedForTarget, err = dm.refreshTarget(target)

		if err != nil {
			_ = dm.logger.Log("warn", fmt.Sprintf("skipping target %s:%d, failed to connect", target.Host, target.Port))
		} else {
			changed = append(changed, changedForTarget...)
		}
	}

	return changed, err
}

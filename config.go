// SPDX-License-Identifier: GPL-2.0-only

package main

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
	"fmt"
	"strings"

	"github.com/MatthiasValvekens/usbip-device-plugin/deviceplugin"
	"github.com/mitchellh/mapstructure"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const defaultDomain = "usbip.dev.mvalvekens.be"

// initConfig defines config flags, config file, and envs
func initConfig() error {
	cfgFile := flag.String("config", "", "Path to the config file.")
	flag.String("domain", defaultDomain, "The domain to use when when declaring devices.")
	flag.String("plugin-directory", v1beta1.DevicePluginPath, "The directory in which to create plugin sockets.")
	flag.String("pod-resources-socket", "/var/lib/kubelet/pod-resources/kubelet.sock", "The path to the kubelet pod-resources socket")
	flag.String("log-level", logLevelInfo, fmt.Sprintf("Log level to use. Possible values: %s", availableLogLevels))
	flag.String("listen", ":8080", "The address at which to listen for health and metrics.")

	flag.Parse()
	if err := viper.BindPFlags(flag.CommandLine); err != nil {
		return fmt.Errorf("failed to bind config: %w", err)
	}

	if *cfgFile != "" {
		viper.SetConfigFile(*cfgFile)
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		viper.AddConfigPath("/etc/usbip-device-plugin/")
		viper.AddConfigPath(".")
	}

	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found; ignore error
		} else {
			// Config file was found but another error was produced
			return fmt.Errorf("failed to read config file: %w", err)
		}
	}

	return nil
}

func getConfiguredDevices() (map[string][]*deviceplugin.KnownDevice, error) {
	resourceDefs := viper.GetStringMap("resources")
	result := make(map[string][]*deviceplugin.KnownDevice)

	for resourceName, groupData := range resourceDefs {
		switch raw := groupData.(type) {
		case []interface{}:
			deviceSpecs := make([]*deviceplugin.KnownDevice, len(raw))
			for i, def := range raw {
				decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
					Result:  &deviceSpecs[i],
					TagName: "json",
				})
				if err != nil {
					return nil, err
				}

				if err := decoder.Decode(def); err != nil {
					return nil, fmt.Errorf("failed to decode device data %q: %w", def, err)
				}
			}
			result[resourceName] = deviceSpecs
		default:
			return nil, fmt.Errorf("failed to decode devices: unexpected type: %T", groupData)
		}
	}
	return result, nil
}

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
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"

	"github.com/MatthiasValvekens/usbip-device-plugin/deviceplugin"
	"github.com/MatthiasValvekens/usbip-device-plugin/driver"
	"github.com/MatthiasValvekens/usbip-device-plugin/usbip"
	"github.com/efficientgo/core/errors"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	logLevelAll   = "all"
	logLevelDebug = "debug"
	logLevelInfo  = "info"
	logLevelWarn  = "warn"
	logLevelError = "error"
	logLevelNone  = "none"
)

var (
	availableLogLevels = strings.Join([]string{
		logLevelAll,
		logLevelDebug,
		logLevelInfo,
		logLevelWarn,
		logLevelError,
		logLevelNone,
	}, ", ")
)

// Main is the principal function for the binary, wrapped only by `main` for convenience.
func Main() error {
	if err := initConfig(); err != nil {
		return err
	}

	domain := viper.GetString("domain")
	if errs := validation.IsDNS1123Subdomain(domain); len(errs) > 0 {
		return fmt.Errorf("failed to parse domain %q: %s", domain, strings.Join(errs, ", "))
	}

	deviceSpecs, err := getConfiguredDevices()
	if err != nil {
		return err
	}
	if len(deviceSpecs) == 0 {
		return fmt.Errorf("at least one device must be specified")
	}

	logger := log.NewJSONLogger(log.NewSyncWriter(os.Stdout))
	logLevel := viper.GetString("log-level")
	switch logLevel {
	case logLevelAll:
		logger = level.NewFilter(logger, level.AllowAll())
	case logLevelDebug:
		logger = level.NewFilter(logger, level.AllowDebug())
	case logLevelInfo:
		logger = level.NewFilter(logger, level.AllowInfo())
	case logLevelWarn:
		logger = level.NewFilter(logger, level.AllowWarn())
	case logLevelError:
		logger = level.NewFilter(logger, level.AllowError())
	case logLevelNone:
		logger = level.NewFilter(logger, level.AllowNone())
	default:
		return fmt.Errorf("log level %v unknown; possible values are: %s", logLevel, availableLogLevels)
	}
	logger = log.With(logger, "ts", log.DefaultTimestampUTC)
	logger = log.With(logger, "caller", log.DefaultCaller)

	r := prometheus.NewRegistry()
	r.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	var g run.Group
	{
		// Run the HTTP server.
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.Handle("/metrics", promhttp.HandlerFor(r, promhttp.HandlerOpts{}))
		listen := viper.GetString("listen")
		l, err := net.Listen("tcp", listen)
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %v", listen, err)
		}

		g.Add(func() error {
			if err := http.Serve(l, mux); err != nil && err != http.ErrServerClosed {
				return fmt.Errorf("server exited unexpectedly: %v", err)
			}
			return nil
		}, func(error) {
			_ = l.Close()
		})
	}

	{
		// Exit gracefully on SIGINT and SIGTERM.
		term := make(chan os.Signal, 1)
		signal.Notify(term, syscall.SIGINT, syscall.SIGTERM)
		cancel := make(chan struct{})
		g.Add(func() error {
			for {
				select {
				case <-term:
					_ = logger.Log("msg", "caught interrupt; gracefully cleaning up; see you next time!")
					return nil
				case <-cancel:
					return nil
				}
			}
		}, func(error) {
			close(cancel)
		})
	}

	idsByResource := make(map[string][]string, len(deviceSpecs))
	pluginPath := viper.GetString("plugin-directory")
	podResourcesSocket := viper.GetString("pod-resources-socket")
	vhci, err := driver.NewLibUdevVHCIDriver()
	if err != nil {
		return errors.Wrap(err, "failed to set up VHCI driver")
	}
	defer vhci.Close()
	dm := deviceplugin.NewDeviceManager(podResourcesSocket, logger, vhci, usbip.NetDialer{})
	for name, devs := range deviceSpecs {
		registeredIds, err := dm.Register(name, devs)
		if err != nil {
			return errors.Wrapf(err, "failed to register devices for %s", name)
		}
		idsByResource[name] = registeredIds
	}
	err = dm.Start()
	if err != nil {
		return errors.Wrapf(err, "error starting device manager")
	}
	dm.AddRefreshJob(&g)

	for name, devIds := range idsByResource {
		ctx, cancel := context.WithCancel(context.Background())
		fullName := path.Join(domain, name)
		p := deviceplugin.NewPluginForDeviceGroup(
			devIds, dm, fullName, pluginPath,
			log.With(logger, "resource", fullName),
			prometheus.WrapRegistererWith(prometheus.Labels{"resource": fullName}, r),
		)
		g.Add(func() error {
			_ = logger.Log("msg", fmt.Sprintf("Starting the usbip-device-plugin for %s.", fullName))
			return p.Run(ctx)
		}, func(error) {
			cancel()
		})
	}

	return g.Run()
}

func main() {
	if err := Main(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Execution failed: %v\n", err)
		os.Exit(1)
	}
}

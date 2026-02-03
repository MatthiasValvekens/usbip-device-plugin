# usbip-device-plugin

A Kubernetes device plugin for USB/IP devices.

## Overview

This repository publishes a Kubernetes device plugin to
assign USB devices exposed over USB/IP to Kubernetes pods.
This allows decoupling workloads that require access to
USB devices from having to run on a particular
physical node that has the relevant devices plugged in.

All nodes on which the plugin is active will always advertise all
configured devices, until a pod requesting a device
is scheduled. As soon as a device is bound to a particular
node, the other nodes will flag it as unavailable.
In other words, this plugin will _not_ allow you to bind
a USB/IP device to multiple nodes simultaneously.
The workload using the device first has to finish running
before the device will become available for selection again.

I use this plugin to manage some testing hardware and a few
home automation devices. Nothing in here has been tested under any
serious kind of load.

## Requirements

 - The plugin is provisioned as a `DaemonSet` running on
   all nodes that will run workloads that make use of
   devices connected over USB/IP.
 - This plugin only works on Linux nodes, and the
   `vhci_hcd` kernel module is required on all nodes
   on which the `DaemonSet` runs.
 - There must be a network route between each
   node to which the `DaemonSet` is deployed and
   the relevant USB/IP host(s).
 - The plugin requires the ability to interface
   with the `vhci_hcd` controller, which effectively
   means that it has the ability to connect arbitrary
   (virtual) USB devices. This is a privileged operation.

## Example deployment

### Daemonset and configuration

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: usbip-device-plugin
  namespace: kube-system
  labels:
    app.kubernetes.io/name: usbip-device-plugin
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: usbip-device-plugin
  template:
    metadata:
      labels:
        app.kubernetes.io/name: usbip-device-plugin
    spec:
      hostNetwork: true
      priorityClassName: system-node-critical
      tolerations:
        - operator: "Exists"
          effect: "NoExecute"
        - operator: "Exists"
          effect: "NoSchedule"
      containers:
        - image: ghcr.io/matthiasvalvekens/usbip-device-plugin@sha256:...
          imagePullPolicy: IfNotPresent
          name: usbip-device-plugin
          resources:
            requests:
              cpu: 50m
              memory: 20Mi
            limits:
              cpu: 50m
              memory: 30Mi
          ports:
            - containerPort: 8080
              name: http
          securityContext:
            privileged: true
          volumeMounts:
            - name: device-plugin
              mountPath: /var/lib/kubelet/device-plugins
            - name: pod-resources
              mountPath: /var/lib/kubelet/pod-resources
            - name: dev
              mountPath: /dev
            - name: config
              mountPath: /etc/usbip-device-plugin
      volumes:
        - name: device-plugin
          hostPath:
            path: /var/lib/kubelet/device-plugins
        - name: pod-resources
          hostPath:
            path: /var/lib/kubelet/pod-resources
            type: Directory
        - name: dev
          hostPath:
            path: /dev
        - name: config
          configMap:
            name: usbip-devices
            optional: false
  updateStrategy:
    type: RollingUpdate
---
kind: ConfigMap
apiVersion: v1
metadata:
  name: usbip-devices
  namespace: kube-system
data:
  config.yaml: |
    resources:
      some-device:
        - target:
            host: usbip.example.com
            port: 3240
          selector:
            vendor: 0x1050
            product: 0x0407
      other-device:
        - target:
            host: usbip.example.com
            port: 3240
          selector:
            vendor: 0x20a0
            product: 0x4230
```

### Requesting a device

In order to request a USB/IP device for a container,
include the following stanza in the resource limits.

```yaml
    limits:
      usbip.dev.mvalvekens.be/some-device: 1
```
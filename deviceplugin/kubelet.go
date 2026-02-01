// SPDX-License-Identifier: Apache-2.0

package deviceplugin

import (
	"fmt"
	"path/filepath"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func kubeletClient(socketPath string) (*grpc.ClientConn, error) {
	return grpc.NewClient(
		fmt.Sprintf("unix://%s", socketPath),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithResolvers(),
	)
}

func kubeletSocketPath(pluginDir string) string {
	return filepath.Join(pluginDir, filepath.Base(v1beta1.KubeletSocket))
}

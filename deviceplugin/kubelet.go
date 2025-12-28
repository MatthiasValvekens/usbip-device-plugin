package deviceplugin

import (
	"context"
	"net"
	"path/filepath"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func kubeletClient(socketPath string) (*grpc.ClientConn, error) {
	return grpc.NewClient(
		socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			d := &net.Dialer{}
			return d.DialContext(ctx, "unix", addr)
		}),
	)
}

func kubeletSocketPath(pluginDir string) string {
	return filepath.Join(pluginDir, filepath.Base(v1beta1.KubeletSocket))
}

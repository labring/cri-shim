package types

import (
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// SealosCriShimSock is the CRI socket the shim listens on.
	SealosCriShimSock         = "/var/run/sealos/cri-shim.sock"
	DefaultImageCRIShimConfig = "/etc/sealos/cri-shim.yaml"
)

type Config struct {
	CRIShimSocket          string
	RuntimeSocket          string
	Timeout                metav1.Duration
	GlobalRegistryAddr     string
	GlobalRegistryUser     string
	GlobalRegistryPassword string
	GlobalRegistryRepo     string
	ContainerdNamespace    string
	PoolSize               int
	Debug                  bool
	Trace                  bool
}

func BindOptions(cmd *cobra.Command) *Config {
	cfg := &Config{}
	cmd.Flags().StringVar(&cfg.RuntimeSocket, "cri-socket", "unix:///var/run/containerd/containerd.sock", "CRI socket path")
	cmd.Flags().StringVar(&cfg.CRIShimSocket, "shim-socket", "/var/run/sealos/cri-shim.sock", "CRI shim socket path")
	cmd.Flags().StringVar(&cfg.GlobalRegistryAddr, "global-registry-addr", "docker.io", "Global registry address")
	cmd.Flags().StringVar(&cfg.GlobalRegistryUser, "global-registry-user", "", "Global registry username")
	cmd.Flags().StringVar(&cfg.GlobalRegistryPassword, "global-registry-password", "", "Global registry password")
	cmd.Flags().StringVar(&cfg.GlobalRegistryRepo, "global-registry-repository", "", "Global registry repository")
	cmd.Flags().StringVar(&cfg.ContainerdNamespace, "containerd-namespace", "k8s.io", "Containerd namespace")
	cmd.Flags().IntVar(&cfg.PoolSize, "pool-size", 100, "Pool size")
	cmd.Flags().BoolVar(&cfg.Debug, "debug", false, "enable debug logging")
	cmd.Flags().BoolVar(&cfg.Trace, "trace", false, "enable pprof to trace")
	return cfg
}

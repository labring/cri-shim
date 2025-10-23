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
	ContainerdRoot         string
	MetricsConfig          MetricsConfig
}

type MetricsConfig struct {
	Metric       bool
	Endpoint     string
	IngestPath   string
	IsSecure     bool
	PushInterval int
	JobName      string
	Instance     string
}

func BindOptions(cmd *cobra.Command) *Config {
	cfg := &Config{}
	cmd.Flags().StringVar(&cfg.ContainerdRoot, "containerd-root", "/var/lib/containerd", "Containerd root path")
	cmd.Flags().StringVar(&cfg.RuntimeSocket, "cri-socket", "unix:///var/run/containerd/containerd.sock", "CRI socket path")
	cmd.Flags().StringVar(&cfg.CRIShimSocket, "shim-socket", "/var/run/sealos/cri-shim.sock", "CRI shim socket path")
	cmd.Flags().StringVar(&cfg.GlobalRegistryAddr, "global-registry-addr", "docker.io", "Global registry address")
	cmd.Flags().StringVar(&cfg.GlobalRegistryUser, "global-registry-user", "", "Global registry username")
	cmd.Flags().StringVar(&cfg.GlobalRegistryPassword, "global-registry-password", "", "Global registry password")
	cmd.Flags().StringVar(&cfg.GlobalRegistryRepo, "global-registry-repository", "", "Global registry repository")
	cmd.Flags().StringVar(&cfg.ContainerdNamespace, "containerd-namespace", "k8s.io", "Containerd namespace")
	cmd.Flags().IntVar(&cfg.PoolSize, "pool-size", 10, "Pool size")
	cmd.Flags().BoolVar(&cfg.Debug, "debug", false, "enable debug logging")
	cmd.Flags().BoolVar(&cfg.Trace, "trace", false, "enable pprof to trace")
	cmd.Flags().BoolVar(&cfg.MetricsConfig.Metric, "metric", false, "enable otel to metric")
	cmd.Flags().StringVar(&cfg.MetricsConfig.Endpoint, "metric-endpoint", "localhost:8428", "VictoriaMetrics endpoint - host:port")
	cmd.Flags().StringVar(&cfg.MetricsConfig.IngestPath, "metric-ingestPath", "/opentelemetry/api/v1/push", "url path for ingestion path")
	cmd.Flags().BoolVar(&cfg.MetricsConfig.IsSecure, "metric-isSecure", false, "enables https connection for metrics push")
	cmd.Flags().IntVar(&cfg.MetricsConfig.PushInterval, "metric-pushInterval", 5, "how often push samples, aka scrapeInterval at pull model")
	cmd.Flags().StringVar(&cfg.MetricsConfig.JobName, "metric-jobName", "cri-shim", "job name for web-application")
	cmd.Flags().StringVar(&cfg.MetricsConfig.Instance, "metric-instance", "localhost", "hostname of web-application instance")
	return cfg
}

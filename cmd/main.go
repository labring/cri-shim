package main

import (
	"flag"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	imageutil "github.com/labring/cri-shim/pkg/image"
	"github.com/labring/cri-shim/pkg/server"
)

var criSocket, shimSocket, globalRegistryAddr, globalRegistryUser, globalRegistryPassword, globalRegistryRepo, containerdNamespace string
var poolSize int
var debug, trace bool

func main() {
	flag.StringVar(&criSocket, "cri-socket", "unix:///var/run/containerd/containerd.sock", "CRI socket path")
	flag.StringVar(&shimSocket, "shim-socket", "/var/run/sealos/cri-shim.sock", "CRI shim socket path")
	flag.StringVar(&globalRegistryAddr, "global-registry-addr", "docker.io", "Global registry address")
	flag.StringVar(&globalRegistryUser, "global-registry-user", "", "Global registry username")
	flag.StringVar(&globalRegistryPassword, "global-registry-password", "", "Global registry password")
	flag.StringVar(&globalRegistryRepo, "global-registry-repository", "", "Global registry repository")
	flag.StringVar(&containerdNamespace, "containerd-namespace", "k8s.io", "Containerd namespace")
	flag.IntVar(&poolSize, "pool-size", 100, "Pool size")

	flag.BoolVar(&debug, "debug", false, "enable debug logging")
	flag.BoolVar(&trace, "trace", false, "enable pprof to trace")
	flag.Parse()

	s, err := server.New(
		server.Options{
			Timeout:             time.Minute * 5,
			ShimSocket:          shimSocket,
			CRISocket:           criSocket,
			ContainerdNamespace: containerdNamespace,
			PoolSize:            poolSize,
		},
		imageutil.RegistryOptions{
			RegistryAddr: globalRegistryAddr,
			UserName:     globalRegistryUser,
			Password:     globalRegistryPassword,
			Repository:   globalRegistryRepo,
		})
	if debug {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}
	if err != nil {
		slog.Error("failed to create server", err)
		return
	}
	err = s.Start()
	if err != nil {
		slog.Error("failed to start server", err)
		return
	}
	slog.Info("server started")

	if trace {
		go func() {
			err = http.ListenAndServe(":8090", nil)
			if err != nil {
				slog.Error("pprof server started error", err)
				os.Exit(1)
			}
		}()
	}

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	stopCh := make(chan struct{}, 1)
	select {
	case <-signalCh:
		close(stopCh)
	case <-stopCh:
	}
	_ = os.Remove(shimSocket)
	slog.Info("shutting down the image_shim")
	s.Stop()

}

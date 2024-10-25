package app

import (
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	imageutil "github.com/labring/cri-shim/pkg/image"
	"github.com/labring/cri-shim/pkg/server"
	"github.com/labring/cri-shim/pkg/types"
	"github.com/spf13/cobra"
)

var cfg *types.Config

func newServerCmd() *cobra.Command {
	var serverCmd = &cobra.Command{
		Use:   "server",
		Short: "start the cri-shim server",
		Run: func(cmd *cobra.Command, args []string) {
			run(cfg)
		},
	}
	cfg = types.BindOptions(serverCmd)
	return serverCmd
}

func run(cfg *types.Config) {
	s, err := server.New(
		server.Options{
			Timeout:             time.Minute * 5,
			ShimSocket:          cfg.CRIShimSocket,
			CRISocket:           cfg.RuntimeSocket,
			ContainerdNamespace: cfg.ContainerdNamespace,
			PoolSize:            cfg.PoolSize,
		},
		imageutil.RegistryOptions{
			RegistryAddr: cfg.GlobalRegistryAddr,
			UserName:     cfg.GlobalRegistryUser,
			Password:     cfg.GlobalRegistryPassword,
			Repository:   cfg.GlobalRegistryRepo,
		})
	if cfg.Debug {
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

	s.Init()

	if cfg.Trace {
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
	_ = os.Remove(cfg.CRIShimSocket)
	slog.Info("shutting down the image_shim")
	s.Stop()
}

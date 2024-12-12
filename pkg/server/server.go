package server

import (
	"context"
	"go.opentelemetry.io/otel/metric"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/labring/cri-shim/pkg/container"
	imageutil "github.com/labring/cri-shim/pkg/image"
	netutil "github.com/labring/cri-shim/pkg/net"
	"github.com/labring/cri-shim/pkg/types"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type Options struct {
	Timeout             time.Duration
	ShimSocket          string
	ContainerdNamespace string

	CRISocket string
	// User is the user ID for our gRPC socket.
	User int
	// Group is the group ID for our gRPC socket.
	Group int
	// Mode is the permission mode bits for our gRPC socket.
	Mode os.FileMode
	// PoolSize is the size of the pool of goroutines.
	PoolSize int

	MetricFlag bool
}

type Server struct {
	options               Options
	globalRegistryOptions imageutil.RegistryOptions

	containerdClient *containerd.Client
	client           runtimeapi.RuntimeServiceClient
	server           *grpc.Server
	listener         net.Listener
	bufListener      *bufconn.Listener
	imageClient      imageutil.ImageInterface
	MetricClient     metric.Meter

	pool *Pool
}

func New(options Options, registryOptions imageutil.RegistryOptions) (*Server, error) {
	listener, err := net.Listen("unix", options.ShimSocket)
	if err != nil {
		return nil, err
	}
	server := grpc.NewServer()

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0666)
	if err != nil {
		panic(err)
	}

	imageClient, err := imageutil.NewImageInterface(options.ContainerdNamespace, options.CRISocket, devNull)
	if err != nil {
		return nil, err
	}

	s := &Server{
		server:      server,
		listener:    listener,
		imageClient: imageClient,

		options:               options,
		globalRegistryOptions: registryOptions,
	}

	if s.pool, err = NewPool(options.PoolSize, s.client, s.CommitContainer); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) Start() error {
	conn, err := grpc.NewClient(s.options.CRISocket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	s.client = runtimeapi.NewRuntimeServiceClient(conn)
	runtimeapi.RegisterRuntimeServiceServer(s.server, s)

	s.containerdClient, err = containerd.NewWithConn(conn, containerd.WithDefaultNamespace(s.options.ContainerdNamespace))
	if err != nil {
		return err
	}

	// do serve after client is created and registered
	go func() {
		_ = s.server.Serve(s.listener)
	}()

	go func() {
		_ = s.PoolStatus()
	}()

	return netutil.WaitForServer(s.options.ShimSocket, time.Second)
}

func (s *Server) PoolStatus() error {
	for {
		select {
		case <-time.After(20 * time.Second):
			s.pool.mutex.Lock()
			slog.Info("Pool status", "finished containers", s.pool.CommitStatusMap)
			slog.Info("Pool status", "containers state", s.pool.containerStateMap)
			slog.Info("Pool status", "pool worker num", int64(s.pool.pool.Running()))

			queLens := make(map[string]int)
			for containerID, queue := range s.pool.queues {
				queLens[containerID] = len(queue)
			}

			if s.options.MetricFlag {
				ctx := context.Background()
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				allocGauge, _ := s.MetricClient.Float64Gauge("cri_shim_memory_alloc")
				totalGauge, _ := s.MetricClient.Float64Gauge("cri_shim_memory_total")
				heapGauge, _ := s.MetricClient.Float64Gauge("cri_shim_memory_heap")

				allocGauge.Record(ctx, float64(m.Alloc))
				totalGauge.Record(ctx, float64(m.TotalAlloc))
				heapGauge.Record(ctx, float64(m.HeapAlloc))

				if gauge, err := s.MetricClient.Int64Gauge("cri_shim_pool_goroutine_num", metric.WithDescription("The number of running pool")); err != nil {
					slog.Debug("failed to get gauge of cri_shim_pool_goroutine_num", "error", err)
				} else {
					gauge.Record(ctx, int64(s.pool.pool.Running()))
				}
			}

			slog.Info("Pool status", "containers commit queues length", queLens)
			s.pool.mutex.Unlock()
		}
	}
}

func (s *Server) Stop() {
	s.server.Stop()
	s.listener.Close()
	s.imageClient.Stop()
	s.pool.Close()
}

func (s *Server) Version(ctx context.Context, request *runtimeapi.VersionRequest) (*runtimeapi.VersionResponse, error) {
	slog.Debug("Doing version request", "request", request)
	resp, err := s.client.Version(ctx, request)
	if err != nil {
		slog.Error("failed to get version", "error", err)
		return resp, err
	}
	slog.Debug("Got version response", "response", resp)
	return resp, err
}

func (s *Server) RunPodSandbox(ctx context.Context, request *runtimeapi.RunPodSandboxRequest) (*runtimeapi.RunPodSandboxResponse, error) {
	slog.Debug("Doing run pod sandbox request", "request", request)
	return s.client.RunPodSandbox(ctx, request)
}

func (s *Server) StopPodSandbox(ctx context.Context, request *runtimeapi.StopPodSandboxRequest) (*runtimeapi.StopPodSandboxResponse, error) {
	slog.Debug("Doing stop pod sandbox request", "request", request)
	return s.client.StopPodSandbox(ctx, request)
}

func (s *Server) RemovePodSandbox(ctx context.Context, request *runtimeapi.RemovePodSandboxRequest) (*runtimeapi.RemovePodSandboxResponse, error) {
	slog.Debug("Doing remove pod sandbox request", "request", request)
	return s.client.RemovePodSandbox(ctx, request)
}

func (s *Server) PodSandboxStatus(ctx context.Context, request *runtimeapi.PodSandboxStatusRequest) (*runtimeapi.PodSandboxStatusResponse, error) {
	slog.Debug("Doing pod sandbox status request", "request", request)
	return s.client.PodSandboxStatus(ctx, request)
}

func (s *Server) ListPodSandbox(ctx context.Context, request *runtimeapi.ListPodSandboxRequest) (*runtimeapi.ListPodSandboxResponse, error) {
	slog.Debug("Doing list pod sandbox request", "request", request)
	return s.client.ListPodSandbox(ctx, request)
}

func (s *Server) CreateContainer(ctx context.Context, request *runtimeapi.CreateContainerRequest) (*runtimeapi.CreateContainerResponse, error) {
	slog.Debug("Doing create container request", "request", request)
	return s.client.CreateContainer(ctx, request)
}

func (s *Server) StartContainer(ctx context.Context, request *runtimeapi.StartContainerRequest) (*runtimeapi.StartContainerResponse, error) {
	slog.Debug("Doing start container request", "request", request)
	return s.client.StartContainer(ctx, request)
}

func (s *Server) StopContainer(ctx context.Context, request *runtimeapi.StopContainerRequest) (*runtimeapi.StopContainerResponse, error) {
	// todo check container env and create commit
	slog.Info("Doing stop container request", "request", request)
	_, info, err := s.GetContainerInfo(ctx, request.ContainerId)
	if err != nil {
		return nil, err
	}
	if info.CommitEnabled {
		slog.Info("commit flag found when doing stop container request", "container id", request.ContainerId)
		s.pool.SubmitTask(types.Task{
			Kind:        types.KindStop,
			ContainerID: request.ContainerId,
		})
	}
	return s.client.StopContainer(ctx, request)
}

func (s *Server) RemoveContainer(ctx context.Context, request *runtimeapi.RemoveContainerRequest) (*runtimeapi.RemoveContainerResponse, error) {
	slog.Info("Doing remove container request", "request", request)

	resp, err := s.client.ContainerStatus(ctx, &runtimeapi.ContainerStatusRequest{
		ContainerId: request.ContainerId,
		Verbose:     false,
	})
	if err != nil {
		slog.Error("failed to get container status", "error", err)
		return nil, err
	}
	if resp.Status.State == runtimeapi.ContainerState_CONTAINER_UNKNOWN {
		slog.Error("unknown container", "container id", request.ContainerId)
		return s.client.RemoveContainer(ctx, request)
	}

	_, info, err := s.GetContainerInfo(ctx, request.ContainerId)
	if err != nil {
		return nil, err
	}
	if info.CommitEnabled {
		slog.Info("commit flag found when doing remove container request", "container id", request.ContainerId)
		s.pool.SubmitTask(types.Task{
			Kind:        types.KindRemove,
			ContainerID: request.ContainerId,
		})
		return nil, nil
	}
	return s.client.RemoveContainer(ctx, request)
}

func (s *Server) ListContainers(ctx context.Context, request *runtimeapi.ListContainersRequest) (*runtimeapi.ListContainersResponse, error) {
	slog.Debug("Doing list containers request", "request", request)
	return s.client.ListContainers(ctx, request)
}

func (s *Server) ContainerStatus(ctx context.Context, request *runtimeapi.ContainerStatusRequest) (*runtimeapi.ContainerStatusResponse, error) {
	slog.Debug("Doing container status request", "request", request)
	_, info, err := s.GetContainerInfo(ctx, request.ContainerId)
	if err != nil {
		slog.Error("failed to get container env", "error", err)
		return s.client.ContainerStatus(ctx, request)
	}
	resp, err := s.client.ContainerStatus(ctx, request)
	if info.CommitEnabled {
		slog.Debug("commit flag found when doing container status request", "container id", request.ContainerId)
		s.pool.SubmitTask(types.Task{
			Kind:           types.KindStatus,
			ContainerID:    request.ContainerId,
			ContainerState: resp.Status.State,
		})
	}
	return resp, err
}

func (s *Server) UpdateContainerResources(ctx context.Context, request *runtimeapi.UpdateContainerResourcesRequest) (*runtimeapi.UpdateContainerResourcesResponse, error) {
	slog.Debug("Doing update container resources request", "request", request)
	return s.client.UpdateContainerResources(ctx, request)
}

func (s *Server) ReopenContainerLog(ctx context.Context, request *runtimeapi.ReopenContainerLogRequest) (*runtimeapi.ReopenContainerLogResponse, error) {
	slog.Debug("Doing reopen container log request", "request", request)
	return s.client.ReopenContainerLog(ctx, request)
}

func (s *Server) ExecSync(ctx context.Context, request *runtimeapi.ExecSyncRequest) (*runtimeapi.ExecSyncResponse, error) {
	slog.Debug("Doing exec sync request", "request", request)
	return s.client.ExecSync(ctx, request)
}

func (s *Server) Exec(ctx context.Context, request *runtimeapi.ExecRequest) (*runtimeapi.ExecResponse, error) {
	slog.Debug("Doing exec request", "request", request)
	return s.client.Exec(ctx, request)
}

func (s *Server) Attach(ctx context.Context, request *runtimeapi.AttachRequest) (*runtimeapi.AttachResponse, error) {
	slog.Debug("Doing attach request", "request", request)
	return s.client.Attach(ctx, request)
}

func (s *Server) PortForward(ctx context.Context, request *runtimeapi.PortForwardRequest) (*runtimeapi.PortForwardResponse, error) {
	slog.Debug("Doing port forward request", "request", request)
	return s.client.PortForward(ctx, request)
}

func (s *Server) ContainerStats(ctx context.Context, request *runtimeapi.ContainerStatsRequest) (*runtimeapi.ContainerStatsResponse, error) {
	slog.Debug("Doing container stats request", "request", request)
	return s.client.ContainerStats(ctx, request)
}

func (s *Server) ListContainerStats(ctx context.Context, request *runtimeapi.ListContainerStatsRequest) (*runtimeapi.ListContainerStatsResponse, error) {
	slog.Debug("Doing list container stats request", "request", request)
	return s.client.ListContainerStats(ctx, request)
}

func (s *Server) PodSandboxStats(ctx context.Context, request *runtimeapi.PodSandboxStatsRequest) (*runtimeapi.PodSandboxStatsResponse, error) {
	slog.Debug("Doing pod sandbox stats request", "request", request)
	return s.client.PodSandboxStats(ctx, request)
}

func (s *Server) ListPodSandboxStats(ctx context.Context, request *runtimeapi.ListPodSandboxStatsRequest) (*runtimeapi.ListPodSandboxStatsResponse, error) {
	slog.Debug("Doing list pod sandbox stats request", "request", request)
	return s.client.ListPodSandboxStats(ctx, request)
}

func (s *Server) UpdateRuntimeConfig(ctx context.Context, request *runtimeapi.UpdateRuntimeConfigRequest) (*runtimeapi.UpdateRuntimeConfigResponse, error) {
	slog.Debug("Doing update runtime config request", "request", request)
	return s.client.UpdateRuntimeConfig(ctx, request)
}

func (s *Server) Status(ctx context.Context, request *runtimeapi.StatusRequest) (*runtimeapi.StatusResponse, error) {
	slog.Debug("Doing status request", "request", request)
	return s.client.Status(ctx, request)
}

func (s *Server) CheckpointContainer(ctx context.Context, request *runtimeapi.CheckpointContainerRequest) (*runtimeapi.CheckpointContainerResponse, error) {
	slog.Debug("Doing checkpoint container request", "request", request)
	return s.client.CheckpointContainer(ctx, request)
}

func (s *Server) GetContainerEvents(request *runtimeapi.GetEventsRequest, server runtimeapi.RuntimeService_GetContainerEventsServer) error {
	slog.Debug("Doing get container events request", "request", request)
	client, err := s.client.GetContainerEvents(context.Background(), request)
	if err != nil {
		return err
	}
	if res, err := client.Recv(); err != nil {
		return err
	} else {
		return server.Send(res)
	}
}

func (s *Server) ListMetricDescriptors(ctx context.Context, request *runtimeapi.ListMetricDescriptorsRequest) (*runtimeapi.ListMetricDescriptorsResponse, error) {
	slog.Debug("Doing list metric descriptors request", "request", request)
	return s.client.ListMetricDescriptors(ctx, request)
}

func (s *Server) ListPodSandboxMetrics(ctx context.Context, request *runtimeapi.ListPodSandboxMetricsRequest) (*runtimeapi.ListPodSandboxMetricsResponse, error) {
	slog.Debug("Doing list pod sandbox metrics request", "request", request)
	return s.client.ListPodSandboxMetrics(ctx, request)
}

func (s *Server) RuntimeConfig(ctx context.Context, request *runtimeapi.RuntimeConfigRequest) (*runtimeapi.RuntimeConfigResponse, error) {
	slog.Debug("Doing runtime config request", "request", request)
	return s.client.RuntimeConfig(ctx, request)
}

func (s *Server) CommitContainer(task types.Task) error {
	slog.Info("Doing commit container task", "task", task)

	defer s.pool.ContainerCommittingLock[task.ContainerID].Unlock()

	start := time.Now()
	commitFlag := true
	if status, exists := s.pool.CommitStatusMap[task.ContainerID]; exists {
		commitFlag = types.StopCommit != status
	}
	ctx, _ := context.WithTimeout(context.Background(), 20*time.Minute)

	if commitFlag {
		statusReq := &runtimeapi.ContainerStatusRequest{
			ContainerId: task.ContainerID,
			Verbose:     true,
		}

		statusResp, err := s.client.ContainerStatus(ctx, statusReq)
		if err != nil {
			slog.Error("failed to get container status", "error", err)
			return err
		}

		registry, info, err := s.GetContainerInfo(ctx, task.ContainerID)

		if err != nil {
			slog.Error("failed to get container env", "error", err)
			return err
		}

		ctx = namespaces.WithNamespace(ctx, s.options.ContainerdNamespace)
		imageName := registry.GetImageRef(info.CommitImage)
		initialImageName := imageName + "-initial"

		if registry.UserName == "" {
			registry.UserName = s.globalRegistryOptions.UserName
		}
		if registry.Password == "" {
			registry.Password = s.globalRegistryOptions.Password
		}
		if err = s.imageClient.Pull(ctx, info.ImageRef, registry.UserName, registry.Password); err != nil {
			slog.Error("failed to pull image", "image name", imageName, "error", err)
		}

		if err := s.imageClient.Commit(ctx, initialImageName, statusResp.Status.Id, false); err != nil {
			slog.Error("failed to commit container after retries", "containerId", statusResp.Status.Id, "image name", initialImageName, "error", err)
			s.pool.SetCommitStatus(task.ContainerID, types.ErrorCommit)
			if s.options.MetricFlag {
				if counter, err := s.MetricClient.Int64Counter("cri_shim_error_commit_counter", metric.WithDescription("The number of error commit")); err != nil {
					slog.Debug("failed to get counter of cri_shim_error_commit_counter", "error", err)
				} else {
					counter.Add(ctx, 1)
				}
			}
			return err
		}
		slog.Info("commit container time", "containerId", statusResp.Status.Id, "time", time.Since(start).Seconds())

		defer s.imageClient.Remove(ctx, initialImageName, false, false)

		if info.SquashEnabled {
			if err = s.imageClient.Squash(ctx, initialImageName, imageName); err != nil {
				slog.Error("failed to squash image", "image name", imageName, "error", err)
				s.pool.SetCommitStatus(task.ContainerID, types.ErrorCommit)
				return err
			}
		} else {
			if err = s.imageClient.Tag(ctx, initialImageName, imageName); err != nil {
				slog.Error("failed to tag image", "image name", imageName, "error", err)
				s.pool.SetCommitStatus(task.ContainerID, types.ErrorCommit)
				return err
			}
		}

		if info.PushEnabled {
			if err = s.imageClient.Push(ctx, imageName, registry.UserName, registry.Password); err != nil {
				slog.Error("failed to push container", "error", err, "image name", imageName)
				return err
			}
			slog.Info("pushed image time", "image name", imageName, "time", time.Since(start).Seconds())
		} else {
			slog.Debug("did not push container", "image name", imageName)
		}
		if s.options.MetricFlag {
			s.MetricCommit(start, ctx)
		}
	}

	switch task.Kind {
	case types.KindRemove:
		s.pool.ClearTasks(task.ContainerID)
		// do remove container request, ignore error
		_, _ = s.client.RemoveContainer(ctx, &runtimeapi.RemoveContainerRequest{ContainerId: task.ContainerID})
	case types.KindStop:
		s.pool.SetCommitStatus(task.ContainerID, types.StopCommit)
	case types.KindStatus:
		s.pool.SetCommitStatus(task.ContainerID, types.StatusCommit)
	}

	return nil
}

func (s *Server) Init() {
	slog.Info("Start to add container state to pool")
	ctx := context.Background()
	request := &runtimeapi.ListContainerStatsRequest{}
	list, err := s.client.ListContainerStats(ctx, request)
	if err != nil {
		slog.Error("failed to list container stats", "error", err)
	}
	for _, i := range list.Stats {
		_, info, err := s.GetContainerInfo(ctx, i.Attributes.Id)
		if err != nil {
			slog.Error("failed to get container env", "error", err)
		}
		if info.CommitEnabled {
			s.pool.containerStateMap[i.Attributes.Id] = runtimeapi.ContainerState_CONTAINER_RUNNING
		}
	}
}

func (s *Server) GetContainerInfo(ctx context.Context, containerID string) (registry *imageutil.Registry, info *container.Info, err error) {
	registry = &imageutil.Registry{}
	info = &container.Info{}

	ctx = namespaces.WithNamespace(ctx, s.options.ContainerdNamespace)

	c, err := s.containerdClient.LoadContainer(ctx, containerID)
	if err != nil {
		return registry, info, err
	}
	spec, err := c.Spec(ctx)
	if err != nil {
		return nil, nil, err
	}
	containerInfo, err := c.Info(ctx)
	if err != nil {
		return registry, info, err
	}
	info.ImageRef = containerInfo.Image

	slog.Debug("Got container info env", "info env", spec.Process.Env)
	for _, env := range spec.Process.Env {
		kv := strings.SplitN(env, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case types.ImageRegistryAddressOnEnv:
			registry.RegistryAddr = kv[1]
		case types.ImageRegistryUserNameOnEnv:
			registry.UserName = kv[1]
		case types.ImageRegistryPasswordOnEnv:
			registry.Password = kv[1]
		case types.ImageRegistryRepositoryOnEnv:
			registry.Repository = kv[1]
		case types.SealosUsernameOnEnv:
			registry.SealosUsername = kv[1]
		case types.ImageSquashOnEnv:
			info.SquashEnabled = kv[1] == types.ImageSquashOnEnvEnableValue
		case types.ImageNameOnEnv:
			info.CommitImage = kv[1]
		case types.ContainerCommitOnStopEnvFlag:
			info.CommitEnabled = kv[1] == types.ContainerCommitOnStopEnvEnableValue
		}

		info.PushEnabled = true
		if registry.UserName != "" && registry.Password == "" {
			info.PushEnabled = false
		}
	}
	return registry, info, nil

}

func (s *Server) MetricCommit(start time.Time, ctx context.Context) {
	if counter, err := s.MetricClient.Int64Counter("cri_shim_commit_counter", metric.WithDescription("The number of commit container")); err != nil {
		slog.Debug("failed to get counter of cri_shim_commit_counter", "error", err)
	} else {
		counter.Add(ctx, 1)
	}

	if histogram, err := s.MetricClient.Float64Histogram(
		"cri_shim_commit_duration",
		metric.WithDescription("a histogram for commit time"),
		metric.WithExplicitBucketBoundaries(0, 20, 40, 60, 80, 100, 120, 150, 200, 300),
	); err != nil {
		slog.Debug("failed to get histogram of cri_shim_commit_duration", "error", err)
	} else {
		histogram.Record(ctx, time.Since(start).Seconds())
	}
}

package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/labring/cri-shim/pkg/container"
	errutil "github.com/labring/cri-shim/pkg/errors"
	imageutil "github.com/labring/cri-shim/pkg/image"
	netutil "github.com/labring/cri-shim/pkg/net"
	"github.com/labring/cri-shim/pkg/types"

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
}

type Server struct {
	options               Options
	globalRegistryOptions imageutil.RegistryOptions

	client      runtimeapi.RuntimeServiceClient
	server      *grpc.Server
	listener    net.Listener
	bufListener *bufconn.Listener
	imageClient imageutil.ImageInterface

	pool *Pool
}

func New(options Options, registryOptions imageutil.RegistryOptions) (*Server, error) {
	listener, err := net.Listen("unix", options.ShimSocket)
	if err != nil {
		return nil, err
	}
	server := grpc.NewServer()

	imageClient, err := imageutil.NewImageInterface(options.ContainerdNamespace, options.CRISocket, os.Stdout)
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

	// do serve after client is created and registered
	go func() {
		_ = s.server.Serve(s.listener)
	}()
	return netutil.WaitForServer(s.options.ShimSocket, time.Second)
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
	commitFlag, err := s.CheckCommitFlag(ctx, request.ContainerId)
	if err != nil {
		return nil, err
	}
	if commitFlag {
		s.pool.Manager.Set(request.ContainerId)
		s.pool.SubmitTask(types.Task{
			Ctx:         ctx,
			ContainerID: request.ContainerId,
			RemoveFlag:  true,
		})
	}
	return s.client.StopContainer(ctx, request)
}

func (s *Server) RemoveContainer(ctx context.Context, request *runtimeapi.RemoveContainerRequest) (*runtimeapi.RemoveContainerResponse, error) {
	slog.Info("Doing remove container request", "request", request)
	commitFlag, err := s.CheckCommitFlag(ctx, request.ContainerId)
	if err != nil {
		return nil, err
	}

	if commitFlag {
		s.pool.ClearTasks(request.ContainerId)

		if _, exist := s.pool.Manager.Get(request.ContainerId); exist {
			if err = s.pool.Manager.WaitForCommit(request.ContainerId, ctx); err != nil {
				slog.Error("Error happen when container commit", "error", err)
				return nil, err
			}
		} else {
			s.pool.Manager.Set(request.ContainerId)
			s.pool.SubmitTask(types.Task{
				Ctx:         ctx,
				ContainerID: request.ContainerId,
				RemoveFlag:  true,
			})
			if err = s.pool.Manager.WaitForCommit(request.ContainerId, ctx); err != nil {
				slog.Error("Error happen when container commit", "error", err)
				return nil, err
			}
		}

		return s.pool.Remove(ctx, request)
	}

	return s.client.RemoveContainer(ctx, request)
}

func (s *Server) ListContainers(ctx context.Context, request *runtimeapi.ListContainersRequest) (*runtimeapi.ListContainersResponse, error) {
	slog.Debug("Doing list containers request", "request", request)
	return s.client.ListContainers(ctx, request)
}

func (s *Server) ContainerStatus(ctx context.Context, request *runtimeapi.ContainerStatusRequest) (*runtimeapi.ContainerStatusResponse, error) {
	slog.Debug("Doing container status request", "request", request)
	request.Verbose = true
	resp, err := s.client.ContainerStatus(ctx, request)
	if err != nil {
		slog.Error("failed to get container status", "error", err)
		return resp, err
	}
	_, _, commitFlag, _, err := s.GetInfoFromContainerEnv(resp)
	if err != nil {
		slog.Error("failed to get container env", "error", err)
		return resp, err
	}

	if commitFlag {
		s.pool.SubmitTask(types.Task{
			Ctx:         ctx,
			ContainerID: request.ContainerId,
			RemoveFlag:  false,
		})
	}

	slog.Debug("Got container status response", "response", resp)
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
	statusReq := &runtimeapi.ContainerStatusRequest{
		ContainerId: task.ContainerID,
		Verbose:     true,
	}

	statusResp, err := s.client.ContainerStatus(task.Ctx, statusReq)
	if err != nil {
		slog.Error("failed to get container status", "error", err)
		return err
	}

	registry, imageRef, commitFlag, pushFlag, err := s.GetInfoFromContainerEnv(statusResp)

	//commit image
	if commitFlag && err == nil {

		if _, exist := s.pool.Manager.Get(task.ContainerID); exist {
			return nil
		}

		pauseFlag := statusResp.Status.State == runtimeapi.ContainerState_CONTAINER_RUNNING

		ctx := namespaces.WithNamespace(task.Ctx, s.options.ContainerdNamespace)

		imageName := registry.GetImageRef(imageRef)

		const maxRetries = 5
		const retryDelay = time.Second * 2

		for i := 0; i < maxRetries; i++ {
			if err = s.imageClient.Commit(ctx, imageName, statusResp.Status.Id, pauseFlag); err == nil {
				break
			}
			slog.Error("failed to commit container", "attempt", i+1, "error", err)
			time.Sleep(retryDelay)
		}

		if err != nil {
			slog.Error("failed to commit container after retries", "image name", imageName, "error", err)
			return err
		}

		//notify to remove the container
		if task.RemoveFlag {
			s.pool.Manager.Notify(task.ContainerID)
		}

		if err = s.imageClient.Squash(ctx, imageName, imageName); err != nil {
			slog.Error("failed to squash image", "image name", imageName, "error", err)
			return err
		}

		if pushFlag {
			if err = s.imageClient.Login(ctx, registry.LoginAddress, registry.UserName, registry.Password); err != nil {
				slog.Error("failed to login register", "error", err)
				return err
			}

			if err = s.imageClient.Push(ctx, imageName); err != nil {
				slog.Error("failed to push container", "error", err)
				return err
			}
		} else {
			slog.Error("not push container", "error", errutil.ErrPasswordNotFound)
		}
	}
	return nil
}

func (s *Server) GetInfoFromContainerEnv(resp *runtimeapi.ContainerStatusResponse) (*imageutil.Registry, string, bool, bool, error) {
	info := &container.Info{}
	if err := json.Unmarshal([]byte(resp.Info["info"]), info); err != nil {
		slog.Error("failed to unmarshal container info", "error", err)
		return nil, "", false, false, err
	}
	slog.Debug("Got container info env", "info env", info.Config.Envs)

	var registryName, userName, password, imageName, repo, commitOnStop, sealosUsername string
	envMap := map[string]*string{
		types.ImageRegistryAddressOnEnv:    &registryName,
		types.ImageRegistryUserNameOnEnv:   &userName,
		types.ImageRegistryPasswordOnEnv:   &password,
		types.ImageNameOnEnv:               &imageName,
		types.ImageRegistryRepositoryOnEnv: &repo,
		types.ContainerCommitOnStopEnvFlag: &commitOnStop,
		types.SealosUsernameOnEnv:          &sealosUsername,
	}
	for _, env := range info.Config.Envs {
		if target, exists := envMap[env.Key]; exists {
			*target = env.Value
		}
	}
	slog.Debug("Got container env", "registry", registryName, "user", userName, "password", password, "image", imageName, "repo", repo, "commitOnStop", commitOnStop)

	commitFlag := false
	if commitOnStop == types.ContainerCommitOnStopEnvEnableValue {
		commitFlag = true
	} else {
		return nil, "", false, false, nil
	}

	if imageName == "" {
		imageName = resp.Status.Image.Image
		parts := strings.Split(imageName, "/")
		// todo add more check for image name
		if len(parts) > 1 {
			imageName = strings.Join(parts[len(parts)-1:], "/")
		}
	}
	envRegistryOpt := imageutil.RegistryOptions{
		RegistryAddr: registryName,
		UserName:     userName,
		Password:     password,
		Repository:   repo,
	}

	pushFlag := true
	if userName != "" && password == "" {
		pushFlag = false
		slog.Error("not found password", "error", errutil.ErrPasswordNotFound)
	}

	return imageutil.NewRegistry(s.globalRegistryOptions, envRegistryOpt, s.options.ContainerdNamespace, sealosUsername), imageName, commitFlag, pushFlag, nil
}

func (s *Server) CheckCommitFlag(ctx context.Context, ContainerID string) (bool, error) {
	statusReq := &runtimeapi.ContainerStatusRequest{
		ContainerId: ContainerID,
		Verbose:     true,
	}

	statusResp, err := s.client.ContainerStatus(ctx, statusReq)
	if err != nil {
		slog.Error("failed to get container status", "error", err)
		return false, err
	}

	_, _, commitFlag, _, err := s.GetInfoFromContainerEnv(statusResp)
	if err != nil {
		slog.Error("failed to get container env", "error", err)
		return false, err
	}
	return commitFlag, nil
}

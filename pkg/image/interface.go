package image

import (
	"context"
	"fmt"

	"io"
	"log/slog"
	"os"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/containerd/remotes/docker/config"
	"github.com/containerd/nerdctl/v2/pkg/api/types"
	"github.com/containerd/nerdctl/v2/pkg/clientutil"
	"github.com/containerd/nerdctl/v2/pkg/cmd/container"
	"github.com/containerd/nerdctl/v2/pkg/cmd/image"
	"github.com/containerd/nerdctl/v2/pkg/cmd/login"
	"github.com/labring/layer-squash/pkg/options"
	"github.com/labring/layer-squash/pkg/runtime"
)

// ImageInterface defines the interface for image operations
type ImageInterface interface {
	Push(ctx context.Context, args, username, password string) error
	Commit(ctx context.Context, imageName, containerID string, pause bool) error
	Login(ctx context.Context, serverAddress, username, password string) error
	Squash(ctx context.Context, SourceImageRef, TargetImageName string) error
	Remove(ctx context.Context, args string, force, async bool) error
	Tag(ctx context.Context, src, dest string) error
	Stop()
}

type imageInterfaceImpl struct {
	GlobalOptions types.GlobalCommandOptions
	Stdout        io.Writer
	FStdout       *os.File
	Cancel        context.CancelFunc

	Client       *containerd.Client
	SquashClient *runtime.Runtime
}

// NewImageInterface returns a new implementation of ImageInterface
// address: the address of the container runtime
// writer: the io.Writer for output
func NewImageInterface(namespace, address string, fStdout *os.File) (ImageInterface, error) {
	global := types.GlobalCommandOptions{
		Namespace:        namespace,
		Address:          address,
		InsecureRegistry: true,
	}
	impl := &imageInterfaceImpl{
		GlobalOptions: global,
		Stdout:        fStdout,
		FStdout:       fStdout,
	}
	var err error
	if impl.Client, _, impl.Cancel, err = clientutil.NewClient(context.Background(), global.Namespace, global.Address); err != nil {
		return nil, err
	}

	if impl.SquashClient, err = runtime.NewRuntime(impl.Client, global.Namespace); err != nil {
		return nil, err
	}

	return impl, nil
}

func (impl *imageInterfaceImpl) Stop() {
	slog.Info("Stopping image interface")
	impl.Client.Close()
	impl.FStdout.Close()
}

// Commit commits a container as an image
// imageName: the name of the image
// containerID: the ID of the container
// pause: whether to pause the container before committing
func (impl *imageInterfaceImpl) Commit(ctx context.Context, imageName, containerID string, pause bool) error {
	slog.Info("Committing container", "ContainerID", containerID, "ImageName", imageName)
	opt := types.ContainerCommitOptions{
		Stdout:   impl.Stdout,
		GOptions: impl.GlobalOptions,
		Pause:    pause,
	}
	return container.Commit(ctx, impl.Client, imageName, containerID, opt)
}

// convert converts an image to the specified format
// srcRawRef: the source image reference
// destRawRef: the destination image reference
func (impl *imageInterfaceImpl) convert(ctx context.Context, srcRawRef, destRawRef string) error {
	slog.Info("Converting image", "Source", srcRawRef, "Destination", destRawRef)
	opt := types.ImageConvertOptions{
		GOptions: impl.GlobalOptions,
		Oci:      true,
		Stdout:   impl.Stdout,
	}
	return image.Convert(ctx, impl.Client, srcRawRef, destRawRef, opt)
}

// Remove deletes the specified image
// args: the list of images
// force: whether to force delete
// async: whether to delete asynchronously
func (impl *imageInterfaceImpl) Remove(ctx context.Context, args string, force, async bool) error {
	slog.Info("Removing image", "Image", args)
	opt := types.ImageRemoveOptions{
		Stdout:   impl.Stdout,
		GOptions: impl.GlobalOptions,
		Force:    force,
		Async:    async,
	}
	return image.Remove(ctx, impl.Client, []string{args}, opt)
}

// Push pushes an image to a remote repository
// args: the list of images
func (impl *imageInterfaceImpl) Push(ctx context.Context, args, username, password string) error {
	//set resolver
	resolver, err := GetResolver(ctx, username, password)
	if err != nil {
		slog.Error("failed to set resolver", "Image", args, "Error", err)
		return err
	}

	imageRef, err := impl.Client.GetImage(ctx, args)
	if err != nil {
		slog.Error("Get image error", "Image", args, "Error", err)
		return err
	}

	// push image
	err = impl.Client.Push(ctx, args, imageRef.Target(),
		containerd.WithResolver(resolver),
	)
	if err != nil {
		slog.Error("Push image error ", "Image", args, "Error", err)
		return err
	}
	slog.Info("Pushed image success", "Image", args)
	return nil
}

func GetResolver(ctx context.Context, username string, secret string) (remotes.Resolver, error) {
	resolverOptions := docker.ResolverOptions{
		Tracker: docker.NewInMemoryTracker(),
	}
	hostOptions := config.HostOptions{}
	hostOptions.Credentials = func(host string) (string, string, error) {
		return username, secret, nil
	}
	hostOptions.DefaultScheme = "http"

	hostOptions.DefaultTLS = nil
	resolverOptions.Hosts = config.ConfigureHosts(ctx, hostOptions)

	return docker.NewResolver(resolverOptions), nil
}

// Tag tags an image with the specified name
func (impl *imageInterfaceImpl) Tag(ctx context.Context, src, dest string) error {
	slog.Info("Tagging image", "Source", src, "Destination", dest)
	opt := types.ImageTagOptions{
		GOptions: impl.GlobalOptions,
		Source:   src,
		Target:   dest,
	}
	return image.Tag(ctx, impl.Client, opt)
}

// Login logs in to the image registry
// serverAddress: the registry address
// username: the username
// password: the password
func (impl *imageInterfaceImpl) Login(ctx context.Context, serverAddress, username, password string) error {
	slog.Info("Logging in to registry", "ServerAddress", serverAddress, "Username", username)
	opt := types.LoginCommandOptions{
		GOptions: impl.GlobalOptions,
		Username: username,
		Password: password,
	}
	if serverAddress != "" {
		opt.ServerAddress = serverAddress
	}
	return login.Login(ctx, opt, impl.Stdout)
}

func (impl *imageInterfaceImpl) Squash(ctx context.Context, SourceImageRef, TargetImageName string) error {
	slog.Info("Squashing image", "SourceImageRef", SourceImageRef, "TargetImageName", TargetImageName)
	opt := options.Option{
		SourceImageRef:   SourceImageRef,
		TargetImageName:  TargetImageName,
		SquashLayerCount: 2,
	}
	ctx, done, err := impl.Client.WithLease(ctx, leases.WithRandomID(), leases.WithExpiration(1*time.Hour))
	if err != nil {
		return fmt.Errorf("failed to create lease for squash: %w", err)
	}
	defer done(ctx)
	return impl.SquashClient.Squash(ctx, opt)
}

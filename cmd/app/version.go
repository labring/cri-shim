package app

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/containerd/nerdctl/v2/pkg/clientutil"

	"github.com/labring/cri-shim/pkg/infoutil"
	"github.com/labring/cri-shim/pkg/types"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Display the version information",
		RunE:  versionAction,
	}
}

func versionAction(cmd *cobra.Command, args []string) error {
	var w io.Writer = os.Stdout

	config := types.BindOptions(cmd)

	v, vErr := versionInfo(cmd, config.ContainerdNamespace, config.RuntimeSocket)

	fmt.Fprintln(w, "Client:")
	fmt.Fprintf(w, " Version:\t%s\n", v.Client.Version)
	fmt.Fprintf(w, " GO Version:\t%s\n", v.Client.GoVersion)
	fmt.Fprintf(w, " OS/Arch:\t%s/%s\n", v.Client.Os, v.Client.Arch)
	fmt.Fprintf(w, " Commit:\t%s\n", v.Client.CommitHash)
	fmt.Fprintf(w, " Build Time:\t%s\n", v.Client.BuildTime)
	if v.Server != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Server:")
		for _, compo := range v.Server.Components {
			fmt.Fprintf(w, " %s:\n", compo.Name)
			fmt.Fprintf(w, "  Version:\t%s\n", compo.Version)
			for detailK, detailV := range compo.Details {
				fmt.Fprintf(w, "  %s:\t%s\n", detailK, detailV)
			}
		}
	}
	return vErr
}

func versionInfo(cmd *cobra.Command, ns, address string) (infoutil.VersionInfo, error) {
	v := infoutil.VersionInfo{
		Client: infoutil.GetClientVersion(),
	}
	if address == "" {
		return v, nil
	}
	client, ctx, cancel, err := clientutil.NewClient(cmd.Context(), ns, address)
	if err != nil {
		return v, err
	}
	defer cancel()
	v.Server, err = infoutil.GetServerVersion(ctx, client)
	return v, err
}

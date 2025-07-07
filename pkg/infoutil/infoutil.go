package infoutil

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/containerd/containerd/v2/client"
)

var (
	// Version is filled via Makefile
	Version    = ""
	CommitHash = ""
	BuildTime  = ""
)

const unknown = "<unknown>"

func GetClientVersion() ClientVersion {
	return ClientVersion{
		Version:    GetVersion(),
		GoVersion:  runtime.Version(),
		Os:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		CommitHash: CommitHash,
		BuildTime:  BuildTime,
	}
}

func GetVersion() string {
	if Version != "" {
		return Version
	}
	/*
	 * go install example.com/cmd/foo@vX.Y.Z: bi.Main.Version="vX.Y.Z",                               vcs.revision is unset
	 * go install example.com/cmd/foo@latest: bi.Main.Version="vX.Y.Z",                               vcs.revision is unset
	 * go install example.com/cmd/foo@master: bi.Main.Version="vX.Y.Z-N.yyyyMMddhhmmss-gggggggggggg", vcs.revision is unset
	 * go install ./cmd/foo:                  bi.Main.Version="(devel)", vcs.revision="gggggggggggggggggggggggggggggggggggggggg"
	 *                                        vcs.time="yyyy-MM-ddThh:mm:ssZ", vcs.modified=("false"|"true")
	 */
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return bi.Main.Version
		}
	}
	return unknown
}

func GetServerVersion(ctx context.Context, client *client.Client) (*ServerVersion, error) {
	daemonVersion, err := client.Version(ctx)
	if err != nil {
		return nil, err
	}

	v := &ServerVersion{
		Components: []ComponentVersion{
			{
				Name:    "containerd",
				Version: daemonVersion.Version,
				Details: map[string]string{"GitCommit": daemonVersion.Revision},
			},
			runcVersion(),
		},
	}
	return v, nil
}

func runcVersion() ComponentVersion {
	stdout, err := exec.Command("runc", "--version").Output()
	if err != nil {
		slog.Error("failed to exec runc version", err)
		return ComponentVersion{Name: "runc"}
	}
	v, err := parseRuncVersion(stdout)
	if err != nil {
		slog.Error("failed to parse runc version", err)
		return ComponentVersion{Name: "runc"}
	}
	return *v
}

func parseRuncVersion(runcVersionStdout []byte) (*ComponentVersion, error) {
	var versionList = strings.Split(strings.TrimSpace(string(runcVersionStdout)), "\n")
	firstLine := strings.Fields(versionList[0])
	if len(firstLine) != 3 || firstLine[0] != "runc" {
		return nil, fmt.Errorf("unable to determine runc version, got: %s", string(runcVersionStdout))
	}
	version := firstLine[2]

	details := map[string]string{}
	for _, detailsLine := range versionList[1:] {
		detail := strings.SplitN(detailsLine, ":", 2)
		if len(detail) != 2 {
			slog.Warn("unable to determine one of runc details", "detail", detail)
			continue
		}
		switch strings.TrimSpace(detail[0]) {
		case "commit":
			details["GitCommit"] = strings.TrimSpace(detail[1])
		}
	}

	return &ComponentVersion{
		Name:    "runc",
		Version: version,
		Details: details,
	}, nil
}

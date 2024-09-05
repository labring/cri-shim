package infoutil

type VersionInfo struct {
	Client ClientVersion
	Server *ServerVersion
}

type ClientVersion struct {
	Version   string
	GoVersion string
	Os        string // GOOS
	Arch      string // GOARCH
}

type ComponentVersion struct {
	Name    string
	Version string
	Details map[string]string `json:",omitempty"`
}

type ServerVersion struct {
	Components []ComponentVersion
}

package buildinfo

import "strings"

var (
	Version = "dev"
	Commit  = "unknown"
)

func DisplayVersion() string {
	version := strings.TrimSpace(Version)
	commit := strings.TrimSpace(Commit)

	switch {
	case version == "":
		version = "dev"
	case version == "dev" && commit != "" && commit != "unknown":
		return version + "+" + commit
	}
	return version
}

package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

const versionFormatJSON = "json"

// BuildInfo contains immutable binary build information.
type BuildInfo struct {
	Version   string `json:"version"`
	BuildTime string `json:"build_time"`
	GitCommit string `json:"git_commit"`
	GitBranch string `json:"git_branch"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// WriteVersion writes build information in text or JSON format.
func WriteVersion(w io.Writer, info BuildInfo, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(info)
	}
	_, err := fmt.Fprintf(
		w,
		"Starport %s\nBuild time: %s\nGit commit: %s\nGit branch: %s\nGo version: %s\nOS/Arch: %s/%s\n",
		info.Version,
		info.BuildTime,
		info.GitCommit,
		info.GitBranch,
		info.GoVersion,
		info.OS,
		info.Arch,
	)
	return err
}

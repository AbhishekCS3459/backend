// Package version exposes build metadata for the running binary.
package version

import (
	"runtime/debug"
	"sync"
)

// Version is set at link time, e.g.
// go build -ldflags "-X github.com/AbhishekCS3459/find-me-backend/internal/platform/version.Version=abc123".
var Version = "dev"

// Get returns the injected version, falling back to the VCS revision embedded
// by `go build` in a git checkout, and finally "dev".
var Get = sync.OnceValue(func() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && len(setting.Value) >= 12 {
				return setting.Value[:12]
			}
		}
	}
	return Version
})

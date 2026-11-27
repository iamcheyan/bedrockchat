// Package update installs verified binaries from the latest stable bedrockchat release.
package update

import (
	"context"
	"fmt"
	"io"
	"runtime"
)

// Run checks for a newer stable release and replaces the running executable on
// Linux amd64 or arm64. A current version of "dev" or "(devel)" is older than any
// stable release. Output is best effort; a completed install is not an error
// just because its status message could not be written.
func Run(ctx context.Context, current string, out io.Writer) error {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return fmt.Errorf("self-update is unavailable on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return runPlatform(ctx, current, out)
}

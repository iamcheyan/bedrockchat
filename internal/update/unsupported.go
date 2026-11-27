//go:build !linux

package update

import (
	"context"
	"errors"
	"io"
)

func runPlatform(context.Context, string, io.Writer) error {
	return errors.New("self-update requires Linux")
}

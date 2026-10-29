//go:build !darwin

package tunnel

import (
	"context"
	"errors"
)

func Run(context.Context, Up, func(string, ...any), func(string)) error {
	return errors.New("the tunnel is only implemented for macOS so far")
}

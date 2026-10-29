//go:build windows

package main

import (
	"context"
	"errors"
)

func up(context.Context) error {
	return errors.New("up is for terminals on macOS; on Windows use the tray app")
}

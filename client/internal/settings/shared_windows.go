//go:build windows

package settings

import (
	"os"
	"path/filepath"
)

func init() { Shared = filepath.Join(os.Getenv("ProgramData"), "MapleGate", "config.json") }

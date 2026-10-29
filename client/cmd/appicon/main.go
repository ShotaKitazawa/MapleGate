// appicon writes the app's icon as an .iconset folder, which iconutil turns into the .icns the bundle needs.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ShotaKitazawa/MapleGate/client/internal/icon"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: appicon <dir>.iconset")
		os.Exit(2)
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, n := range []int{16, 32, 128, 256, 512} {
		for _, scale := range []int{1, 2} {
			name := fmt.Sprintf("icon_%dx%d.png", n, n)
			if scale == 2 {
				name = fmt.Sprintf("icon_%dx%d@2x.png", n, n)
			}
			if err := os.WriteFile(filepath.Join(dir, name), icon.App(n*scale), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	}
}

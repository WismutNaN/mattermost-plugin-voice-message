// +build ignore

// pack.go — creates the plugin .tar.gz with correct Unix permissions.
// Run: go run pack.go
// Works on Windows, Linux, macOS.
package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	pluginID      = "com.scientia.voice-message"
	pluginVersion = "2.0.1"
)

func main() {
	srcDir := filepath.Join("dist", pluginID)
	outFile := filepath.Join("dist", pluginID+"-"+pluginVersion+".tar.gz")

	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "ERROR: %s not found. Run 'make server webapp' first, then 'make dist' or this script.\n", srcDir)
		os.Exit(1)
	}

	f, err := os.Create(outFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: create %s: %v\n", outFile, err)
		os.Exit(1)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	count := 0
	err = filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Archive path: com.scientia.voice-message/...
		relPath, _ := filepath.Rel(filepath.Dir(srcDir), path)
		relPath = filepath.ToSlash(relPath) // Windows backslash → forward slash

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		if info.IsDir() {
			header.Name += "/"
			header.Mode = 0755
		} else if isExecutable(relPath) {
			header.Mode = 0755
		} else {
			header.Mode = 0644
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			if _, err := io.Copy(tw, file); err != nil {
				return err
			}
			count++
		}
		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅  %s (%d files)\n", outFile, count)
}

// isExecutable returns true for server binaries (no extension or .exe).
func isExecutable(archivePath string) bool {
	// server/dist/plugin-linux-amd64, plugin-darwin-arm64, etc.
	if !strings.Contains(archivePath, "server/dist/plugin-") {
		return false
	}
	// .exe files don't need Unix execute bit, but it doesn't hurt
	return true
}

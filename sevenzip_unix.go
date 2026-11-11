//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func extractEmbedded7z(dir string) (string, error) {
	if len(embedded7z) == 0 {
		return "", fmt.Errorf("sin 7-Zip incluido")
	}
	p := filepath.Join(dir, "7zz")
	if st, err := os.Stat(p); err == nil && st.Size() == int64(len(embedded7z)) {
		return p, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p+".tmp", embedded7z, 0o755); err != nil {
		return "", err
	}
	return p, os.Rename(p+".tmp", p)
}

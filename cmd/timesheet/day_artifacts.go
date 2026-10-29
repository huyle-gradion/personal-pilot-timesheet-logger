package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

func dayArtifactsDir(statePath, day string) (string, error) {
	abs, err := filepath.Abs(statePath)
	if err != nil {
		return "", fmt.Errorf("resolve artifact store identity: %w", err)
	}
	identity := fmt.Sprintf("%x", sha256.Sum256([]byte(abs)))
	return filepath.Join(os.TempDir(), "timesheet", identity, day), nil
}

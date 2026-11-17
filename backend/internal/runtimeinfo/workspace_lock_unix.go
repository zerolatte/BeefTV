//go:build !windows

package runtimeinfo

import (
	"infinite-canvas/backend/internal/desktopstorage"
	"os"
)

func lockWorkspaceFile(dataDir string) (*WorkspaceLock, error) {
	file, err := desktopstorage.Lock(dataDir)
	if err != nil {
		return nil, err
	}
	return &WorkspaceLock{files: []*os.File{file}}, nil
}

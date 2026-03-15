package util

import (
	"syscall"
)

// DiskInfo holds disk space information for a path
type DiskInfo struct {
	Total     uint64 `json:"total"`      // Total space in bytes
	Free      uint64 `json:"free"`       // Free space in bytes
	Used      uint64 `json:"used"`       // Used space in bytes
	Available uint64 `json:"available"`  // Available to unprivileged users
}

// GetDiskFree returns the free disk space in bytes for the given path
func GetDiskFree(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	// Available space = blocks available to unprivileged users * block size
	return stat.Bavail * uint64(stat.Bsize), nil
}

// GetDiskInfo returns full disk space information for the given path
func GetDiskInfo(path string) (*DiskInfo, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	available := stat.Bavail * uint64(stat.Bsize)
	used := total - free
	return &DiskInfo{
		Total:     total,
		Free:      free,
		Used:      used,
		Available: available,
	}, nil
}

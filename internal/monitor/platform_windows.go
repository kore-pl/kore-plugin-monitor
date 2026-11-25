//go:build windows

package monitor

// Windows 采集走 PowerShell/WMI，不读 /proc/mounts，也不做块设备符号解析。
const platformSupported = true

const platformLabel = "Windows PowerShell/WMI"

type fsStats struct {
	BlockSize, Blocks, Bfree, Bavail, Files, Ffree int64
}

func statfs(string) (fsStats, error) { return fsStats{}, errUnsupportedPlatform }

func resolveDevice(path string) (string, error) { return path, nil }

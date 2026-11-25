//go:build darwin

package monitor

// macOS 采集走命令执行面，不读 /proc/mounts，也不做块设备符号解析。
const platformSupported = true

const platformLabel = "macOS sysctl/ps"

type fsStats struct {
	BlockSize, Blocks, Bfree, Bavail, Files, Ffree int64
}

func statfs(string) (fsStats, error) { return fsStats{}, errUnsupportedPlatform }

func resolveDevice(path string) (string, error) { return path, nil }

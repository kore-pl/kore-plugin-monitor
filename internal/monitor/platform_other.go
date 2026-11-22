//go:build !linux

package monitor

// platformSupported 表示当前平台是否提供真实 /proc 采集；非 Linux 显式拒绝。
const platformSupported = false

// fsStats 在非 Linux 上不参与运行（Call 入口返回 E_UNSUPPORTED）。
type fsStats struct {
	BlockSize, Blocks, Bfree, Bavail, Files, Ffree int64
}

// statfs 在非 Linux 上显式失败，不静默返回 0。
func statfs(string) (fsStats, error) { return fsStats{}, errUnsupportedPlatform }

// resolveDevice 在非 Linux 上没有块设备语义。
func resolveDevice(string) (string, error) { return "", errUnsupportedPlatform }

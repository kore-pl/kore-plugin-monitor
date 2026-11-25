//go:build !linux && !darwin && !windows

package monitor

import "fmt"

// platformSupported 表示当前平台是否有真实采集实现；其它 OS 显式拒绝，不静默降级。
const platformSupported = false

const platformLabel = "unsupported"

// fsStats 在不支持的平台上不参与运行（Call 入口返回 E_UNSUPPORTED）。
type fsStats struct {
	BlockSize, Blocks, Bfree, Bavail, Files, Ffree int64
}

func statfs(string) (fsStats, error) { return fsStats{}, errUnsupportedPlatform }

func resolveDevice(string) (string, error) { return "", errUnsupportedPlatform }

func newPlatformExecutor() Executor { return nil }

var _ = fmt.Sprintf

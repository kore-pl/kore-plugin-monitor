//go:build linux

package monitor

import "syscall"

// platformSupported 表示当前平台是否提供真实 /proc 采集。
const platformSupported = true

// fsStats 是 statfs 的关键计数（块大小、块数、inode 数）。
type fsStats struct {
	BlockSize int64
	Blocks    int64
	Bfree     int64
	Bavail    int64
	Files     int64
	Ffree     int64
}

// statfs 读取路径所在文件系统的容量与 inode 统计。
func statfs(path string) (fsStats, error) {
	var raw syscall.Statfs_t
	if err := syscall.Statfs(path, &raw); err != nil {
		return fsStats{}, err
	}
	return fsStats{
		BlockSize: int64(raw.Bsize), Blocks: int64(raw.Blocks), Bfree: int64(raw.Bfree),
		Bavail: int64(raw.Bavail), Files: int64(raw.Files), Ffree: int64(raw.Ffree),
	}, nil
}

// resolveDevice 解析 /dev/mapper 等符号链接，得到真实块设备路径。
func resolveDevice(path string) (string, error) {
	return resolveSymlink(path)
}

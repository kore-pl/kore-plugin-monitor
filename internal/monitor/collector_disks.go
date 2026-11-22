package monitor

import (
	"os"
	"path/filepath"
	"time"
)

// diskUsages 供 monitor.system 使用：每个真实块设备挂载点一行容量用量。
func (c *Collector) diskUsages(warnings *[]string) []DiskUsage {
	mounts, err := c.readMounts(warnings)
	if err != nil {
		return []DiskUsage{}
	}
	usages := make([]DiskUsage, 0, len(mounts))
	for _, entry := range mounts {
		stats, statErr := statfs(entry.Mount)
		if statErr != nil {
			addWarning(warnings, "statfs %s 失败: %v", entry.Mount, statErr)
			continue
		}
		usage := diskUsageFrom(entry, stats)
		usages = append(usages, usage)
	}
	return usages
}

// Disks 返回磁盘详表（容量 + inode + /proc/diskstats 读写累计）。
func (c *Collector) Disks(ctx Context, params DisksParams) (DisksResult, error) {
	result := DisksResult{Schema: "monitor.disks.v1", Path: params.Path, Warnings: []string{}}
	result.SampledAt = c.now().UTC().Format(time.RFC3339Nano)
	mounts, _ := c.readMounts(&result.Warnings)
	if len(mounts) == 0 && params.Path == "" {
		return result, nil // /proc/mounts 不可读或无真实块设备：空列表 + warning，不失败
	}
	if params.Path != "" {
		entry, ok := matchMount(mounts, params.Path)
		if !ok {
			return result, errPathNotMounted
		}
		mounts = []mountEntry{entry}
	}
	ioStats := c.readDiskstats(&result.Warnings)
	items := make([]DiskDetail, 0, len(mounts))
	for _, entry := range mounts {
		stats, statErr := statfs(entry.Mount)
		if statErr != nil {
			addWarning(&result.Warnings, "statfs %s 失败: %v", entry.Mount, statErr)
			continue
		}
		items = append(items, diskDetailFrom(entry, stats, ioStats[diskName(entry.Device)]))
	}
	result.Items = items
	result.Count = len(items)
	return result, nil
}

// readMounts 读取并过滤 /proc/mounts；失败时返回 error 且追加 warning。
func (c *Collector) readMounts(warnings *[]string) ([]mountEntry, error) {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "mounts"))
	if err != nil {
		addWarning(warnings, "读取 /proc/mounts 失败: %v", err)
		return nil, err
	}
	return parseMounts(string(data)), nil
}

// readDiskstats 读取 /proc/diskstats；失败只降级为空统计 + warning。
func (c *Collector) readDiskstats(warnings *[]string) map[string]diskIO {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "diskstats"))
	if err != nil {
		addWarning(warnings, "读取 /proc/diskstats 失败: %v", err)
		return map[string]diskIO{}
	}
	return parseDiskstats(string(data))
}

// diskUsageFrom 把 statfs 计数换算为对外容量结构（df 口径：used/(used+avail)）。
func diskUsageFrom(entry mountEntry, stats fsStats) DiskUsage {
	total := stats.Blocks * stats.BlockSize
	used := (stats.Blocks - stats.Bfree) * stats.BlockSize
	avail := stats.Bavail * stats.BlockSize
	return DiskUsage{
		Mount: entry.Mount, Device: entry.Device, FSType: entry.FSType,
		Total: total, TotalHuman: humanBytes(total), Used: used, UsedHuman: humanBytes(used),
		Avail: avail, AvailHuman: humanBytes(avail), UsedPercent: diskUsedPercent(used, avail),
	}
}

// diskDetailFrom 在容量用量上补 inode 与块设备读写累计。
func diskDetailFrom(entry mountEntry, stats fsStats, io diskIO) DiskDetail {
	base := diskUsageFrom(entry, stats)
	inodesUsed := stats.Files - stats.Ffree
	return DiskDetail{
		Mount: base.Mount, Device: base.Device, FSType: base.FSType,
		Total: base.Total, TotalHuman: base.TotalHuman, Used: base.Used, UsedHuman: base.UsedHuman,
		Avail: base.Avail, AvailHuman: base.AvailHuman, UsedPercent: base.UsedPercent,
		InodesTotal: stats.Files, InodesUsed: inodesUsed, InodesFree: stats.Ffree,
		InodesUsedPercent: percentOf(inodesUsed, stats.Files),
		ReadBytes:         io.ReadBytes, ReadBytesHuman: humanBytes(io.ReadBytes),
		WriteBytes: io.WriteBytes, WriteBytesHuman: humanBytes(io.WriteBytes),
		Reads: io.Reads, Writes: io.Writes,
	}
}

// diskUsedPercent 用 used/(used+avail)（df 语义），排除 root 保留块造成的偏差。
func diskUsedPercent(used, avail int64) float64 {
	if used+avail <= 0 {
		return 0
	}
	return clampPercent(float64(used) / float64(used+avail) * 100)
}

// percentOf 返回 part/whole 的百分比（whole<=0 时为 0）。
func percentOf(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return clampPercent(float64(part) / float64(whole) * 100)
}

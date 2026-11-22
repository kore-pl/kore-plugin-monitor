package monitor

import (
	"strconv"
	"strings"
)

// diskIO 是一个块设备的累计读写统计（扇区按 512 字节换算为字节）。
type diskIO struct {
	Reads, Writes         int64
	ReadBytes, WriteBytes int64
}

// parseDiskstats 解析 /proc/diskstats：name → 累计读写。
// 行格式：major minor name reads reads_merged sectors_read ms_reading writes ...
// 其中 reads=索引 3、sectors_read=5、writes=7、sectors_written=9。
func parseDiskstats(data string) map[string]diskIO {
	result := make(map[string]diskIO)
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		name := fields[2]
		reads, errReads := strconv.ParseInt(fields[3], 10, 64)
		sectorsRead, errSR := strconv.ParseInt(fields[5], 10, 64)
		writes, errWrites := strconv.ParseInt(fields[7], 10, 64)
		sectorsWrite, errSW := strconv.ParseInt(fields[9], 10, 64)
		if errReads != nil || errSR != nil || errWrites != nil || errSW != nil {
			continue
		}
		result[name] = diskIO{
			Reads: reads, Writes: writes,
			ReadBytes: sectorsRead * DiskSectorBytes, WriteBytes: sectorsWrite * DiskSectorBytes,
		}
	}
	return result
}

// diskName 把 /proc/mounts 的设备路径归一到 /proc/diskstats 的块设备名：
// /dev/nvme0n1p2 → nvme0n1p2；/dev/mapper/vg-lv 若可解析则取 dm-0。
func diskName(device string) string {
	base := device
	if idx := strings.LastIndexByte(device, '/'); idx >= 0 {
		base = device[idx+1:]
	}
	if resolved, err := resolveDevice(device); err == nil && resolved != "" {
		if idx := strings.LastIndexByte(resolved, '/'); idx >= 0 {
			return resolved[idx+1:]
		}
	}
	return base
}

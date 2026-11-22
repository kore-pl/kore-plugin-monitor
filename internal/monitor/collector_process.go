package monitor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// procSample 是单个进程一次采样的原始记录。
type procSample struct {
	stat   procStat
	rss    int64
	status procStatus
}

// Processes 返回 top N 进程：两次扫描 /proc 求 CPU tick 差分，按 sort 排序后截断。
func (c *Collector) Processes(ctx Context, params ProcessParams) (ProcessesResult, error) {
	result := ProcessesResult{
		Schema: "monitor.processes.v1", Sort: params.Sort, Limit: params.Limit, Warnings: []string{},
	}
	interval, err := c.window(ctx)
	if err != nil {
		return result, err
	}
	result.SampleIntervalMS = interval.Milliseconds()
	result.SampledAt = c.now().UTC().Format(time.RFC3339Nano)
	start := c.now()
	prev, prevScan, err := c.scanProcesses()
	if err != nil {
		addWarning(&result.Warnings, "扫描 /proc 失败: %v", err)
		return result, nil
	}
	c.sleep(interval)
	curr, currScan, err := c.scanProcesses()
	if err != nil {
		addWarning(&result.Warnings, "第二次扫描 /proc 失败: %v", err)
		return result, nil
	}
	elapsed := c.now().Sub(start)
	skipped := prevScan.skipped + currScan.skipped
	statusMissing := prevScan.statusMissing + currScan.statusMissing
	if skipped > 0 {
		addWarning(&result.Warnings, "%d 个进程在采样期间退出或不可读，已跳过", skipped)
	}
	if statusMissing > 0 {
		addWarning(&result.Warnings, "%d 个进程 status 不可读，mem_rss_bytes 记 0", statusMissing)
	}
	items := make([]ProcessInfo, 0, len(curr))
	for pid, current := range curr {
		percent := 0.0
		if previous, ok := prev[pid]; ok {
			percent = procCPUPercent(previous.stat.UTicks+previous.stat.STicks,
				current.stat.UTicks+current.stat.STicks, elapsed)
		}
		threads := current.status.Threads
		if threads == 0 {
			threads = current.stat.Threads
		}
		items = append(items, ProcessInfo{
			PID: pid, Comm: current.stat.Comm, State: string(current.stat.State),
			CPUPercent: normalizeCPU(percent), MemRSSBytes: current.rss,
			MemRSSHuman: humanBytes(current.rss), Threads: threads,
		})
	}
	sortProcessItems(items, params.Sort)
	result.Total = len(items)
	if len(items) > params.Limit {
		items = items[:params.Limit]
	}
	result.Items = items
	result.Count = len(items)
	return result, nil
}

// scanCount 是一次扫描的降级计数，供调用方汇总为 warnings。
type scanCount struct {
	skipped       int
	statusMissing int
}

// scanProcesses 扫描 /proc/<pid>/stat + status；单进程退出/不可读只计数，不中断整次扫描。
func (c *Collector) scanProcesses() (map[int]procSample, scanCount, error) {
	entries, err := os.ReadDir(c.procRoot)
	if err != nil {
		return nil, scanCount{}, err
	}
	samples := make(map[int]procSample, len(entries))
	count := scanCount{}
	for _, entry := range entries {
		pid, ok := parsePID(entry.Name())
		if !ok {
			continue
		}
		statData, statErr := os.ReadFile(filepath.Join(c.procRoot, entry.Name(), "stat"))
		if statErr != nil {
			if !errors.Is(statErr, fs.ErrNotExist) {
				count.skipped++
			}
			continue
		}
		stat, parseErr := parseProcStat(string(statData), pid)
		if parseErr != nil {
			count.skipped++
			continue
		}
		status := procStatus{}
		if statusData, statusErr := os.ReadFile(filepath.Join(c.procRoot, entry.Name(), "status")); statusErr == nil {
			status = parseProcStatus(string(statusData))
		} else {
			count.statusMissing++
		}
		samples[pid] = procSample{stat: stat, rss: status.RSSBytes, status: status}
	}
	return samples, count, nil
}

// parsePID 只接受纯数字目录名（/proc/<pid>），其它条目一律跳过。
func parsePID(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	pid, err := strconv.Atoi(name)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// sortProcessItems 按 cpu/mem 降序排序，同值按 pid 升序保证结果稳定。
func sortProcessItems(items []ProcessInfo, key string) {
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if key == "mem" {
			if left.MemRSSBytes != right.MemRSSBytes {
				return left.MemRSSBytes > right.MemRSSBytes
			}
		} else if left.CPUPercent != right.CPUPercent {
			return left.CPUPercent > right.CPUPercent
		}
		return left.PID < right.PID
	})
}

package monitor

import (
	"strings"
	"testing"
)

// TestSortProcessItemsTieBreak 验证 CPU / 内存两种降序排序与同值按 pid 升序的稳定性。
func TestSortProcessItemsTieBreak(t *testing.T) {
	base := []ProcessInfo{
		{PID: 30, CPUPercent: 5, MemRSSBytes: 100},
		{PID: 10, CPUPercent: 50, MemRSSBytes: 200},
		{PID: 20, CPUPercent: 50, MemRSSBytes: 300},
	}
	cpuSorted := copyProcessItems(base)
	sortProcessItems(cpuSorted, "cpu")
	wantCPU := []int{10, 20, 30}
	for i, pid := range wantCPU {
		if cpuSorted[i].PID != pid {
			t.Fatalf("CPU 排序第 %d 位 pid=%d，期望 %d（%+v）", i, cpuSorted[i].PID, pid, cpuSorted)
		}
	}
	memSorted := copyProcessItems(base)
	sortProcessItems(memSorted, "mem")
	wantMem := []int{20, 10, 30}
	for i, pid := range wantMem {
		if memSorted[i].PID != pid {
			t.Fatalf("内存排序第 %d 位 pid=%d，期望 %d（%+v）", i, memSorted[i].PID, pid, memSorted)
		}
	}
}

// TestProcessesMemPercentAndName 验证 TOP 项包含 pid/name/comm/cpu%/mem%/mem_percent。
func TestProcessesMemPercentAndName(t *testing.T) {
	collector := fixtureCollector(t, map[string]string{
		"1234/stat":   procStatFixture("worker", "S", 100, 0, 4),
		"1234/status": "VmRSS:\t512 kB\nThreads:\t4\n",
		"meminfo":     "MemTotal: 1024 kB\nMemAvailable: 512 kB\n",
	}, nil)
	result, err := collector.Processes(Context{}, ProcessParams{Limit: 5, Sort: "cpu"})
	if err != nil {
		t.Fatalf("processes 失败: %v", err)
	}
	if result.Count != 1 || len(result.Items) != 1 {
		t.Fatalf("期望 1 个进程: %+v", result)
	}
	item := result.Items[0]
	if item.PID != 1234 || item.Comm != "worker" || item.Name != "worker" {
		t.Fatalf("pid/name 字段错误: %+v", item)
	}
	if item.MemRSSBytes != 512*1024 || item.MemPercent != 50 {
		t.Fatalf("mem_rss_bytes/mem_percent 错误: %+v", item)
	}
	if item.Threads != 4 || item.CPUPercent < 0 {
		t.Fatalf("threads/cpu_percent 错误: %+v", item)
	}
}

// TestProcessParamsDefaults 验证 limit / sort 的默认值与大小写归一。
func TestProcessParamsDefaults(t *testing.T) {
	params, err := parseProcessParams([]byte(`{}`))
	if err != nil {
		t.Fatalf("空参数解析失败: %v", err)
	}
	if params.Limit != DefaultProcessLimit || params.Sort != "cpu" {
		t.Fatalf("默认值错误: %+v", params)
	}
	params, err = parseProcessParams([]byte(`{"sort":"MEM"}`))
	if err != nil || params.Sort != "mem" {
		t.Fatalf("sort 大小写归一失败: %+v err=%v", params, err)
	}
}

// TestProcessParamsInvalid 验证 limit / sort 越界一律拒绝。
func TestProcessParamsInvalid(t *testing.T) {
	cases := []struct{ params, want string }{
		{`{"limit":0}`, "limit"},
		{`{"limit":501}`, "limit"},
		{`{"sort":"pid"}`, "sort"},
	}
	for _, tc := range cases {
		if _, err := parseProcessParams([]byte(tc.params)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("params=%s err=%v，期望拒绝并提及 %q", tc.params, err, tc.want)
		}
	}
}

// copyProcessItems 复制切片，避免排序用例相互污染。
func copyProcessItems(items []ProcessInfo) []ProcessInfo {
	out := make([]ProcessInfo, len(items))
	copy(out, items)
	return out
}

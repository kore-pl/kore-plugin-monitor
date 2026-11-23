package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// historyCollector 构造带固定时钟与状态目录的采集器，用于历史曲线用例。
func historyCollector(t *testing.T, stateDir string, clock *time.Time) *Collector {
	t.Helper()
	collector := fixtureCollector(t, map[string]string{
		"meminfo": "MemTotal: 1024 kB\nMemAvailable: 512 kB\n",
	}, nil)
	collector.stateDir = stateDir
	collector.now = func() time.Time { return *clock }
	return collector
}

// TestHistoryAppendAndInterval 验证「距上次采样 ≥ 间隔」才追加，且数组一一对齐。
func TestHistoryAppendAndInterval(t *testing.T) {
	stateDir := t.TempDir()
	clock := time.Unix(1_700_000_000, 0)
	collector := historyCollector(t, stateDir, &clock)
	params := SystemParams{HistoryPoints: 3, HistoryIntervalS: 10}

	first, err := collector.System(Context{}, params)
	if err != nil {
		t.Fatalf("首次 system 失败: %v", err)
	}
	if !first.History.Appended || first.History.Count != 1 || len(first.History.Timestamps) != 1 {
		t.Fatalf("首次调用应追加 1 个点: %+v", first.History)
	}
	if first.History.Schema != "monitor.history.v1" || first.History.IntervalS != 10 || first.History.Capacity != 3 {
		t.Fatalf("历史曲线元数据错误: %+v", first.History)
	}
	if first.History.MemoryPercent[0] != 50 {
		t.Fatalf("内存占比应为 50%%，得到 %v", first.History.MemoryPercent[0])
	}
	assertHistoryArraysAligned(t, first.History)

	clock = clock.Add(5 * time.Second) // 间隔未到
	second, err := collector.System(Context{}, params)
	if err != nil {
		t.Fatalf("第二次 system 失败: %v", err)
	}
	if second.History.Appended || second.History.Count != 1 {
		t.Fatalf("间隔未到不应追加: %+v", second.History)
	}
	if second.History.Timestamps[0] != first.History.Timestamps[0] {
		t.Fatalf("未追加时不应改变已有时间戳: %v → %v", first.History.Timestamps, second.History.Timestamps)
	}

	clock = clock.Add(5 * time.Second) // 正好等于 10s：应追加（≥ 语义）
	third, err := collector.System(Context{}, params)
	if err != nil {
		t.Fatalf("第三次 system 失败: %v", err)
	}
	if !third.History.Appended || third.History.Count != 2 {
		t.Fatalf("间隔正好到达应追加: %+v", third.History)
	}
	if third.History.Timestamps[1]-third.History.Timestamps[0] != 10_000 {
		t.Fatalf("时间戳间隔应为 10000ms: %v", third.History.Timestamps)
	}

	info, err := os.Stat(filepath.Join(stateDir, historyFileName))
	if err != nil {
		t.Fatalf("历史文件未落盘: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("历史文件权限应为 0600，实际 %o", perm)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, historyFileName))
	if err != nil || !strings.Contains(string(data), `"version":1`) || !strings.Contains(string(data), `"samples"`) {
		t.Fatalf("历史文件内容不合法: %s err=%v", data, err)
	}
}

// TestHistoryRingOverwrite 验证容量满后丢弃最旧的点（环形覆盖），保留最近 N 个。
func TestHistoryRingOverwrite(t *testing.T) {
	clock := time.Unix(1_700_000_100, 0)
	collector := historyCollector(t, t.TempDir(), &clock)
	params := SystemParams{HistoryPoints: 3, HistoryIntervalS: 1}
	for i := 0; i < 5; i++ {
		clock = time.Unix(1_700_000_100, 0).Add(time.Duration(i) * 2 * time.Second)
		result, err := collector.System(Context{}, params)
		if err != nil {
			t.Fatalf("第 %d 次 system 失败: %v", i, err)
		}
		if !result.History.Appended {
			t.Fatalf("第 %d 次调用应追加: %+v", i, result.History)
		}
	}
	last, err := collector.System(Context{}, SystemParams{HistoryPoints: 3, HistoryIntervalS: 1})
	if err != nil {
		t.Fatalf("末次 system 失败: %v", err)
	}
	if last.History.Count != 3 {
		t.Fatalf("容量 3 应只保留 3 个点，实际 %d", last.History.Count)
	}
	want := []int64{
		time.Unix(1_700_000_100, 0).Add(4 * time.Second).UnixMilli(),
		time.Unix(1_700_000_100, 0).Add(6 * time.Second).UnixMilli(),
		time.Unix(1_700_000_100, 0).Add(8 * time.Second).UnixMilli(),
	}
	if len(last.History.Timestamps) != 3 || last.History.Timestamps[0] != want[0] ||
		last.History.Timestamps[2] != want[2] {
		t.Fatalf("环形覆盖后应保留最近 3 个点: %v", last.History.Timestamps)
	}
	assertHistoryArraysAligned(t, last.History)
}

// TestHistoryRestartContinuity 验证新进程实例从 state 目录接续曲线，间隔内不重复追加。
func TestHistoryRestartContinuity(t *testing.T) {
	stateDir := t.TempDir()
	clock := time.Unix(1_700_000_200, 0)
	first := historyCollector(t, stateDir, &clock)
	params := SystemParams{HistoryPoints: 5, HistoryIntervalS: 1}
	if _, err := first.System(Context{}, params); err != nil {
		t.Fatalf("首次 system 失败: %v", err)
	}
	clock = clock.Add(2 * time.Second)
	if _, err := first.System(Context{}, params); err != nil {
		t.Fatalf("第二次 system 失败: %v", err)
	}
	// 模拟进程重启：全新 Collector 实例，只依赖 state 目录。
	restarted := historyCollector(t, stateDir, &clock)
	clock = clock.Add(500 * time.Millisecond)
	resumed, err := restarted.System(Context{}, params)
	if err != nil {
		t.Fatalf("重启后 system 失败: %v", err)
	}
	if resumed.History.Count != 2 || resumed.History.Appended {
		t.Fatalf("重启后间隔未到应接续 2 个点且不追加: %+v", resumed.History)
	}
	clock = clock.Add(500 * time.Millisecond) // 距最后一点正好 1s
	appended, err := restarted.System(Context{}, params)
	if err != nil {
		t.Fatalf("重启后追加 system 失败: %v", err)
	}
	if appended.History.Count != 3 || !appended.History.Appended {
		t.Fatalf("重启后间隔到达应追加第 3 个点: %+v", appended.History)
	}
}

// TestHistoryCorruptFileRebuild 验证损坏文件只 warning + 重建，不影响采集结果。
func TestHistoryCorruptFileRebuild(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, historyFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("写入损坏文件失败: %v", err)
	}
	clock := time.Unix(1_700_000_300, 0)
	collector := historyCollector(t, stateDir, &clock)
	result, err := collector.System(Context{}, SystemParams{HistoryPoints: 3, HistoryIntervalS: 1})
	if err != nil {
		t.Fatalf("损坏文件不应让 system 失败: %v", err)
	}
	if result.History.Count != 1 || !result.History.Appended {
		t.Fatalf("损坏文件应重建为 1 个点: %+v", result.History)
	}
	if !hasWarning(result.Warnings, "历史曲线文件损坏") {
		t.Fatalf("应写入重建 warning: %v", result.Warnings)
	}
}

// TestHistoryWithoutStateDir 验证未注入 KORE_STATE_DIR 时不落盘；每次调用各自成点
// （生产由内核注入该变量，跨调用接续依赖 state 文件）。
func TestHistoryWithoutStateDir(t *testing.T) {
	clock := time.Unix(1_700_000_400, 0)
	collector := historyCollector(t, "", &clock)
	result, err := collector.System(Context{}, SystemParams{HistoryPoints: 4, HistoryIntervalS: 1})
	if err != nil {
		t.Fatalf("system 失败: %v", err)
	}
	if result.History.Count != 1 || !result.History.Appended {
		t.Fatalf("无 state 目录时应只保留本次采样的 1 个点: %+v", result.History)
	}
	if collector.historyPath() != "" {
		t.Fatalf("未注入 KORE_STATE_DIR 时不应推导出历史文件路径: %q", collector.historyPath())
	}
}

// TestHistoryCapacityShrinkTrims 验证 history_points 调小后立即裁剪，不返回超过 N 的旧点。
func TestHistoryCapacityShrinkTrims(t *testing.T) {
	stateDir := t.TempDir()
	origin := time.Unix(1_700_000_150, 0)
	clock := origin
	collector := historyCollector(t, stateDir, &clock)
	for i := 0; i < 4; i++ {
		clock = origin.Add(time.Duration(i) * 2 * time.Second)
		if _, err := collector.System(Context{}, SystemParams{HistoryPoints: 5, HistoryIntervalS: 1}); err != nil {
			t.Fatalf("第 %d 次 system 失败: %v", i, err)
		}
	}
	clock = origin.Add(6*time.Second + 500*time.Millisecond)
	result, err := collector.System(Context{}, SystemParams{HistoryPoints: 2, HistoryIntervalS: 60})
	if err != nil {
		t.Fatalf("裁剪调用失败: %v", err)
	}
	if result.History.Count != 2 || result.History.Capacity != 2 {
		t.Fatalf("容量调小后应只剩 2 个点: %+v", result.History)
	}
	if result.History.Timestamps[1] != origin.Add(6*time.Second).UnixMilli() {
		t.Fatalf("裁剪应保留最近的点: %v", result.History.Timestamps)
	}
}

// assertHistoryArraysAligned 断言列式数组长度都等于 count（前端可直接按下标画折线）。
func assertHistoryArraysAligned(t *testing.T, history HistoryResult) {
	t.Helper()
	lengths := []int{
		len(history.Timestamps), len(history.CPUPercent), len(history.MemoryPercent),
		len(history.Load1), len(history.DiskPercent),
	}
	for _, length := range lengths {
		if length != history.Count {
			t.Fatalf("数组长度 %v 与 count=%d 不一致", lengths, history.Count)
		}
	}
}

// hasWarning 判断 warnings 是否包含指定子串。
func hasWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}

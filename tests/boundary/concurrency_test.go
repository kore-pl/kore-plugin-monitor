// 并发（concurrency）类边界：
//   - 同一 Collector 上 System / Processes / Temperature 真并发（go func + sync.WaitGroup，
//     -race 下断言无数据竞争，且每个并发结果都完整、没有跨调用串味）；
//   - 历史曲线写者与读者并发：读者看到的 history.json 必须始终是合法 JSON
//     （原子 rename 语义），最终文件满足 append 间隔语义（互斥失效会写出重复点）。
package boundary

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-monitor/internal/monitor"
)

// TestConcurrentCollectAllMethods 用 8 个 goroutine × 3 轮并发调三个采集方法。
// 断言并发结果与串行结果一致（cores/per_cpu/进程数/温度），且没有非预期 warning。
func TestConcurrentCollectAllMethods(t *testing.T) {
	t.Parallel()
	collector := newFixtureCollector(t)
	const workers, rounds = 8, 3
	var wg sync.WaitGroup
	errs := make(chan error, workers*rounds)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for round := 0; round < rounds; round++ {
				sys, err := collector.System(shortCtx(), monitor.SystemParams{
					PerCPU: true, HistoryPoints: 10, HistoryIntervalS: 1,
				})
				if err != nil {
					errs <- fmt.Errorf("worker %d system: %w", id, err)
					return
				}
				if sys.Schema != "monitor.system.v1" || sys.CPU.Cores != 2 || len(sys.CPU.PerCPU) != 2 {
					errs <- fmt.Errorf("worker %d system 结果损坏: schema=%q cores=%d per_cpu=%d",
						id, sys.Schema, sys.CPU.Cores, len(sys.CPU.PerCPU))
					return
				}
				if len(sys.Warnings) != 0 {
					errs <- fmt.Errorf("worker %d system 出现非预期 warning（共享状态被污染）: %v", id, sys.Warnings)
					return
				}
				procs, err := collector.Processes(shortCtx(), monitor.ProcessParams{Limit: 10, Sort: "mem"})
				if err != nil {
					errs <- fmt.Errorf("worker %d processes: %w", id, err)
					return
				}
				if procs.Total != 2 || len(procs.Items) != 2 || len(procs.Warnings) != 0 {
					errs <- fmt.Errorf("worker %d processes 结果损坏: total=%d items=%d warnings=%v",
						id, procs.Total, len(procs.Items), procs.Warnings)
					return
				}
				temp, err := collector.Temperature(shortCtx())
				if err != nil {
					errs <- fmt.Errorf("worker %d temperature: %w", id, err)
					return
				}
				if temp.Count != 1 || temp.Zones[0].TempC != 45 {
					errs <- fmt.Errorf("worker %d temperature 结果损坏: count=%d zones=%+v",
						id, temp.Count, temp.Zones)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// historyFileShape 是测试侧对 history.json 的最小解码形状。
type historyFileShape struct {
	Version   int   `json:"version"`
	IntervalS int64 `json:"interval_s"`
	Capacity  int   `json:"capacity"`
	Samples   []struct {
		TSUnixMS      int64   `json:"ts_unix_ms"`
		CPUPercent    float64 `json:"cpu_percent"`
		MemoryPercent float64 `json:"memory_percent"`
		Load1         float64 `json:"load1"`
		DiskPercent   float64 `json:"disk_percent"`
	} `json:"samples"`
}

// seedHistory 预置一个接近满容量的历史文件，让并发 append 触发大块原子重写，
// 从而把"非原子写/互斥失效"放大成可观测的破窗。
func seedHistory(t *testing.T, path string, capacity, intervalS int, end time.Time) {
	t.Helper()
	file := historyFileShape{Version: 1, IntervalS: int64(intervalS), Capacity: capacity}
	file.Samples = make([]struct {
		TSUnixMS      int64   `json:"ts_unix_ms"`
		CPUPercent    float64 `json:"cpu_percent"`
		MemoryPercent float64 `json:"memory_percent"`
		Load1         float64 `json:"load1"`
		DiskPercent   float64 `json:"disk_percent"`
	}, capacity)
	for i := range file.Samples {
		file.Samples[i].TSUnixMS = end.Add(-time.Duration(capacity-1-i) * time.Second).UnixMilli()
		file.Samples[i].MemoryPercent = 50
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("序列化 seed history 失败: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写入 seed history 失败: %v", err)
	}
}

// TestConcurrentHistoryWriteAndRead 让 4 个写者持续调用 System（每次都会读历史）
// 与 1 个读者持续读 history.json 并发 2.2s：
//   - 读者每次读到的内容必须通过 json.Valid（原子 rename 保证不会看到半截文件）；
//   - 终态每个采样间隔必须 ≥ interval_s*1000ms（append 判据被互斥串行化）。
func TestConcurrentHistoryWriteAndRead(t *testing.T) {
	requireLinux(t)
	procRoot, sysRoot := newFixture(t)
	stateDir := t.TempDir()
	t.Setenv("KORE_STATE_DIR", stateDir)
	collector := monitor.NewCollector(procRoot, sysRoot)

	historyPath := filepath.Join(stateDir, "history.json")
	const capacity, intervalS = 720, 1
	seedHistory(t, historyPath, capacity, intervalS, time.Now().Add(-5*time.Second))
	params := monitor.SystemParams{HistoryPoints: capacity, HistoryIntervalS: intervalS}

	stop := make(chan struct{})
	readerErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				readerErr <- nil
				return
			default:
			}
			data, err := os.ReadFile(historyPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				readerErr <- fmt.Errorf("读者读取 history.json 失败: %w", err)
				return
			}
			if len(data) == 0 || !json.Valid(data) {
				readerErr <- fmt.Errorf("读者读到半截/非法 history.json（%d 字节）：非原子写或互斥失效", len(data))
				return
			}
			runtime.Gosched()
		}
	}()

	const writers = 4
	deadline := time.Now().Add(2200 * time.Millisecond)
	var wg sync.WaitGroup
	writerErrs := make(chan error, writers)
	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				sys, err := collector.System(shortCtx(), params)
				if err != nil {
					writerErrs <- fmt.Errorf("writer %d system: %w", id, err)
					return
				}
				if sys.History.Schema != "monitor.history.v1" || sys.History.Capacity != capacity {
					writerErrs <- fmt.Errorf("writer %d history 元数据错误: schema=%q capacity=%d",
						id, sys.History.Schema, sys.History.Capacity)
					return
				}
				if sys.History.Count > capacity || len(sys.History.Timestamps) != sys.History.Count {
					writerErrs <- fmt.Errorf("writer %d history 尺寸不一致: count=%d timestamps=%d",
						id, sys.History.Count, len(sys.History.Timestamps))
					return
				}
			}
		}(writer)
	}
	wg.Wait()
	close(stop)
	if err := <-readerErr; err != nil {
		t.Fatal(err)
	}
	close(writerErrs)
	for err := range writerErrs {
		t.Error(err)
	}

	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("终态读取 history.json 失败: %v", err)
	}
	var file historyFileShape
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("终态 history.json 非法: %v", err)
	}
	if file.Version != 1 || file.IntervalS != int64(intervalS) || file.Capacity != capacity {
		t.Fatalf("终态元数据错误: version=%d interval_s=%d capacity=%d",
			file.Version, file.IntervalS, file.Capacity)
	}
	if len(file.Samples) != capacity {
		t.Fatalf("环形缓冲应保持 %d 个点，实际 %d", capacity, len(file.Samples))
	}
	for i := 1; i < len(file.Samples); i++ {
		gap := file.Samples[i].TSUnixMS - file.Samples[i-1].TSUnixMS
		if gap < 1000 {
			t.Fatalf("第 %d 个采样间隔 %dms < 1000ms：并发 append 未被串行化，历史曲线出现重复点", i, gap)
		}
	}
}

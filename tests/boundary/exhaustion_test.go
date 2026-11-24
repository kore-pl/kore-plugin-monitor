// 资源耗尽（resource exhaustion）类边界：用内核限额**真实注入**，不 mock 错误——
//   - RLIMIT_FSIZE（等价 ulimit -f）：历史曲线落盘必然 EFBIG（"file too large"）；
//   - RLIMIT_NOFILE（等价 ulimit -n）：/proc 读取必然 EMFILE（"too many open files"）。
//
// 断言插件降级为 warning + 部分结果：不 panic、不把写入/读取失败静默当成成功或 0。
//
// 为什么不用 /dev/full 或 tmpfs：插件唯一的写路径是 <state_dir>/history.json 的
// 「同目录临时文件 + rename」原子写，rename 会替换符号链接而不是写入 /dev/full，
// 无法在不 mount tmpfs（需额外权限）的前提下把写目标定向到满盘；ulimit -v 则会在
// Go 运行时预留地址空间阶段直接终止进程，测不到插件代码路径。二者换成同族、
// 可在测试进程内精确注入并恢复的内核限额（文件大小 / 打开文件数）。
package boundary

import (
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/pengtang1024/kore-plugin-monitor/internal/monitor"
)

// setRlimit 注入限额并在用例结束恢复（恢复失败要暴露，避免污染后续用例）。
func setRlimit(t *testing.T, resource int, low syscall.Rlimit) {
	t.Helper()
	var old syscall.Rlimit
	if err := syscall.Getrlimit(resource, &old); err != nil {
		t.Fatalf("读取 rlimit(%d) 失败: %v", resource, err)
	}
	if err := syscall.Setrlimit(resource, &low); err != nil {
		t.Fatalf("注入 rlimit(%d)=%+v 失败: %v", resource, low, err)
	}
	t.Cleanup(func() {
		if err := syscall.Setrlimit(resource, &old); err != nil {
			t.Errorf("恢复 rlimit(%d) 失败: %v", resource, err)
		}
	})
}

// TestResourceExhaustionHistoryWrite 用 RLIMIT_FSIZE 限制本进程可写文件大小，
// 让历史曲线落盘返回 EFBIG：断言采集仍成功、warning 明确、不留半截文件，
// 并在恢复限额后用同一 collector 反证写入成功。
func TestResourceExhaustionHistoryWrite(t *testing.T) {
	requireLinux(t)
	procRoot, sysRoot := newFixture(t)
	stateDir := t.TempDir()
	t.Setenv("KORE_STATE_DIR", stateDir)
	collector := monitor.NewCollector(procRoot, sysRoot)
	historyPath := filepath.Join(stateDir, "history.json")

	// 超过文件大小限额会触发 SIGXFSZ（默认终止进程）；忽略后写调用返回 EFBIG。
	signal.Ignore(syscall.SIGXFSZ)
	t.Cleanup(func() { signal.Reset(syscall.SIGXFSZ) })
	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		t.Fatalf("读取 RLIMIT_FSIZE 失败: %v", err)
	}
	setRlimit(t, syscall.RLIMIT_FSIZE, syscall.Rlimit{Cur: 64, Max: old.Max})

	result, err := collector.System(shortCtx(), monitor.SystemParams{HistoryPoints: 10, HistoryIntervalS: 1})
	if err != nil {
		t.Fatalf("写入受限不应让采集失败: %v", err)
	}
	assertSystemShape(t, result)
	if result.Memory.Total <= 0 || result.CPU.Cores != 2 {
		t.Fatalf("读路径不受 FSIZE 影响，应有真实读数: memory=%+v cpu=%+v", result.Memory, result.CPU)
	}
	requireWarning(t, result.Warnings, "保存历史曲线失败")
	requireWarning(t, result.Warnings, "file too large")
	if _, statErr := os.Stat(historyPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("写失败后不应留下 history.json（半截文件）: err=%v", statErr)
	}

	// 反证：恢复限额后同一路径必须写成功，证明失败来自注入而不是环境。
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		t.Fatalf("恢复 RLIMIT_FSIZE 失败: %v", err)
	}
	second, err := collector.System(shortCtx(), monitor.SystemParams{HistoryPoints: 10, HistoryIntervalS: 1})
	if err != nil {
		t.Fatalf("恢复限额后采集失败: %v", err)
	}
	requireNoWarning(t, second.Warnings, "保存历史曲线失败")
	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("恢复限额后应写出 history.json: %v", err)
	}
	var file historyFileShape
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("恢复限额后 history.json 非法: %v", err)
	}
	if file.Version != 1 || len(file.Samples) != 1 {
		t.Fatalf("恢复限额后应写入 1 个采样点: version=%d samples=%d", file.Version, len(file.Samples))
	}
}

// TestResourceExhaustionFileDescriptors 用 RLIMIT_NOFILE + 自占 fd 制造 EMFILE：
// 所有 /proc 读取都失败时，每个失败点都要有 warning，且不伪造读数；恢复后能正常采集。
func TestResourceExhaustionFileDescriptors(t *testing.T) {
	requireLinux(t)
	procRoot, sysRoot := newFixture(t)
	collector := monitor.NewCollector(procRoot, sysRoot)

	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &old); err != nil {
		t.Fatalf("读取 RLIMIT_NOFILE 失败: %v", err)
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Cur: 64, Max: old.Max}); err != nil {
		t.Fatalf("注入 RLIMIT_NOFILE 失败: %v", err)
	}
	var held []*os.File
	t.Cleanup(func() {
		for _, file := range held {
			_ = file.Close()
		}
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &old); err != nil {
			t.Errorf("恢复 RLIMIT_NOFILE 失败: %v", err)
		}
	})
	for i := 0; i < 512; i++ {
		file, err := os.Open(os.DevNull)
		if err != nil {
			break
		}
		held = append(held, file)
	}
	if _, err := os.ReadFile(filepath.Join(procRoot, "stat")); err == nil {
		t.Fatalf("fd 耗尽后仍能读 /proc fixture（已占 %d 个 fd），注入未生效", len(held))
	}

	result, err := collector.System(shortCtx(), monitor.SystemParams{})
	if err != nil {
		t.Fatalf("fd 耗尽不应让采集返回错误: %v", err)
	}
	assertSystemShape(t, result)
	if result.CPU.Cores != 0 || result.CPU.UsagePercent != 0 {
		t.Fatalf("读不到 /proc/stat 时不应伪造读数: %+v", result.CPU)
	}
	if len(result.Warnings) < 4 {
		t.Fatalf("每个读取失败都应有 warning（明确降级），实际 %d 条: %v",
			len(result.Warnings), result.Warnings)
	}
	requireWarning(t, result.Warnings, "读取 /proc/stat 失败")
	requireWarning(t, result.Warnings, "读取 /proc/meminfo 失败")
	requireWarning(t, result.Warnings, "too many open files")

	// 反证：释放 fd 并恢复限额后，同一 collector 必须正常采集。
	for _, file := range held {
		_ = file.Close()
	}
	held = nil
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &old); err != nil {
		t.Fatalf("恢复 RLIMIT_NOFILE 失败: %v", err)
	}
	recovered, err := collector.System(shortCtx(), monitor.SystemParams{})
	if err != nil {
		t.Fatalf("恢复限额后采集失败: %v", err)
	}
	if recovered.CPU.Cores != 2 || len(recovered.Warnings) != 0 {
		t.Fatalf("恢复限额后应正常采集: cores=%d warnings=%v", recovered.CPU.Cores, recovered.Warnings)
	}
}

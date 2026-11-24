// 权限不足（permission / EACCES）类边界：用真实 chmod 000 构造内核拒绝，
// 断言插件**明确降级**——返回可用的部分结果 + 指向失败原因的 warning，
// 而不是 panic、静默丢条目，或把"读不到"伪造成 0 读数。
package boundary

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pengtang1024/kore-plugin-monitor/internal/monitor"
)

// skipIfRoot 跳过 root：root 绕过 0000 权限，无法构造真实 EACCES。
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root 绕过 0000 权限，无法构造真实 EACCES")
	}
}

// chmodForTest 改权限并在用例结束恢复，保证 t.TempDir 清理不被 0000 卡住。
func chmodForTest(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s %v 失败: %v", path, mode, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, mode|0o700) })
}

// TestPermissionPIDStatUnreadable 覆盖 /proc/<pid> 无权访问：
// 其他进程照常返回，被拒的 pid 必须计入 warning 的"已跳过"。
func TestPermissionPIDStatUnreadable(t *testing.T) {
	requireLinux(t)
	skipIfRoot(t)
	procRoot, sysRoot := newFixture(t)
	writeTree(t, procRoot, map[string]string{
		"4321/stat":   "4321 (locked) S 1 1 1 0 -1 4194560 1 0 0 0 1 2 0 0 20 0 1 0 1 0\n",
		"4321/status": "Name:\tlocked\nVmRSS:\t 1024 kB\nThreads:\t1\n",
	})
	locked := filepath.Join(procRoot, "4321")
	chmodForTest(t, locked, 0)
	if _, err := os.ReadFile(filepath.Join(locked, "stat")); err == nil {
		t.Fatalf("chmod 000 后仍可读，权限注入未生效")
	}

	collector := monitor.NewCollector(procRoot, sysRoot)
	result, err := collector.Processes(shortCtx(), monitor.ProcessParams{Limit: 10, Sort: "cpu"})
	if err != nil {
		t.Fatalf("单个进程不可读不应让整次扫描失败: %v", err)
	}
	if result.Total != 2 || len(result.Items) != 2 {
		t.Fatalf("应只返回 2 个可读进程（1234/5678），得到 total=%d items=%d", result.Total, len(result.Items))
	}
	requireWarning(t, result.Warnings, "已跳过")
	requireWarning(t, result.Warnings, "不可读")
	for _, item := range result.Items {
		if item.PID == 4321 {
			t.Fatalf("不可读的 4321 不应出现在结果里: %+v", item)
		}
	}
}

// TestPermissionProcRootUnreadable 覆盖整个 /proc 根不可读：
// Processes 必须返回空列表 + "扫描 /proc 失败" warning，System 必须逐文件告警且保持零值。
func TestPermissionProcRootUnreadable(t *testing.T) {
	requireLinux(t)
	skipIfRoot(t)
	procRoot, sysRoot := newFixture(t)
	chmodForTest(t, procRoot, 0)
	if _, err := os.ReadDir(procRoot); err == nil {
		t.Fatalf("chmod 000 后仍可列目录，权限注入未生效")
	}

	collector := monitor.NewCollector(procRoot, sysRoot)
	processes, err := collector.Processes(shortCtx(), monitor.ProcessParams{Limit: 10, Sort: "cpu"})
	if err != nil {
		t.Fatalf("/proc 不可读时 Processes 应降级而不是返回错误: %v", err)
	}
	if processes.Total != 0 || len(processes.Items) != 0 {
		t.Fatalf("/proc 不可读时应返回空列表: total=%d items=%d", processes.Total, len(processes.Items))
	}
	requireWarning(t, processes.Warnings, "扫描 /proc 失败")

	system, err := collector.System(shortCtx(), monitor.SystemParams{})
	if err != nil {
		t.Fatalf("/proc 不可读时 System 应降级而不是返回错误: %v", err)
	}
	assertSystemShape(t, system)
	if system.CPU.Cores != 0 || system.CPU.UsagePercent != 0 {
		t.Fatalf("/proc/stat 不可读时不应伪造读数: %+v", system.CPU)
	}
	requireWarning(t, system.Warnings, "读取 /proc/stat 失败")
	requireWarning(t, system.Warnings, "读取 /proc/meminfo 失败")
	requireWarning(t, system.Warnings, "读取 /proc/loadavg 失败")
	requireWarning(t, system.Warnings, "读取 /proc/uptime 失败")
	if len(system.Warnings) < 4 {
		t.Fatalf("每个不可读的 /proc 文件都应有独立 warning，实际 %d 条: %v",
			len(system.Warnings), system.Warnings)
	}
}

// TestPermissionStateDirDenied 覆盖状态目录不可写/不可读：
// 历史曲线读写失败必须各给 warning，采集结果本身仍正常返回。
func TestPermissionStateDirDenied(t *testing.T) {
	requireLinux(t)
	skipIfRoot(t)
	procRoot, sysRoot := newFixture(t)
	stateDir := t.TempDir()
	chmodForTest(t, stateDir, 0)
	t.Setenv("KORE_STATE_DIR", stateDir)
	collector := monitor.NewCollector(procRoot, sysRoot)

	result, err := collector.System(shortCtx(), monitor.SystemParams{HistoryPoints: 10, HistoryIntervalS: 1})
	if err != nil {
		t.Fatalf("状态目录不可用不应让采集失败: %v", err)
	}
	assertSystemShape(t, result)
	if result.Memory.Total <= 0 || result.CPU.Cores != 2 {
		t.Fatalf("历史曲线失败不应影响 /proc 读数: memory=%+v cpu=%+v", result.Memory, result.CPU)
	}
	requireWarning(t, result.Warnings, "读取历史曲线")
	requireWarning(t, result.Warnings, "保存历史曲线失败")
	requireWarning(t, result.Warnings, "permission denied")
}

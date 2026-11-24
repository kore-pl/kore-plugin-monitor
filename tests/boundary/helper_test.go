// Package boundary 覆盖 production 判据①（docs/PLUGIN-CATALOG.md:90）要求的四类边界：
// 异常输入、并发、权限不足、资源耗尽各至少一例，且全部通过插件对外入口
// （monitor.Call / monitor.NewCollector）驱动，不依赖未导出的内部字段。
//
// 设计约束：
//   - 每类断言都必须能因实现退化而失败（校验被删、降级 warning 丢失、
//     互斥/原子写失效、限额错误被吞），不做"永远通过"的形状检查；
//   - fixture 只注入 /proc 与 /sys 根目录，生产执行面仍走真实解析与采集代码；
//   - 资源限额用内核强制注入（RLIMIT_FSIZE / RLIMIT_NOFILE），不 mock 错误。
package boundary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-monitor/internal/monitor"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// requireLinux 在非 Linux 跳过：实现按设计显式返回 E_UNSUPPORTED，不做真实采集。
func requireLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("monitor 的真实 /proc 采集只在 Linux 上可用")
	}
}

// writeTree 在 root 下写入 name→content，自动建父目录（0644）。
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("创建 fixture 目录 %s 失败: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("写入 fixture %s 失败: %v", name, err)
		}
	}
}

// validProcFiles 是最小但合法的 /proc fixture：两次读取内容相同，CPU 差分为 0。
func validProcFiles() map[string]string {
	return map[string]string{
		"stat":    "cpu  1000 100 500 8000 200 0 50 0 0 0\ncpu0 500 50 250 4000 100 0 25 0 0 0\ncpu1 500 50 250 4000 100 0 25 0 0 0\n",
		"meminfo": "MemTotal:       2048000 kB\nMemFree:         800000 kB\nMemAvailable:   1024000 kB\nBuffers:          20000 kB\nCached:          200000 kB\nSwapTotal:      1024000 kB\nSwapFree:       1024000 kB\n",
		"loadavg": "0.50 0.40 0.30 1/100 12345\n",
		"uptime":  "3600.50 7200.25\n",
		"net/dev": "Inter-| Receive | Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n" +
			"  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0\n",
		"mounts":      "proc /proc proc rw 0 0\n/dev/fake0 / ext4 rw 0 0\n",
		"diskstats":   "8 0 fake0 100 0 800 0 50 0 400 0 0 0 0\n",
		"1234/stat":   "1234 (fake-proc) S 1 1 1 0 -1 4194560 100 0 0 0 10 20 0 0 20 0 3 0 100 0\n",
		"1234/status": "Name:\tfake-proc\nVmRSS:\t  2048 kB\nThreads:\t3\n",
		"5678/stat":   "5678 (other-proc) R 1 1 1 0 -1 4194560 100 0 0 0 30 40 0 0 20 0 5 0 200 0\n",
		"5678/status": "Name:\tother-proc\nVmRSS:\t  4096 kB\nThreads:\t5\n",
	}
}

// validSysFiles 是带一个 thermal zone 的 /sys fixture。
func validSysFiles() map[string]string {
	return map[string]string{
		"class/thermal/thermal_zone0/temp": "45000\n",
		"class/thermal/thermal_zone0/type": "x86_pkg_temp\n",
	}
}

// newFixture 建一棵假 /proc + /sys 并返回两个根目录。
func newFixture(t *testing.T) (procRoot, sysRoot string) {
	t.Helper()
	procRoot, sysRoot = t.TempDir(), t.TempDir()
	writeTree(t, procRoot, validProcFiles())
	writeTree(t, sysRoot, validSysFiles())
	return procRoot, sysRoot
}

// newFixtureCollector 用合法 fixture 构造采集器；env 不参与，可供并行用例使用。
func newFixtureCollector(t *testing.T) *monitor.Collector {
	t.Helper()
	procRoot, sysRoot := newFixture(t)
	return monitor.NewCollector(procRoot, sysRoot)
}

// shortCtx 用即将到期的 deadline 把采样窗压到 ~20ms：
// 走的是实现的真实 deadline 分支，不依赖未导出的 interval 字段。
func shortCtx() monitor.Context {
	return monitor.Context{Deadline: time.Now().Add(40 * time.Millisecond).UnixNano()}
}

// callRaw 调插件统一入口，返回原始结果与协议错误。
func callRaw(t *testing.T, method, params string, exec monitor.Executor, ctx monitor.Context) (json.RawMessage, *pluginapi.Error) {
	t.Helper()
	return monitor.Call(method, json.RawMessage(params), exec, ctx)
}

// decodeInto 把结果解码到 out，失败即测试失败。
func decodeInto[T any](t *testing.T, raw json.RawMessage, out *T) {
	t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("结果解码失败: %v（原始=%s）", err, raw)
	}
}

// hasWarning 判断 warnings 中是否有包含 substr 的条目。
func hasWarning(warnings []string, substr string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, substr) {
			return true
		}
	}
	return false
}

// requireWarning 断言存在一条包含 substr 的 warning（明确降级，而非静默 0）。
func requireWarning(t *testing.T, warnings []string, substr string) {
	t.Helper()
	if !hasWarning(warnings, substr) {
		t.Fatalf("缺少含 %q 的 warning；实际 warnings=%v", substr, warnings)
	}
}

// requireNoWarning 断言没有包含 substr 的 warning（反证注入确实解除）。
func requireNoWarning(t *testing.T, warnings []string, substr string) {
	t.Helper()
	if hasWarning(warnings, substr) {
		t.Fatalf("不应出现含 %q 的 warning；实际 warnings=%v", substr, warnings)
	}
}

// assertSystemShape 断言系统结果的基本形状（schema 固定、数组非 null）。
func assertSystemShape(t *testing.T, result monitor.SystemResult) {
	t.Helper()
	if result.Schema != "monitor.system.v1" || result.Source != "proc" {
		t.Fatalf("schema/source 不符: schema=%q source=%q", result.Schema, result.Source)
	}
	if result.Disks == nil || result.Network == nil || result.Warnings == nil {
		t.Fatalf("数组字段不应为 nil: disks=%v network=%v warnings=%v",
			result.Disks, result.Network, result.Warnings)
	}
}

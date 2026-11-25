//go:build linux

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
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/pengtang1024/kore-plugin-monitor/internal/monitor"
)

// dropInheritedFDs 关闭从外层 shell / make 链继承来的 fd（fd ≥ 3 且未置 FD_CLOEXEC）。
//
// 预算按「自身已开 fd + headroom」计算，继承 fd 虽已计入总数，却会挤占 Go runtime /
// race detector 可能懒创建的余量；外层嵌套越深（make check → sh → make → go test），
// 继承 fd 越多，余量越不可预测。关掉之后 fd 表只由本进程决定，预算与宿主继承 fd、
// shell 层数彻底解耦。Go 自己开的 fd 一律带 FD_CLOEXEC，不会被误关。
func dropInheritedFDs(t *testing.T) {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("读取自身 fd 表失败: %v", err)
	}
	dropped := 0
	for _, ent := range ents {
		fd, convErr := strconv.Atoi(ent.Name())
		if convErr != nil || fd < 3 {
			continue
		}
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
		if errno != 0 || flags&syscall.FD_CLOEXEC != 0 {
			continue
		}
		if closeErr := syscall.Close(fd); closeErr == nil {
			dropped++
		}
	}
	t.Logf("关闭继承 fd %d 个（预算只按本进程 fd 表计算）", dropped)
}

// fsizeChildEnv 标记"当前是被父用例拉起的子进程"：子进程只跑注入逻辑，
// 不再递归拉子进程。
const fsizeChildEnv = "MONITOR_FSIZE_CHILD"

// fsizeChildResult 是子进程回给父进程的观测值（只含断言需要的字段）。
type fsizeChildResult struct {
	Err            string   `json:"err"`
	SetLimitErr    string   `json:"set_limit_err"`
	CollectErr     string   `json:"collect_err"`
	MemoryTotal    int64    `json:"memory_total"`
	Cores          int      `json:"cores"`
	Warnings       []string `json:"warnings"`
	HistoryLeft    bool     `json:"history_left"`
	RestoreErr     string   `json:"restore_err"`
	RecoverErr     string   `json:"recover_err"`
	RecoverWarn    []string `json:"recover_warnings"`
	HistoryVersion int      `json:"history_version"`
	HistorySamples int      `json:"history_samples"`
	ReadErr        string   `json:"read_err"`
}

// TestResourceExhaustionHistoryWrite 用 RLIMIT_FSIZE 限制可写文件大小，
// 让历史曲线落盘返回 EFBIG：断言采集仍成功、warning 明确、不留半截文件，
// 并在恢复限额后用同一 collector 反证写入成功。
//
// 注入放在子进程里：RLIMIT_FSIZE 是进程级限额，会连带限制 testing 自身对
// -test.testlogfile 的写入（go test 以 4KB 块落盘），在进程内注入时偶发把 testlog
// 撑爆，绿用例变成 "testing: can't write ...: file too large"（实测 20 次里坏 2~3 次）。
// 子进程不带 -test.testlogfile，stdout 是管道（不受文件大小限额约束），
// 因此可以稳定制造真实 EFBIG 而不污染测试框架。
//
// 子进程用同一个测试二进制自执行（os.Args[0] + -test.run），不引入额外构建产物。
func TestResourceExhaustionHistoryWrite(t *testing.T) {
	if os.Getenv(fsizeChildEnv) == "1" {
		runFSIZEHistoryChild(t)
		return
	}
	requireLinux(t)
	procRoot, sysRoot := newFixture(t)
	stateDir := t.TempDir()

	cmd := exec.Command(os.Args[0], "-test.run=^TestResourceExhaustionHistoryWrite$", "-test.v=false")
	cmd.Env = append(os.Environ(),
		fsizeChildEnv+"=1",
		"KORE_STATE_DIR="+stateDir,
		"MONITOR_FSIZE_PROC="+procRoot,
		"MONITOR_FSIZE_SYS="+sysRoot,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("子进程执行失败: %v\n%s", err, out)
	}
	got, ok := parseFSIZEChild(out)
	if !ok {
		t.Fatalf("子进程未输出观测结果:\n%s", out)
	}
	if got.Err != "" || got.SetLimitErr != "" {
		t.Fatalf("子进程注入失败: err=%q set_limit=%q", got.Err, got.SetLimitErr)
	}
	if got.CollectErr != "" {
		t.Fatalf("写入受限不应让采集失败: %s", got.CollectErr)
	}
	if got.MemoryTotal <= 0 || got.Cores != 2 {
		t.Fatalf("读路径不受 FSIZE 影响，应有真实读数: memory_total=%d cores=%d", got.MemoryTotal, got.Cores)
	}
	requireWarning(t, got.Warnings, "保存历史曲线失败")
	requireWarning(t, got.Warnings, "file too large")
	if got.HistoryLeft {
		t.Fatal("写失败后不应留下 history.json（半截文件）")
	}
	// 反证：恢复限额后同一路径必须写成功，证明失败来自注入而不是环境。
	if got.RestoreErr != "" {
		t.Fatalf("恢复 RLIMIT_FSIZE 失败: %s", got.RestoreErr)
	}
	if got.RecoverErr != "" {
		t.Fatalf("恢复限额后采集失败: %s", got.RecoverErr)
	}
	requireNoWarning(t, got.RecoverWarn, "保存历史曲线失败")
	if got.ReadErr != "" {
		t.Fatalf("恢复限额后应写出 history.json: %s", got.ReadErr)
	}
	if got.HistoryVersion != 1 || got.HistorySamples != 1 {
		t.Fatalf("恢复限额后应写入 1 个采样点: version=%d samples=%d", got.HistoryVersion, got.HistorySamples)
	}
}

// parseFSIZEChild 从子进程输出里取出 fsizeChildResult（前缀标记行，避免和其它 PASS 输出混淆）。
func parseFSIZEChild(out []byte) (fsizeChildResult, bool) {
	const marker = "FSIZE_CHILD_RESULT "
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, marker) {
			continue
		}
		var got fsizeChildResult
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, marker)), &got); err != nil {
			return fsizeChildResult{}, false
		}
		return got, true
	}
	return fsizeChildResult{}, false
}

// runFSIZEHistoryChild 是子进程侧实现：注入 64 字节限额 → 采集 → 恢复 → 反证写入。
// 所有失败都写进结构体回给父进程，不在子进程里 Fatalf（避免父进程只看到退出码）。
func runFSIZEHistoryChild(t *testing.T) {
	procRoot, sysRoot := os.Getenv("MONITOR_FSIZE_PROC"), os.Getenv("MONITOR_FSIZE_SYS")
	historyPath := filepath.Join(os.Getenv("KORE_STATE_DIR"), "history.json")
	got := fsizeChildResult{}
	// 超过文件大小限额会触发 SIGXFSZ（默认终止进程）；忽略后写调用返回 EFBIG。
	signal.Ignore(syscall.SIGXFSZ)
	defer signal.Reset(syscall.SIGXFSZ)
	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		got.Err = "读取 RLIMIT_FSIZE 失败: " + err.Error()
		emitFSIZEChild(got)
		return
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 64, Max: old.Max}); err != nil {
		got.SetLimitErr = err.Error()
		emitFSIZEChild(got)
		return
	}
	collector := monitor.NewCollector(procRoot, sysRoot)
	result, err := collector.System(shortCtx(), monitor.SystemParams{HistoryPoints: 10, HistoryIntervalS: 1})
	if err != nil {
		got.CollectErr = err.Error()
	}
	got.MemoryTotal = result.Memory.Total
	got.Cores = result.CPU.Cores
	got.Warnings = result.Warnings
	if _, statErr := os.Stat(historyPath); statErr == nil {
		got.HistoryLeft = true
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		got.RestoreErr = err.Error()
		emitFSIZEChild(got)
		return
	}
	second, err := collector.System(shortCtx(), monitor.SystemParams{HistoryPoints: 10, HistoryIntervalS: 1})
	if err != nil {
		got.RecoverErr = err.Error()
	}
	got.RecoverWarn = second.Warnings
	data, err := os.ReadFile(historyPath)
	if err != nil {
		got.ReadErr = err.Error()
		emitFSIZEChild(got)
		return
	}
	var file historyFileShape
	if err := json.Unmarshal(data, &file); err != nil {
		got.ReadErr = "history.json 非法: " + err.Error()
		emitFSIZEChild(got)
		return
	}
	got.HistoryVersion = file.Version
	got.HistorySamples = len(file.Samples)
	emitFSIZEChild(got)
}

// emitFSIZEChild 打印父进程可解析的单行观测结果。
func emitFSIZEChild(got fsizeChildResult) {
	data, err := json.Marshal(got)
	if err != nil {
		fmt.Println("FSIZE_CHILD_MARSHAL_FAIL")
		return
	}
	fmt.Println("FSIZE_CHILD_RESULT " + string(data))
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
	// 预算相对自身已开 fd（不写死 Cur:64）：本用例在本进程内注入，go test / race 运行时
	// 自身占的 fd 必须先剔除，再用「当前 fd 数 + 余量」当预算，否则父进程继承的多寡会
	// 直接决定"自占 fd"这一步是否真的发生（实测：继承 60 个 fd 时 64 的预算一个都开不出来，
	// 用例仍绿，但已经不再证明自己耗尽了 fd）。
	dropInheritedFDs(t)
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("读取自身 fd 表失败: %v", err)
	}
	const headroom = 8
	budget := uint64(len(ents)) + headroom
	if budget > old.Max {
		t.Fatalf("预算 %d 超过外层硬限 %d：环境把 RLIMIT_NOFILE 压得过低", budget, old.Max)
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Cur: budget, Max: old.Max}); err != nil {
		t.Fatalf("收紧 RLIMIT_NOFILE 到 %d 失败: %v", budget, err)
	}
	var injected syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &injected); err != nil {
		t.Fatalf("读取 RLIMIT_NOFILE 失败: %v", err)
	}
	if injected.Cur != budget {
		t.Fatalf("RLIMIT_NOFILE 回读=%d，期望预算 %d（预算未生效）", injected.Cur, budget)
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
	for i := 0; i < 4096; i++ {
		file, err := os.Open(os.DevNull)
		if err != nil {
			// 正控：只有 EMFILE 才算真的耗尽；其它错误说明预算没生效。
			if !errors.Is(err, syscall.EMFILE) {
				t.Fatalf("耗尽 fd 时 open 失败原因不是 EMFILE: %v", err)
			}
			break
		}
		held = append(held, file)
	}
	if len(held) == 0 {
		t.Fatalf("RLIMIT_NOFILE=%d 下没有任何空位被耗尽，预算未生效", budget)
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

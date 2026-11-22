package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestBinarySystemRSSIncrement 以真实插件进程跑一次 monitor.system：
// 握手 → 读基线 VmRSS → 调用 → 读调用后 VmRSS，断言单次增量 ≤2 MiB（lazy，不常驻采样）。
func TestBinarySystemRSSIncrement(t *testing.T) {
	requireLinux(t)
	root := filepath.Clean(filepath.Join("..", ".."))
	binPath := filepath.Join(t.TempDir(), "monitor")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binPath, ".")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("构建插件二进制失败: %v\n%s", err, out)
	}
	socketPath := filepath.Join(t.TempDir(), "plugin.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("监听 unix socket 失败: %v", err)
	}
	defer func() { _ = listener.Close() }()
	child := exec.Command(binPath)
	child.Env = append(os.Environ(), "KORE_PLUGIN_SOCK="+socketPath, "KORE_PLUGIN_ID=monitor",
		"KORE_PLUGIN_TOKEN=test-token", "KORE_LOG_LEVEL=error")
	var childLog strings.Builder
	child.Stderr = &childLog
	if err := child.Start(); err != nil {
		t.Fatalf("启动插件失败: %v", err)
	}
	defer func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	}()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("接受插件连接失败: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	request := readRequest(t, conn)
	if request.Method != "kore.hello" {
		t.Fatalf("首帧 method=%q，期望 kore.hello（stderr=%s）", request.Method, childLog.String())
	}
	writeResponse(t, conn, request, json.RawMessage(`{"ok":true,"protocol":"kore.plugin.v1"}`))
	before := readPIDRSS(t, child.Process.Pid)
	callReq := &pluginapi.Request{
		Type: pluginapi.TypeRequest, ID: randomID(t), ReqID: "r-rss",
		Method: "monitor.system", Params: json.RawMessage(`{}`), TimeoutMS: 15000,
	}
	if err := pluginapi.WriteJSON(conn, callReq); err != nil {
		t.Fatalf("发送 monitor.system 失败: %v", err)
	}
	response := readResponse(t, conn)
	if response.Error != nil {
		t.Fatalf("monitor.system 返回错误: %+v", response.Error)
	}
	var result struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil || result.Source != "proc" {
		t.Fatalf("结果不合法: %s err=%v", response.Result, err)
	}
	after := readPIDRSS(t, child.Process.Pid)
	delta := after - before
	t.Logf("插件进程单次 monitor.system RSS: %d KiB → %d KiB（Δ=%d KiB）", before, after, delta)
	if delta > 2048 {
		t.Fatalf("单次 monitor.system RSS 增量 %d KiB 超过 2 MiB", delta)
	}
	shutdown := &pluginapi.Request{Type: pluginapi.TypeRequest, ID: randomID(t), ReqID: "r-shutdown", Method: "kore.shutdown"}
	if err := pluginapi.WriteJSON(conn, shutdown); err != nil {
		t.Fatalf("发送 shutdown 失败: %v", err)
	}
	if got := readResponse(t, conn); got.Error != nil {
		t.Fatalf("shutdown 返回错误: %+v", got.Error)
	}
}

// readRequest 读一帧并断言是 request。
func readRequest(t *testing.T, conn net.Conn) *pluginapi.Request {
	t.Helper()
	payload, err := pluginapi.ReadFrame(conn, pluginapi.MaxFrameSize)
	if err != nil {
		t.Fatalf("读帧失败: %v", err)
	}
	env, err := pluginapi.DecodeFrame(payload)
	if err != nil {
		t.Fatalf("解码帧失败: %v", err)
	}
	request, ok := env.(*pluginapi.Request)
	if !ok {
		t.Fatalf("帧类型 %T，期望 request", env)
	}
	return request
}

// readResponse 读一帧并断言是 response。
func readResponse(t *testing.T, conn net.Conn) *pluginapi.Response {
	t.Helper()
	payload, err := pluginapi.ReadFrame(conn, pluginapi.MaxFrameSize)
	if err != nil {
		t.Fatalf("读帧失败: %v", err)
	}
	env, err := pluginapi.DecodeFrame(payload)
	if err != nil {
		t.Fatalf("解码帧失败: %v", err)
	}
	response, ok := env.(*pluginapi.Response)
	if !ok {
		t.Fatalf("帧类型 %T，期望 response", env)
	}
	return response
}

// writeResponse 回填 request 的 id/req_id。
func writeResponse(t *testing.T, conn net.Conn, request *pluginapi.Request, result json.RawMessage) {
	t.Helper()
	response := &pluginapi.Response{Type: pluginapi.TypeResponse, ID: request.ID, ReqID: request.ReqID, Result: result}
	if err := pluginapi.WriteJSON(conn, response); err != nil {
		t.Fatalf("写响应失败: %v", err)
	}
}

// randomID 生成 32 位小写 hex 请求 id。
func randomID(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("生成请求 id 失败: %v", err)
	}
	return hex.EncodeToString(buf)
}

// readPIDRSS 读取指定进程的 VmRSS（KiB）。
func readPIDRSS(t *testing.T, pid int) int64 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		t.Fatalf("读取子进程 status 失败: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "VmRSS:"))
		if len(fields) == 0 {
			break
		}
		value, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			t.Fatalf("解析 VmRSS 失败: %v", err)
		}
		return value
	}
	t.Fatal("未找到 VmRSS")
	return 0
}

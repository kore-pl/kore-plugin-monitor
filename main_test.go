package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

func testConfig() config {
	return config{
		id:           "monitor",
		version:      "0.2.0",
		requiresKore: ">=0.1.0 <0.2.0",
		capabilities: []string{"system:read"},
		startedAt:    time.Now(),
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func readFrame(t *testing.T, conn net.Conn) any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	payload, err := pluginapi.ReadFrame(conn, pluginapi.MaxFrameSize)
	if err != nil {
		t.Fatalf("读帧失败: %v", err)
	}
	env, err := pluginapi.DecodeFrame(payload)
	if err != nil {
		t.Fatalf("解码帧失败: %v", err)
	}
	return env
}

func writeRequest(t *testing.T, conn net.Conn, method, reqID string, params json.RawMessage) {
	t.Helper()
	req := &pluginapi.Request{Type: pluginapi.TypeRequest, ID: "id-" + reqID, ReqID: reqID, Method: method, Params: params}
	if err := pluginapi.WriteJSON(conn, req); err != nil {
		t.Fatalf("写请求失败: %v", err)
	}
}

func invoke(t *testing.T, method, reqID string, params json.RawMessage) *pluginapi.Response {
	t.Helper()
	pluginConn, kernelConn := net.Pipe()
	defer func() { _ = kernelConn.Close() }()
	done := make(chan error, 1)
	go func() {
		done <- serve(pluginConn, testConfig(), testLogger())
		_ = pluginConn.Close()
	}()
	writeRequest(t, kernelConn, method, reqID, params)
	env := readFrame(t, kernelConn)
	response, ok := env.(*pluginapi.Response)
	if !ok {
		t.Fatalf("帧类型是 %T，不是 response", env)
	}
	writeRequest(t, kernelConn, "kore.shutdown", "r-shutdown", nil)
	if shutdown := readFrame(t, kernelConn).(*pluginapi.Response); string(shutdown.Result) != `{"ok":true}` {
		t.Fatalf("shutdown 结果=%s", shutdown.Result)
	}
	if err := <-done; err != nil {
		t.Fatalf("serve 返回错误: %v", err)
	}
	return response
}

// TestHandshake 校验 hello 首帧字段与三步握手。
func TestHandshake(t *testing.T) {
	pluginConn, kernelConn := net.Pipe()
	defer func() { _ = kernelConn.Close() }()
	done := make(chan error, 1)
	go func() { done <- handshake(pluginConn, testConfig()); _ = pluginConn.Close() }()

	env := readFrame(t, kernelConn)
	request, ok := env.(*pluginapi.Request)
	if !ok || request.Method != helloMethod {
		t.Fatalf("首帧=%v，期望 kore.hello request", env)
	}
	var params helloParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatalf("解析 hello 参数失败: %v", err)
	}
	if params.PluginID != "monitor" || params.Protocol != pluginapi.Protocol || params.PID != os.Getpid() {
		t.Fatalf("hello 参数不符: %+v", params)
	}
	result, _ := json.Marshal(helloResult{OK: true, Protocol: pluginapi.Protocol})
	resp := &pluginapi.Response{Type: pluginapi.TypeResponse, ID: request.ID, ReqID: request.ReqID, Result: result}
	if err := pluginapi.WriteJSON(kernelConn, resp); err != nil {
		t.Fatalf("写握手响应失败: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("握手返回错误: %v", err)
	}
}

// TestSystemOK 无故障时 monitor.system 返回真实 /proc 采集结果，且 req_id 回显。
func TestSystemOK(t *testing.T) {
	for _, method := range []string{"monitor.system", "system"} {
		r := invoke(t, method, "r-1", json.RawMessage(`{"per_cpu":true}`))
		if r.Error != nil || r.ReqID != "r-1" {
			t.Fatalf("%s error=%v req_id=%q", method, r.Error, r.ReqID)
		}
		for _, want := range []string{`"source":"proc"`, `"usage_percent"`, `"total"`, `"load1"`, `"uptime_s"`, `"disks"`, `"network"`} {
			if !strings.Contains(string(r.Result), want) {
				t.Fatalf("%s 结果缺字段 %s: %s", method, want, r.Result)
			}
		}
	}
}

// TestProcessesAndTemperatureOK 校验 top N 进程与 thermal 结果的基本形状。
func TestProcessesAndTemperatureOK(t *testing.T) {
	r := invoke(t, "monitor.processes", "r-2", json.RawMessage(`{"limit":5,"sort":"cpu"}`))
	if r.Error != nil || !strings.Contains(string(r.Result), `"items"`) || !strings.Contains(string(r.Result), `"cpu_percent"`) {
		t.Fatalf("processes error=%v result=%s", r.Error, r.Result)
	}
	r = invoke(t, "monitor.temperature", "r-3", nil)
	if r.Error != nil || !strings.Contains(string(r.Result), `"available"`) || !strings.Contains(string(r.Result), `"zones"`) {
		t.Fatalf("temperature error=%v result=%s", r.Error, r.Result)
	}
}

// TestInvalidParams 非法 limit / 相对路径 / 非对象参数返回 E_INVALID。
func TestInvalidParams(t *testing.T) {
	bad := []struct {
		method string
		params string
		want   string
	}{
		{"monitor.processes", `{"limit":0}`, "limit"},
		{"monitor.processes", `{"sort":"pid"}`, "sort"},
		{"monitor.disks", `{"path":"relative/path"}`, "绝对路径"},
		{"monitor.system", `"x"`, "JSON 对象"},
	}
	for _, tc := range bad {
		r := invoke(t, tc.method, "r-4", json.RawMessage(tc.params))
		if r.Error == nil || r.Error.Code != pluginapi.E_INVALID || !strings.Contains(r.Error.Message, tc.want) {
			t.Fatalf("params=%s error=%v，期望 E_INVALID 含 %q", tc.params, r.Error, tc.want)
		}
	}
}

// TestUnknownMethod 未知方法回 E_NOTFOUND。
func TestUnknownMethod(t *testing.T) {
	r := invoke(t, "monitor.nope", "r-4", nil)
	if r.Error == nil || r.Error.Code != pluginapi.E_NOTFOUND {
		t.Fatalf("error=%v，期望 E_NOTFOUND", r.Error)
	}
}

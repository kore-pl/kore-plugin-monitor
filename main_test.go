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
		version:      "0.1.0",
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

// TestSampleOK 无故障时 sample 返回结构化占位结果，且 req_id 回显。
func TestSampleOK(t *testing.T) {
	for _, method := range []string{"monitor.sample", "sample"} {
		r := invoke(t, method, "r-1", json.RawMessage(`{"metrics":["cpu"]}`))
		if r.Error != nil || r.ReqID != "r-1" {
			t.Fatalf("%s error=%v req_id=%q", method, r.Error, r.ReqID)
		}
		if !strings.Contains(string(r.Result), `"source":"placeholder"`) || !strings.Contains(string(r.Result), `"cpu"`) {
			t.Fatalf("sample 结果非结构化占位: %s", r.Result)
		}
	}
}

// TestAlertOK 合法阈值范围内的告警配置成功；非法阈值/枚举返回 E_INVALID。
func TestAlertOK(t *testing.T) {
	r := invoke(t, "monitor.alert.set", "r-2", json.RawMessage(`{"rule_id":"r1","metric":"cpu","comparator":"gt","threshold":85,"enabled":true}`))
	if r.Error != nil || !strings.Contains(string(r.Result), `"applied":true`) {
		t.Fatalf("alert.set error=%v result=%s", r.Error, r.Result)
	}
	bad := []struct {
		params string
		want   string
	}{
		{`{"rule_id":"r1","metric":"cpu","comparator":"gt","threshold":101}`, "越界"},
		{`{"rule_id":"r1","metric":"cpu","comparator":"eq","threshold":50}`, "comparator"},
		{`{"rule_id":"r1","metric":"cpu","comparator":"gt"}`, "threshold"},
		{`"x"`, "JSON 对象"},
	}
	for _, tc := range bad {
		r := invoke(t, "monitor.alert.set", "r-3", json.RawMessage(tc.params))
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

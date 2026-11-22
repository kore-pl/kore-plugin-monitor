package main

import (
	"encoding/json"
	"log/slog"
	"runtime"
	"strings"
	"time"

	"github.com/pengtang1024/kore-plugin-monitor/internal/monitor"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// statusResult 是 kore.ping 的结果（PLUGIN-PROTOCOL §7.1）。
type statusResult struct {
	Status     string `json:"status"`
	UptimeS    int64  `json:"uptime_s"`
	Goroutines int    `json:"goroutines"`
}

// dispatch 把请求分发为响应帧；第二个返回值表示收到 kore.shutdown。
func dispatch(request *pluginapi.Request, cfg config, logger *slog.Logger) (*pluginapi.Response, bool) {
	response := &pluginapi.Response{Type: pluginapi.TypeResponse, ID: request.ID, ReqID: request.ReqID}
	switch request.Method {
	case "kore.shutdown":
		response.Result = json.RawMessage(`{"ok":true}`)
		logger.Info("收到 kore.shutdown，优雅退出")
		return response, true
	case "kore.ping":
		response.Result = pingResult(cfg)
		return response, false
	case "kore.reload":
		response.Result = json.RawMessage(`{"ok":true,"config_digest":""}`)
		return response, false
	}
	method, ok := normalizeMethod(request.Method, cfg.id)
	if !ok {
		response.Error = notFound(request.Method)
		return response, false
	}
	ctx := monitor.Context{}
	if request.TimeoutMS > 0 {
		ctx.Deadline = time.Now().Add(time.Duration(request.TimeoutMS) * time.Millisecond).UnixNano()
	}
	result, perr := monitor.Call(method, request.Params, monitor.NewExecutor(), ctx)
	if perr != nil {
		response.Error = perr
		return response, false
	}
	response.Result = result
	return response, false
}

// normalizeMethod 把裸名 / 全名统一归一为 monitor.<verb>，非业务方法返回 false。
func normalizeMethod(method, id string) (string, bool) {
	switch method {
	case "monitor.sample", "monitor.alert.set":
		return method, true
	}
	name := strings.TrimPrefix(method, id+".")
	switch name {
	case "sample":
		return "monitor.sample", true
	case "alert.set":
		return "monitor.alert.set", true
	default:
		return "", false
	}
}

// pingResult 组装 kore.ping 结果。
func pingResult(cfg config) json.RawMessage {
	b, _ := json.Marshal(statusResult{
		Status:     "ready",
		UptimeS:    int64(time.Since(cfg.startedAt).Seconds()),
		Goroutines: runtime.NumGoroutine(),
	})
	return b
}

// notFound 构造 E_NOTFOUND 错误体（§9）。
func notFound(method string) *pluginapi.Error {
	return &pluginapi.Error{Code: pluginapi.E_NOTFOUND, Message: "未知方法: " + method}
}

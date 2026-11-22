package monitor

import (
	"encoding/json"
	"fmt"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// Call 是方法级统一入口：校验参数 → 调执行面 → 编码结构化结果。
// 执行面返回的错误统一映射为 E_UPSTREAM，保证四类故障都走方法级错误返回。
func Call(method string, raw json.RawMessage, exec Executor, ctx Context) (json.RawMessage, *pluginapi.Error) {
	if exec == nil {
		exec = NewExecutor()
	}
	switch method {
	case "monitor.sample":
		return callSample(raw, exec, ctx)
	case "monitor.alert.set":
		return callAlert(raw, exec, ctx)
	default:
		return nil, &pluginapi.Error{Code: pluginapi.E_NOTFOUND, Message: "未知方法: " + method}
	}
}

// callSample 校验 sample 入参并返回采样结果。
func callSample(raw json.RawMessage, exec Executor, ctx Context) (json.RawMessage, *pluginapi.Error) {
	p, err := parseSampleParams(raw)
	if err != nil {
		return nil, invalid(err.Error())
	}
	result, execErr := exec.Sample(ctx, p.Metrics)
	if execErr != nil {
		return nil, upstream(execErr)
	}
	return encode(result)
}

// callAlert 校验 alert.set 入参并返回告警配置结果。
func callAlert(raw json.RawMessage, exec Executor, ctx Context) (json.RawMessage, *pluginapi.Error) {
	req, err := parseAlertParams(raw)
	if err != nil {
		return nil, invalid(err.Error())
	}
	result, execErr := exec.SetAlert(ctx, req)
	if execErr != nil {
		return nil, upstream(execErr)
	}
	return encode(result)
}

// encode 把具体结构体编码为 JSON 载荷；失败按内部错误处理。
func encode(v any) (json.RawMessage, *pluginapi.Error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INTERNAL, Message: "结果序列化失败: " + err.Error()}
	}
	return b, nil
}

// invalid / upstream 构造标准协议错误体（§9）。
func invalid(msg string) *pluginapi.Error {
	return &pluginapi.Error{Code: pluginapi.E_INVALID, Message: msg}
}

func upstream(err error) *pluginapi.Error {
	return &pluginapi.Error{Code: pluginapi.E_UPSTREAM, Message: fmt.Sprintf("执行面故障: %v", err)}
}

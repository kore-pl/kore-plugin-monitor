package monitor

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// Call 是方法级统一入口：校验平台与参数 → 调执行面 → 编码结构化结果。
// 平台不支持回 E_UNSUPPORTED；执行面故障统一回 E_UPSTREAM。
func Call(method string, raw json.RawMessage, exec Executor, ctx Context) (json.RawMessage, *pluginapi.Error) {
	if !platformSupported {
		return nil, &pluginapi.Error{Code: pluginapi.E_UNSUPPORTED, Message: errUnsupportedPlatform.Error() + "（可用平台：Linux / macOS / Windows）"}
	}
	if exec == nil {
		exec = NewExecutor()
	}
	switch method {
	case "monitor.system":
		params, err := parseSystemParams(raw)
		if err != nil {
			return nil, invalid(err.Error())
		}
		result, execErr := exec.System(ctx, params)
		if execErr != nil {
			return nil, classify(execErr)
		}
		return encode(result)
	case "monitor.sample":
		// 薄包装：复用 System 的真实采集（其中已调 recordHistory 追加历史点），
		// 不新增 Executor 方法、不起任何定时器（G-19）。周期由调用方（scheduler 任务）提供。
		// 入参 history_points / history_interval_s 可空，语义与 monitor.system 完全一致，
		// 由 configDefaultsFor("monitor.sample") 注入配置缺省值后合并。
		params, err := parseSystemParams(raw)
		if err != nil {
			return nil, invalid(err.Error())
		}
		result, execErr := exec.System(ctx, params)
		if execErr != nil {
			return nil, classify(execErr)
		}
		return encode(SampleResult{
			Schema: "monitor.sample.v1", SampledAt: result.SampledAt, Source: result.Source,
			Appended: result.History.Appended, History: result.History, Warnings: result.Warnings,
		})
	case "monitor.processes":
		params, err := parseProcessParams(raw)
		if err != nil {
			return nil, invalid(err.Error())
		}
		result, execErr := exec.Processes(ctx, params)
		if execErr != nil {
			return nil, classify(execErr)
		}
		return encode(result)
	case "monitor.disks":
		params, err := parseDisksParams(raw)
		if err != nil {
			return nil, invalid(err.Error())
		}
		result, execErr := exec.Disks(ctx, params)
		if execErr != nil {
			return nil, classify(execErr)
		}
		return encode(result)
	case "monitor.temperature":
		if err := requireObject(raw); err != nil {
			return nil, invalid(err.Error())
		}
		result, execErr := exec.Temperature(ctx)
		if execErr != nil {
			return nil, classify(execErr)
		}
		return encode(result)
	default:
		return nil, &pluginapi.Error{Code: pluginapi.E_NOTFOUND, Message: "未知方法: " + method}
	}
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

// classify 把执行面错误映射为协议错误码：平台不支持→E_UNSUPPORTED，
// path 未命中挂载点→E_INVALID，其余（含四类故障注入）→E_UPSTREAM。
func classify(err error) *pluginapi.Error {
	switch {
	case errors.Is(err, errUnsupportedPlatform):
		return &pluginapi.Error{Code: pluginapi.E_UNSUPPORTED, Message: err.Error()}
	case errors.Is(err, errPathNotMounted):
		return invalid(err.Error())
	default:
		return upstream(err)
	}
}

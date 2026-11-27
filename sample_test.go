package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-monitor/internal/monitor"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestSampleConfigDefaultsInjection 证明 monitor.sample 与 monitor.system 共用同一份历史配置：
// 未显式给参数时，config 里的 history_points / history_interval_s 被注入，Parse 通过；
// 显式给的参数不被覆盖（请求优先，与 config 契约 §5 一致）。
func TestSampleConfigDefaultsInjection(t *testing.T) {
	cfg := testConfig(t)
	cfg.values.HistoryPoints = 120
	cfg.values.HistoryIntervalS = 300

	for _, method := range []string{"monitor.sample", "sample"} {
		merged := applyConfigDefaults(method, json.RawMessage(`{}`), cfg)
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(merged, &obj); err != nil {
			t.Fatalf("%s 注入结果非法: %v (%s)", method, err, merged)
		}
		var hp, iv int
		_ = json.Unmarshal(obj["history_points"], &hp)
		_ = json.Unmarshal(obj["history_interval_s"], &iv)
		if hp != 120 || iv != 300 {
			t.Fatalf("%s 未注入配置默认值: hp=%d iv=%d", method, hp, iv)
		}
		// 经 Call 用替身执行面验证注入值真被解析（替身只记录入参，不碰 /proc）。
		// Call 只认全名（裸名由 dispatch 的 normalizeMethod 归一），故这里固定全名。
		var exec paramCaptureExecutor
		if _, perr := monitor.Call("monitor.sample", merged, &exec, monitor.Context{}); perr != nil {
			t.Fatalf("%s 注入后 Call 失败: %+v", method, perr)
		}
		if exec.params.HistoryPoints != 120 || exec.params.HistoryIntervalS != 300 {
			t.Fatalf("%s 注入未到达执行面: %+v", method, exec.params)
		}
	}

	// 显式参数优先：请求给的 30s 不被配置 300s 覆盖。
	merged := applyConfigDefaults("monitor.sample", json.RawMessage(`{"history_interval_s":30}`), cfg)
	var exec paramCaptureExecutor
	if _, perr := monitor.Call("monitor.sample", merged, &exec, monitor.Context{}); perr != nil {
		t.Fatalf("显式参数 Call 失败: %+v", perr)
	}
	if exec.params.HistoryIntervalS != 30 {
		t.Fatalf("显式参数未优先: iv=%d", exec.params.HistoryIntervalS)
	}
}

// paramCaptureExecutor 是只记录 System 入参的最小替身，其余方法不会被 sample 触达。
type paramCaptureExecutor struct {
	recordingExecutor
	params monitor.SystemParams
}

func (p *paramCaptureExecutor) System(_ monitor.Context, params monitor.SystemParams) (monitor.SystemResult, error) {
	p.params = params
	return monitor.SystemResult{}, nil
}

// TestSampleDispatchInvalidParams 证明 sample 走与 system 相同的参数校验：
// history_interval_s 越界回 E_INVALID，而非静默落到默认值。
func TestSampleDispatchInvalidParams(t *testing.T) {
	resp := invoke(t, "monitor.sample", "sample-invalid", json.RawMessage(`{"history_interval_s":99999}`))
	if resp.Error == nil || resp.Error.Code != pluginapi.E_INVALID {
		t.Fatalf("越界参数应回 E_INVALID: %+v", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "history_interval_s") {
		t.Fatalf("错误信息应定位到字段: %s", resp.Error.Message)
	}
}

// TestSampleMethodNormalization 证明全名与裸名都能归一为 monitor.sample，
// 而未知方法仍返回 E_NOTFOUND（方法分发点同步，避免 E_NOTFOUND 回归）。
func TestSampleMethodNormalization(t *testing.T) {
	for _, method := range []string{"monitor.sample", "sample"} {
		name, ok := normalizeMethod(method, "monitor")
		if !ok || name != "monitor.sample" {
			t.Fatalf("%s 归一失败: name=%q ok=%v", method, name, ok)
		}
	}
	if _, ok := normalizeMethod("monitor.nope", "monitor"); ok {
		t.Fatal("未知方法不应归一成功")
	}
}

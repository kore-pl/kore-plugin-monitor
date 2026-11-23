//go:build !linux

package monitor

import (
	"encoding/json"
	"testing"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestCallUnsupportedPlatform 在非 Linux 上断言四个方法都显式返回 E_UNSUPPORTED，
// 不静默降级为空结果（G-16：能力缺失必须走降级矩阵）。
func TestCallUnsupportedPlatform(t *testing.T) {
	methods := []struct{ method, params string }{
		{"monitor.system", `{}`},
		{"monitor.processes", `{"limit":1}`},
		{"monitor.disks", `{}`},
		{"monitor.temperature", `{}`},
	}
	for _, item := range methods {
		result, perr := Call(item.method, json.RawMessage(item.params), nil, Context{})
		if result != nil {
			t.Fatalf("%s 在非 Linux 上不应返回结果: %s", item.method, result)
		}
		if perr == nil || perr.Code != pluginapi.E_UNSUPPORTED {
			t.Fatalf("%s 错误=%v，期望 E_UNSUPPORTED", item.method, perr)
		}
	}
}

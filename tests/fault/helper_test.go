// Package fault 对 monitor 插件做四类故障注入，验证方法级错误返回（G-20 / proposal §4.8 S-04）。
// 每个用例向执行面注入一种故障，断言两方法返回 nil 结果 + E_UPSTREAM + 类别关键词；
// 同时用无故障 sanity 反证断言不是"永远失败"的假通过。
package fault

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-monitor/internal/monitor"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// methods 是四类故障共同覆盖的两个业务方法及合法参数。
var methods = []struct {
	name   string
	method string
	params string
}{
	{"sample", "monitor.sample", `{"metrics":["cpu"]}`},
	{"alert_set", "monitor.alert.set", `{"rule_id":"r1","metric":"cpu","comparator":"gt","threshold":85,"enabled":true}`},
}

// faultExecutor 实现 monitor.Executor：任意方法都返回携类别与故障说明的 FaultError。
// detail 由各 fault 文件传入（如"信号 9"/"ENOSPC"），作为错误信息关键词断言点。
type faultExecutor struct {
	kind   monitor.FaultKind
	detail string
}

func (f faultExecutor) Sample(_ monitor.Context, _ []string) (monitor.SampleResult, error) {
	return monitor.SampleResult{}, monitor.NewFaultError(f.kind, "sample 故障: "+f.detail)
}

func (f faultExecutor) SetAlert(_ monitor.Context, _ monitor.AlertRequest) (monitor.AlertResult, error) {
	return monitor.AlertResult{}, monitor.NewFaultError(f.kind, "alert.set 故障: "+f.detail)
}

// assertFault 对每个方法注入类别 kind：断言无结果、E_UPSTREAM、错误体完整且含类别关键词。
func assertFault(t *testing.T, kind monitor.FaultKind, detail, wantHint string) {
	t.Helper()
	for _, m := range methods {
		t.Run(m.name, func(t *testing.T) {
			result, perr := monitor.Call(m.method, json.RawMessage(m.params), faultExecutor{kind: kind, detail: detail}, monitor.Context{})
			if perr == nil {
				t.Fatalf("%s 注入 %s 后未返回错误（结果=%s）", m.method, kind, result)
			}
			if result != nil {
				t.Fatalf("%s 出错时不应带结果: %s", m.method, result)
			}
			if perr.Code != pluginapi.E_UPSTREAM {
				t.Fatalf("%s 错误码=%q，期望 E_UPSTREAM（message=%q）", m.method, perr.Code, perr.Message)
			}
			if perr.Message == "" || !strings.Contains(perr.Message, string(kind)) || !strings.Contains(perr.Message, wantHint) {
				t.Fatalf("%s 错误信息 %q 未含类别 %q / 关键词 %q", m.method, perr.Message, kind, wantHint)
			}
		})
	}
}

// sanityNoFault 用生产执行面（无注入）验证两方法正常返回，防止故障测试假通过。
func sanityNoFault(t *testing.T) {
	t.Helper()
	for _, m := range methods {
		result, perr := monitor.Call(m.method, json.RawMessage(m.params), monitor.NewExecutor(), monitor.Context{})
		if perr != nil {
			t.Fatalf("无故障时 %s 意外返回错误: %v", m.method, perr)
		}
		if len(result) == 0 || string(result) == "null" {
			t.Fatalf("无故障时 %s 返回空结果", m.method)
		}
	}
}

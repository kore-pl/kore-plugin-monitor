package main

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-monitor/internal/monitor"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// infoManifest 是 info.json 中本测试关心的字段（方法级能力声明）。
type infoManifest struct {
	ID                 string            `json:"id"`
	Version            string            `json:"version"`
	Capabilities       []string          `json:"capabilities"`
	MethodCapabilities map[string]string `json:"method_capabilities"`
}

// implementedMethod 是采集器已实现的方法及其最小合法参数。
type implementedMethod struct {
	method string
	params string
}

// implementedMethods 是采集器（monitor.Call）分发分支的方法清单；新增采集方法必须同步 info.json 与本表。
// config.get / config.set 由 dispatch 层实现，不属于采集器分支，单独在 config 契约测试里断言（PLUGIN-CONFIG §1）。
var implementedMethods = []implementedMethod{
	{"monitor.system", `{}`},
	{"monitor.processes", `{"limit":1}`},
	{"monitor.disks", `{}`},
	{"monitor.temperature", `{}`},
}

// configMethods 是 dispatch 层配置契约方法的期望能力（PLUGIN-CONFIG §1 + PROTOCOL §3.1.2.1）。
var configMethods = map[string]string{
	"monitor.config.get": "system:read",
	"monitor.config.set": "system:admin",
}

// recordingExecutor 记录被分发到的方法，用于证明 info.json 声明的方法在采集器里有对应分支。
type recordingExecutor struct{ called string }

func (r *recordingExecutor) System(monitor.Context, monitor.SystemParams) (monitor.SystemResult, error) {
	r.called = "monitor.system"
	return monitor.SystemResult{}, nil
}

func (r *recordingExecutor) Processes(monitor.Context, monitor.ProcessParams) (monitor.ProcessesResult, error) {
	r.called = "monitor.processes"
	return monitor.ProcessesResult{}, nil
}

func (r *recordingExecutor) Disks(monitor.Context, monitor.DisksParams) (monitor.DisksResult, error) {
	r.called = "monitor.disks"
	return monitor.DisksResult{}, nil
}

func (r *recordingExecutor) Temperature(monitor.Context) (monitor.TemperatureResult, error) {
	r.called = "monitor.temperature"
	return monitor.TemperatureResult{}, nil
}

// loadInfo 读取并解析仓根 info.json。
func loadInfo(t *testing.T) infoManifest {
	t.Helper()
	data, err := os.ReadFile("info.json")
	if err != nil {
		t.Fatalf("读取 info.json: %v", err)
	}
	var info infoManifest
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("解析 info.json: %v", err)
	}
	return info
}

// TestMethodCapabilitiesMatchCollector 是「声明 vs 实现」一致性门禁：
// 读 info.json 的采集器方法清单，逐个断言 Call 有对应分支，且反向无遗漏。
// 配置契约方法不在采集器表里，由 TestConfigMethodsDeclared 单独断言。
func TestMethodCapabilitiesMatchCollector(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("monitor 只在 Linux 提供真实采集；非 Linux 由 call_unsupported_test.go 断言 E_UNSUPPORTED")
	}
	info := loadInfo(t)
	if len(info.MethodCapabilities) == 0 {
		t.Fatal("info.json 缺少 method_capabilities 声明")
	}
	params := make(map[string]string, len(implementedMethods))
	for _, item := range implementedMethods {
		params[item.method] = item.params
	}
	for _, item := range implementedMethods {
		method := item.method
		capability, ok := info.MethodCapabilities[method]
		if !ok {
			t.Fatalf("采集器实现了 %q，但 info.json 未声明方法级 capability", method)
		}
		if capability != "system:read" {
			t.Fatalf("%s 的 capability=%q，只读方法必须声明 system:read", method, capability)
		}
		if !strings.HasPrefix(method, info.ID+".") {
			t.Fatalf("方法键 %q 必须以插件 id %q 为域", method, info.ID)
		}
		exec := &recordingExecutor{}
		result, perr := monitor.Call(method, json.RawMessage(item.params), exec, monitor.Context{})
		if perr != nil {
			t.Fatalf("%s 分发失败: code=%s message=%s", method, perr.Code, perr.Message)
		}
		if result == nil || exec.called != method {
			t.Fatalf("%s 未命中采集器分支（实际分发到 %q）", method, exec.called)
		}
		if !contains(info.Capabilities, capability) {
			t.Fatalf("插件级 capabilities 未包含 %q（方法 %s 声明了它）", capability, method)
		}
	}
	manifest := loadManifest(t)
	if manifest.Version != info.Version {
		t.Fatalf("manifest.json 版本 %q 与 info.json %q 不一致", manifest.Version, info.Version)
	}
	if len(info.Capabilities) != 2 || !contains(info.Capabilities, "system:read") || !contains(info.Capabilities, "system:admin") {
		t.Fatalf("capabilities 需同时声明 system:read 与 system:admin，实际 %v", info.Capabilities)
	}
}

// TestConfigMethodsDeclared 断言 info.json 声明了 config.get=system:read / config.set=system:admin，
// 且插件级 capabilities 覆盖这两个值；dispatch 命中由 config_contract_test.go 验证。
func TestConfigMethodsDeclared(t *testing.T) {
	info := loadInfo(t)
	for method, want := range configMethods {
		capability, ok := info.MethodCapabilities[method]
		if !ok {
			t.Fatalf("info.json 缺少配置契约方法声明 %s", method)
		}
		if capability != want {
			t.Fatalf("%s 的 capability=%q，期望 %q", method, capability, want)
		}
		if !strings.HasPrefix(method, info.ID+".") {
			t.Fatalf("方法键 %q 必须以插件 id %q 为域", method, info.ID)
		}
		if !contains(info.Capabilities, capability) {
			t.Fatalf("插件级 capabilities 未包含 %q（方法 %s 声明了它）", capability, method)
		}
	}
}

// TestUnknownMethodNotImplemented 确保测试表之外的方法不会被误判为已实现。
func TestUnknownMethodNotImplemented(t *testing.T) {
	_, perr := monitor.Call("monitor.nope", json.RawMessage(`{}`), &recordingExecutor{}, monitor.Context{})
	if perr == nil || perr.Code != pluginapi.E_NOTFOUND {
		t.Fatalf("未知方法错误码=%v，期望 E_NOTFOUND", perr)
	}
}

// manifestInfo 是 manifest.json 的握手字段子集。
type manifestInfo struct {
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

// loadManifest 读取随二进制内嵌的 manifest.json。
func loadManifest(t *testing.T) manifestInfo {
	t.Helper()
	data, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatalf("读取 manifest.json: %v", err)
	}
	var manifest manifestInfo
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("解析 manifest.json: %v", err)
	}
	return manifest
}

// contains 判断字符串切片是否包含目标值。
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

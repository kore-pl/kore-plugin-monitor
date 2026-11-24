package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-monitor/internal/plugincfg"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// decodeGet 解析 config.get 结果为通用对象，便于逐条核对契约形状。
func decodeGet(t *testing.T, raw json.RawMessage) struct {
	Schema struct {
		Title  string `json:"title"`
		Fields []struct {
			Key       string          `json:"key"`
			Label     string          `json:"label"`
			Type      string          `json:"type"`
			Item      string          `json:"item"`
			Default   json.RawMessage `json:"default"`
			Required  bool            `json:"required"`
			Help      string          `json:"help"`
			Options   []string        `json:"options"`
			Min       *int            `json:"min"`
			Max       *int            `json:"max"`
			Sensitive bool            `json:"sensitive"`
		} `json:"fields"`
	} `json:"schema"`
	Values json.RawMessage `json:"values"`
	Source string          `json:"source"`
} {
	t.Helper()
	var result struct {
		Schema struct {
			Title  string `json:"title"`
			Fields []struct {
				Key       string          `json:"key"`
				Label     string          `json:"label"`
				Type      string          `json:"type"`
				Item      string          `json:"item"`
				Default   json.RawMessage `json:"default"`
				Required  bool            `json:"required"`
				Help      string          `json:"help"`
				Options   []string        `json:"options"`
				Min       *int            `json:"min"`
				Max       *int            `json:"max"`
				Sensitive bool            `json:"sensitive"`
			} `json:"fields"`
		} `json:"schema"`
		Values json.RawMessage `json:"values"`
		Source string          `json:"source"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("解析 config.get 结果: %v", err)
	}
	return result
}

// TestConfigGetSchemaComplete 逐条核对 PLUGIN-CONFIG §2：schema 两字段、类型枚举、
// 每个字段必填项、中文 label/help、int 的 min/max、select 的 options、禁 object/any。
func TestConfigGetSchemaComplete(t *testing.T) {
	for _, method := range []string{"config.get", "monitor.config.get"} {
		r := invoke(t, method, "r-schema", nil)
		if r.Error != nil {
			t.Fatalf("%s 失败: %v", method, r.Error)
		}
		get := decodeGet(t, r.Result)
		if strings.TrimSpace(get.Schema.Title) == "" {
			t.Fatalf("%s schema.title 为空", method)
		}
		if len(get.Schema.Fields) == 0 {
			t.Fatalf("%s schema.fields 为空", method)
		}
		seen := map[string]bool{}
		for _, f := range get.Schema.Fields {
			if f.Key == "" || f.Label == "" || f.Help == "" {
				t.Fatalf("%s 字段缺 key/label/help: %+v", method, f)
			}
			if seen[f.Key] {
				t.Fatalf("%s 字段 key 重复: %s", method, f.Key)
			}
			seen[f.Key] = true
			// label 用用户语言：至少含一个汉字（允许 CPU/括号/单位等辅助字符）。
			hasHan := false
			for _, ch := range f.Label {
				if ch >= 0x4e00 && ch <= 0x9fff {
					hasHan = true
					break
				}
			}
			if !hasHan {
				t.Errorf("%s 字段 %s 的 label 不含中文: %q", method, f.Key, f.Label)
			}
			if !json.Valid(f.Default) && string(f.Default) != "null" {
				t.Fatalf("%s 字段 %s 的 default 非法: %s", method, f.Key, f.Default)
			}
			switch f.Type {
			case plugincfg.TypeInt:
				if f.Min == nil || f.Max == nil || *f.Min > *f.Max {
					t.Fatalf("%s int 字段 %s 缺合法 min/max", method, f.Key)
				}
			case plugincfg.TypeSelect:
				if len(f.Options) == 0 {
					t.Fatalf("%s select 字段 %s 缺 options", method, f.Key)
				}
			case plugincfg.TypeString, plugincfg.TypeBool, plugincfg.TypeList:
				// 合法显式类型
			default:
				t.Fatalf("%s 字段 %s 的 type=%q 不在显式枚举（禁 object/any）", method, f.Key, f.Type)
			}
		}
		for _, key := range []string{"history_points", "history_interval_s", "cpu_threshold", "memory_threshold", "disk_threshold", "process_limit", "process_sort", "sample_interval_ms"} {
			if !seen[key] {
				t.Fatalf("%s schema 缺可配置字段 %s", method, key)
			}
		}
		if get.Source != plugincfg.SourceDefault {
			t.Fatalf("%s 初始 source=%q，期望 default", method, get.Source)
		}
	}
}

// TestConfigSetValidation 校验非法值返回 E_INVALID 且内存/文件不变。
func TestConfigSetValidation(t *testing.T) {
	cfg := testConfig(t)
	before, _ := json.Marshal(cfg.values)
	bad := []struct {
		name   string
		values string
		want   string
	}{
		{"缺 values", `{}`, "values"},
		{"history_points 越界", `{"values":{"history_points":9999}}`, "history_points"},
		{"阈值越界", `{"values":{"cpu_threshold":101}}`, "cpu_threshold"},
		{"process_sort 非法", `{"values":{"process_sort":"pid"}}`, "process_sort"},
		{"sample_interval 越界", `{"values":{"sample_interval_ms":0}}`, "sample_interval_ms"},
		{"类型错误", `{"values":{"history_points":"x"}}`, "history_points"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			result, perr := configSet(json.RawMessage(tc.values), cfg)
			if perr == nil || perr.Code != pluginapi.E_INVALID {
				t.Fatalf("configSet(%s) result=%s perr=%+v，期望 E_INVALID", tc.values, result, perr)
			}
			if !strings.Contains(perr.Message, tc.want) {
				t.Fatalf("错误消息 %q 不含定位信息 %q", perr.Message, tc.want)
			}
			after, _ := json.Marshal(cfg.values)
			if string(after) != string(before) {
				t.Fatalf("非法 set 改了内存：before=%s after=%s", before, after)
			}
		})
	}
}

// TestConfigSetPersistAndHotApply 校验 set → 原子持久化 → source=file → 下一次调用热生效（PID 不变）。
func TestConfigSetPersistAndHotApply(t *testing.T) {
	cfg := testConfig(t)
	params := json.RawMessage(`{"values":{"history_points":123,"history_interval_s":30,"cpu_threshold":75,"process_sort":"mem","sample_interval_ms":50}}`)
	result, perr := configSet(params, cfg)
	if perr != nil {
		t.Fatalf("configSet: %+v", perr)
	}
	var setResult struct {
		Saved           bool     `json:"saved"`
		Applied         bool     `json:"applied"`
		RestartRequired bool     `json:"restart_required"`
		Warnings        []string `json:"warnings"`
	}
	if err := json.Unmarshal(result, &setResult); err != nil {
		t.Fatalf("解析 set 结果: %v", err)
	}
	if !setResult.Saved || !setResult.Applied || setResult.RestartRequired {
		t.Fatalf("set 结果不符: %+v", setResult)
	}
	if len(setResult.Warnings) == 0 || !strings.Contains(setResult.Warnings[0], "立即生效") {
		t.Fatalf("set 结果缺热生效提示: %+v", setResult.Warnings)
	}
	if cfg.values.HistoryPoints != 123 || cfg.values.ProcessSort != "mem" || cfg.values.SampleIntervalMS != 50 {
		t.Fatalf("内存未更新: %+v", cfg.values)
	}
	if cfg.source != plugincfg.SourceFile {
		t.Fatalf("set 后 source=%q，期望 file", cfg.source)
	}

	// 持久化：文件存在、0600、内容等于内存值。
	path := filepath.Join(cfg.store.Path())
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("配置文件未落盘: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("配置文件权限=%o，期望 600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读配置文件: %v", err)
	}
	var disk fileValues
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("解析配置文件: %v", err)
	}
	if disk.HistoryPoints != 123 || disk.ProcessSort != "mem" || disk.SampleIntervalMS != 50 {
		t.Fatalf("落盘值与内存不一致: disk=%+v mem=%+v", disk, cfg.values)
	}

	// 同一进程内 config.get 立即反映新值（PID 不变，未重启）。
	getResult, perr := configGet(cfg)
	if perr != nil {
		t.Fatalf("configGet: %+v", perr)
	}
	get := decodeGet(t, getResult)
	if get.Source != plugincfg.SourceFile {
		t.Fatalf("config.get source=%q，期望 file", get.Source)
	}
	var values map[string]json.RawMessage
	_ = json.Unmarshal(get.Values, &values)
	var hp int
	_ = json.Unmarshal(values["history_points"], &hp)
	if hp != 123 {
		t.Fatalf("config.get 未反映新值 history_points=%d", hp)
	}

	// 业务调用热生效：请求未显式给 process_sort/limit 时注入配置值。
	merged := applyConfigDefaults("monitor.processes", json.RawMessage(`{}`), cfg)
	var injected map[string]json.RawMessage
	_ = json.Unmarshal(merged, &injected)
	var procSort string
	_ = json.Unmarshal(injected["sort"], &procSort)
	if procSort != "mem" {
		t.Fatalf("processes 未注入配置排序 sort=%q", procSort)
	}
}

// TestConfigPrecedenceFileOverEnv 校验 config.json 优先于环境变量（PLUGIN-CONFIG §4）。
func TestConfigPrecedenceFileOverEnv(t *testing.T) {
	dir := t.TempDir()
	store := plugincfg.NewStore(dir)
	if err := store.Write(json.RawMessage(`{"history_points":42,"process_sort":"mem"}`)); err != nil {
		t.Fatalf("写配置: %v", err)
	}
	t.Setenv(EnvHistoryPoints, "999")
	t.Setenv(EnvProcessSort, "cpu")
	values, source, err := resolveValues(store)
	if err != nil {
		t.Fatalf("resolveValues: %v", err)
	}
	if source != plugincfg.SourceFile {
		t.Fatalf("source=%q，期望 file", source)
	}
	if values.HistoryPoints != 42 || values.ProcessSort != "mem" {
		t.Fatalf("文件值未覆盖环境变量: %+v", values)
	}
}

// TestConfigEnvFallback 校验无文件时环境变量生效（老用户兼容，PLUGIN-CONFIG §4）。
func TestConfigEnvFallback(t *testing.T) {
	store := plugincfg.NewStore(t.TempDir())
	t.Setenv(EnvCPUThreshold, "77")
	values, source, err := resolveValues(store)
	if err != nil {
		t.Fatalf("resolveValues: %v", err)
	}
	if source != plugincfg.SourceEnv {
		t.Fatalf("source=%q，期望 env", source)
	}
	if values.CPUThreshold != 77 {
		t.Fatalf("env 未生效 cpu_threshold=%d", values.CPUThreshold)
	}
}

// TestPlugincfgSensitiveMechanism 覆盖 G-30：sensitive 字段 get 回 ***、set 收 *** 不改。
// 本仓业务 schema 无凭据字段，但通用机制必须随 schema 驱动生效，防止将来加字段时漏掩码。
func TestPlugincfgSensitiveMechanism(t *testing.T) {
	schema := plugincfg.Schema{
		Title: "测试",
		Fields: []plugincfg.Field{{
			Key: "password", Label: "口令", Type: plugincfg.TypeString, Required: true,
			Help: "测试用敏感字段", Sensitive: true,
		}},
	}
	masked, err := plugincfg.MaskValues(schema, json.RawMessage(`{"password":"secret-value"}`))
	if err != nil {
		t.Fatalf("MaskValues: %v", err)
	}
	if !strings.Contains(string(masked), plugincfg.SensitivePlaceholder) || strings.Contains(string(masked), "secret-value") {
		t.Fatalf("敏感字段未掩码: %s", masked)
	}
	if !plugincfg.IsSensitivePlaceholder(json.RawMessage(`"***"`)) {
		t.Fatal("*** 应判为不修改占位")
	}
	if plugincfg.IsSensitivePlaceholder(json.RawMessage(`"new-secret"`)) {
		t.Fatal("新明文不应判为占位")
	}
}

// TestSchemaValidateRejectsBadTypes 校验 ValidateSchema 拒绝 object/any 与非法 list.item（G-24）。
func TestSchemaValidateRejectsBadTypes(t *testing.T) {
	bad := []plugincfg.Schema{
		{Title: "t", Fields: []plugincfg.Field{{Key: "k", Label: "键", Help: "帮助", Type: "object", Default: json.RawMessage(`{}`)}}},
		{Title: "t", Fields: []plugincfg.Field{{Key: "k", Label: "键", Help: "帮助", Type: "any", Default: json.RawMessage(`null`)}}},
		{Title: "t", Fields: []plugincfg.Field{{Key: "k", Label: "键", Help: "帮助", Type: plugincfg.TypeList, Item: "object", Default: json.RawMessage(`[]`)}}},
	}
	for i, schema := range bad {
		if err := plugincfg.ValidateSchema(schema); err == nil {
			t.Fatalf("第 %d 个非法 schema 未被拒绝", i)
		}
	}
	if err := plugincfg.ValidateSchema(configSchema()); err != nil {
		t.Fatalf("本仓 configSchema 自检失败: %v", err)
	}
}

package monitor

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Temperature 读取 /sys/class/thermal/thermal_zone*/temp；无 zone 时 available=false，不报错。
func (c *Collector) Temperature(ctx Context) (TemperatureResult, error) {
	result := TemperatureResult{Schema: "monitor.temperature.v1", Zones: []ThermalZone{}, Warnings: []string{}}
	result.SampledAt = c.now().UTC().Format(time.RFC3339Nano)
	matches, err := filepath.Glob(filepath.Join(c.sysRoot, "class", "thermal", "thermal_zone*"))
	if err != nil {
		return result, err
	}
	sort.Strings(matches)
	for _, dir := range matches {
		tempData, readErr := os.ReadFile(filepath.Join(dir, "temp"))
		if readErr != nil {
			addWarning(&result.Warnings, "%s 读取失败: %v", filepath.Base(dir), readErr)
			continue
		}
		tempC, parseErr := parseMilliCelsius(string(tempData))
		if parseErr != nil {
			addWarning(&result.Warnings, "%s 解析失败: %v", filepath.Base(dir), parseErr)
			continue
		}
		zone := ThermalZone{Zone: filepath.Base(dir), TempC: tempC}
		if typeData, typeErr := os.ReadFile(filepath.Join(dir, "type")); typeErr == nil {
			zone.Type = strings.TrimSpace(string(typeData))
		}
		result.Zones = append(result.Zones, zone)
	}
	result.Count = len(result.Zones)
	result.Available = result.Count > 0
	return result, nil
}

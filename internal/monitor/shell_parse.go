package monitor

import (
	"fmt"
	"strconv"
	"strings"
)

// 跨平台解析器共用的显式类型转换：解析失败返回错误，绝不静默当作 0 冒充实测。

func parseFloatField(raw, label string) (float64, error) {
	s := strings.TrimSpace(raw)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("解析 %s 失败：%q", label, s)
	}
	return v, nil
}

func parseIntField(raw, label string) (int64, error) {
	s := strings.TrimSpace(raw)
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("解析 %s 失败：%q", label, s)
	}
	return v, nil
}

// clampRate 把两次采样的比率夹到 [0,100] 并保留两位小数。
func clampRate(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}
	return clampPercent(part / whole * 100)
}

// parseTimeTicks 解析 ps TIME（[[dd-]hh:]mm:ss 或 mm:ss）为秒。
func parseTimeTicks(raw string) (float64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("TIME 为空")
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		days, err := parseFloatField(s[:i], "TIME 天")
		if err != nil {
			return 0, err
		}
		s = s[i+1:]
		parts := strings.Split(s, ":")
		hours, err := parseFloatField(parts[0], "TIME 时")
		if err != nil {
			return 0, err
		}
		minutes, err := parseFloatField(parts[1], "TIME 分")
		if err != nil {
			return 0, err
		}
		seconds, err := parseFloatField(parts[2], "TIME 秒")
		if err != nil {
			return 0, err
		}
		return days*86400 + hours*3600 + minutes*60 + seconds, nil
	}
	parts := strings.Split(s, ":")
	var total float64
	for _, part := range parts {
		v, err := parseFloatField(part, "TIME")
		if err != nil {
			return 0, err
		}
		total = total*60 + v
	}
	return total, nil
}

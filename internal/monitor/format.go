package monitor

import (
	"fmt"
	"math"
)

// humanBytes 把字节数格式化为二进制单位（B / KiB / MiB / GiB / TiB），保留一位小数。
// 数值本身仍以 int64 字节字段返回，人类可读串只用于展示。
func humanBytes(n int64) string {
	if n == 0 {
		return "0 B"
	}
	units := [...]string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	value := float64(n)
	idx := 0
	for value >= 1024 && idx < len(units)-1 {
		value /= 1024
		idx++
	}
	if idx == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", value, units[idx])
}

// round2 把百分比保留两位小数，避免 JSON 出现长浮点尾巴。
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// clampPercent 把百分比夹到 [0,100]，防止计数器回绕或采样抖动越界。
func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return round2(v)
}

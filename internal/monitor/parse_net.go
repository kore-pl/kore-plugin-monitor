package monitor

import (
	"fmt"
	"strconv"
	"strings"
)

// netStat 是 /proc/net/dev 中单个网卡的累计计数。
type netStat struct {
	Name                 string
	RXBytes, TXBytes     int64
	RXPackets, TXPackets int64
	RXErrors, TXErrors   int64
}

// parseNetDev 解析 /proc/net/dev；includeLoopback=false 时排除 lo。
// 字段顺序（数据行冒号后）：rx_bytes rx_packets rx_errs rx_drop fifo frame compressed
// multicast tx_bytes tx_packets tx_errs tx_drop ...
func parseNetDev(data string, includeLoopback bool) ([]netStat, error) {
	stats := make([]netStat, 0, 4)
	for _, line := range strings.Split(data, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" || name == "Inter-|" || name == "face" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 11 {
			return nil, fmt.Errorf("网卡 %s 字段数 %d 少于 11", name, len(fields))
		}
		if !includeLoopback && name == "lo" {
			continue
		}
		values := make([]int64, 11)
		for i := 0; i < 11; i++ {
			value, err := strconv.ParseInt(fields[i], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("网卡 %s 第 %d 字段 %q 非法", name, i+1, fields[i])
			}
			values[i] = value
		}
		stats = append(stats, netStat{
			Name: name, RXBytes: values[0], RXPackets: values[1], RXErrors: values[2],
			TXBytes: values[8], TXPackets: values[9], TXErrors: values[10],
		})
	}
	return stats, nil
}

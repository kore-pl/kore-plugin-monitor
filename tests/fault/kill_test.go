package fault

import "testing"

// TestKillFault 注入 kill -9（进程被信号 9 终止），期望两方法返回 E_UPSTREAM，
// 错误信息含 kill 类别与信号说明（G-20 四类故障之一）。
func TestKillFault(t *testing.T) {
	assertFault(t, "kill", "进程被信号 9 终止", "信号 9")
}

// TestKillFaultSanity 无故障时方法正常返回，反证 kill 断言不是假通过。
func TestKillFaultSanity(t *testing.T) {
	sanityNoFault(t)
}

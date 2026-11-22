package fault

import "testing"

// TestCgroupFault 注入 cgroup memory.max 超限（OOM kill），期望两方法返回
// E_UPSTREAM，错误信息含 cgroup 类别与 memory.max 说明（G-20 四类故障之一）。
func TestCgroupFault(t *testing.T) {
	assertFault(t, "cgroup", "cgroup memory.max 超限", "memory.max")
}

// TestCgroupFaultSanity 无故障时方法正常返回，反证 cgroup 断言不是假通过。
func TestCgroupFaultSanity(t *testing.T) {
	sanityNoFault(t)
}

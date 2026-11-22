package fault

import "testing"

// TestDiskFault 注入磁盘满（ENOSPC），期望两方法返回 E_UPSTREAM，错误信息含
// disk 类别与 ENOSPC 说明（G-20 四类故障之一）。
func TestDiskFault(t *testing.T) {
	assertFault(t, "disk", "写入磁盘 ENOSPC", "ENOSPC")
}

// TestDiskFaultSanity 无故障时方法正常返回，反证 disk 断言不是假通过。
func TestDiskFaultSanity(t *testing.T) {
	sanityNoFault(t)
}

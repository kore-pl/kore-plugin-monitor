package fault

import "testing"

// TestTimeoutFault 注入请求级超时（调用在 deadline 内未完成），期望两方法返回
// E_UPSTREAM，错误信息含 timeout 类别与"超时"说明（G-20 四类故障之一）。
// 注：超时是请求级 context，不是插件内周期定时器（G-19 允许请求级超时）。
func TestTimeoutFault(t *testing.T) {
	assertFault(t, "timeout", "调用超时 15000ms", "超时")
}

// TestTimeoutFaultSanity 无故障时方法正常返回，反证 timeout 断言不是假通过。
func TestTimeoutFaultSanity(t *testing.T) {
	sanityNoFault(t)
}

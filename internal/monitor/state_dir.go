package monitor

import (
	"os"
	"strings"
)

// stateDirFromEnv 返回插件状态目录（KORE_STATE_DIR），跨执行面共用，
// 与 Linux Collector 的 stateDir 语义一致：未设置时历史只留进程内返回。
func stateDirFromEnv() string {
	return strings.TrimSpace(os.Getenv("KORE_STATE_DIR"))
}

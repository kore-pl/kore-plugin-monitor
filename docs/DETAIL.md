# kore-plugin-monitor（系统监控插件）

## 它做什么

真实读取 Linux `/proc` 与 `/sys`，按需返回系统概览、进程 TOP、磁盘用量与温度。生产级（depth_level=production），M5 批 1「无依赖基础件」。

## 设计约束

- **零轮询（G-19）**：插件内不起 ticker / AfterFunc / 后台 goroutine。CPU 与进程 CPU% 在单次请求内读取两次采样、`time.Sleep(200ms)` 求 delta，调用结束即返回。历史曲线同样无后台采集器，只在每次 `monitor.system` 调用时按「距上次采样 ≥ 间隔」追加一个点，空载零 CPU。
- **阈值告警只回传、不外发**：超限时在返回体标注 `alerts[]`（指标/当前值/阈值/级别/hint），插件不发通知、邮件、Webhook、不出网（G-10）；通知统一归主壳告警中心。
- **零第三方依赖**：只依赖 `github.com/pengtang1024/kore/pkg/pluginapi`，其余为标准库。

## 方法

| 方法 | 入参 | 行为 |
| --- | --- | --- |
| `monitor.system` | `per_cpu`、`include_loopback`、`history_points`、`history_interval_s`、`thresholds`（均可选） | 返回 `cpu`（使用率、分核可选）、`memory`（total/available/used/free/buffers/cached/swap）、`load`（load1/5/15、running/total）、`uptime`、`disks[]`、`network[]`、`alerts[]`、`history`、`warnings[]`。 |
| `monitor.processes` | `limit`（1–500）、`sort`（cpu/mem） | 进程 TOP：`{pid, comm, name, state, cpu_percent, mem_rss_bytes, mem_rss_human, mem_percent, threads}`。 |
| `monitor.disks` | `path?`（绝对路径） | 挂载点容量、inodes 与 `/proc/diskstats` 读写统计；空路径返回全部真实块设备挂载点。 |
| `monitor.temperature` | 无 | `/sys/class/thermal/thermal_zone*` 的温度区域列表。 |

## 安装与验证

从插件市场安装 `monitor`，或通过 `make dist` 产出 `dist/monitor-0.5.0.tar.gz` 导入。开发验证：`make check`（gofmt → go vet → go test -timeout 200s），含 boundary / fault 测试。

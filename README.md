# kore-plugin-monitor

kore 的系统监控插件：**真实读取 Linux `/proc` 与 `/sys`**，按需返回系统概览、进程 TOP、
磁盘用量与温度。生产级（`depth_level=production`），属 M5 批 1「无依赖基础件」。

## 设计约束

- **零轮询（G-19）**：插件内**不起 ticker / AfterFunc / 后台 goroutine**。CPU 与进程 CPU%
  都在单次请求内读取两次采样、`time.Sleep(200ms)` 求 delta；调用结束即返回，不常驻。
- **零第三方依赖**：`go.mod` 只依赖 `github.com/pengtang1024/kore/pkg/pluginapi`；
  静态单二进制，`CGO_ENABLED=0` + `-trimpath` + `-s -w`。
- **仅 Linux 真实实现**：非 Linux 平台 `monitor.*` 显式返回 `E_UNSUPPORTED`，不静默降级。
- **部分失败不扩大**：容器里缺失的 `/proc` 文件只让对应字段为 0 / 空数组，并写入
  `warnings[]`，不让整次调用失败；`monitor.temperature` 无 thermal zone 时返回
  `available=false` + 空数组，不报错。
- **时间预算**：`monitor.system` / `monitor.processes` 约 200ms 采样 + 少量文件 IO，
  单次调用 ≤1s；`monitor.disks` / `monitor.temperature` 为纯读取，毫秒级。
- **数据形状显式**：对外结构体字段全部显式列出，不使用 `object` / `any` / `map[string]any`；
  大数值用 `int64` 字节，人类可读串另给 `*_human` 字段（如 `used_human: "3.2 GiB"`）。

## 协议方法

| 方法 | 入参 | 结果 |
| --- | --- | --- |
| `monitor.system` | `{"per_cpu":true,"include_loopback":false}`（均可选） | `schema/source/sampled_at` + `cpu`（使用率、分核可选）+ `memory`（total/available/used/free/buffers/cached/swap_total/swap_used）+ `load`（load1/5/15、running/total、last_pid）+ `uptime`（uptime_s/idle_s）+ `disks[]` + `network[]` + `warnings[]` |
| `monitor.processes` | `{"limit":20,"sort":"cpu"}`；`limit` ∈ [1,500]，`sort` ∈ `cpu`/`mem` | `schema/sampled_at/sample_interval_ms/sort/limit/count/total` + `items[]`：`{pid, comm, state, cpu_percent, mem_rss_bytes, mem_rss_human, threads}` + `warnings[]` |
| `monitor.disks` | `{"path":"/var"}`（可选，绝对路径） | `items[]`：挂载点容量 + `inodes_total/used/free/used_percent` + `/proc/diskstats` 的 `read_bytes/write_bytes/reads/writes`；`path` 为空返回全部真实块设备挂载点，否则返回最长前缀匹配的单个挂载点 |
| `monitor.temperature` | 无 | `available` + `zones[]`：`{zone, temp_c, type}`（`/sys/class/thermal/thermal_zone*`） |

- 磁盘只统计 `/proc/mounts` 中设备为 `/dev/*` 且文件系统非虚拟类型（tmpfs / proc / sysfs /
  overlay / squashfs / cgroup 等）的挂载点；`/dev/fuse` 这类字符设备不计入。
- 网络来自 `/proc/net/dev`，默认排除 `lo`；`include_loopback=true` 可保留。
- 进程 `cpu_percent` 按单核归一（多核满载可 >100），两次采样间退出的进程跳过并计入 warning。
- 参数非法回 `E_INVALID`，未知方法回 `E_NOTFOUND`，执行面故障回 `E_UPSTREAM`，非 Linux 回 `E_UNSUPPORTED`。

握手复用 site 骨架：`kore.hello` → 会话循环 → `kore.ping` / `kore.reload` / `kore.shutdown`。

## 构建与验收

```bash
gofmt -l .
go vet ./...
go test -timeout 200s ./...
make build
make dist
```

`make dist` 输出 `dist/monitor-<version>.tar.gz`（条目：`info.json`、`bin/monitor`、`SHA256SUMS`），
tar 首条目为 `info.json`；确定性打包保证同源码两次出包 sha256 一致，`size_bytes` 与实测包体积偏差 ≤2 字节。

## 测试

- `internal/monitor/*_test.go`：`/proc/stat` 两次采样 delta、`meminfo`、`loadavg`、`mounts`
  虚拟文件系统过滤、`net/dev`、`processes` 排序与 delta、thermal 缺失、部分文件缺失降级。
- `tests/e2e/`：本机 Linux 真实 E2E——CPU% ∈ [0,100]、内存 total>0、真实挂载点与网卡、
  5 个真实进程、inode、thermal、两次连续调用 CPU% 不同、单次调用 RSS 增量 ≤2 MiB、耗时 ≤1s。
- `tests/fault/` 四类故障注入（kill / timeout / disk / cgroup）验证方法级 `E_UPSTREAM`。

## 资源

`info.json` 声明常驻 `memory_mb: 6`（单插件目标 12 MiB / 红线 24 MiB 之内），
`capabilities: ["system:read"]`，无触发式唤醒（`triggers: []`）。`monitor.*` 为 lazy 采集：
一次调用完成即释放，不常驻采样。

## License

MIT © pengtang1024

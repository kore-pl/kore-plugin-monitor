# kore-plugin-monitor

语言：[简体中文](docs/DETAIL.md) · [English](docs/DETAIL.en.md) · [Español](docs/DETAIL.es.md) · [Français](docs/DETAIL.fr.md) · [Deutsch](docs/DETAIL.de.md) · [Русский](docs/DETAIL.ru.md) · [Português](docs/DETAIL.pt.md)

kore 的系统监控插件：**真实读取 Linux `/proc` 与 `/sys`**，按需返回系统概览、进程 TOP、
磁盘用量与温度。生产级（`depth_level=production`），属 M5 批 1「无依赖基础件」。

## 设计约束

- **零轮询（G-19）**：插件内**不起 ticker / AfterFunc / 后台 goroutine**。CPU 与进程 CPU%
  都在单次请求内读取两次采样、`time.Sleep(200ms)` 求 delta；调用结束即返回，不常驻。
  历史曲线同样没有后台采集器：只在每次 `monitor.system` 调用时按「距上次采样 ≥ 间隔」
  决定是否追加一个点（`internal/monitor/history.go`），空载时零 CPU。
- **阈值告警只回传、不外发**：超限时在返回体标注 `alerts[]`（指标 / 当前值 / 阈值 / 级别 /
  可操作 hint），插件**不发通知、邮件、Webhook、不出网**（G-10）；通知统一归主壳告警中心。
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
| `monitor.system` | `{"per_cpu":true,"include_loopback":false,"history_points":60,"history_interval_s":60,"thresholds":{"cpu_percent":90,"memory_percent":90,"disk_percent":90}}`（均可选） | `schema/source/sampled_at` + `cpu`（使用率、分核可选）+ `memory`（total/available/used/free/buffers/cached/swap_total/swap_used）+ `load`（load1/5/15、running/total、last_pid）+ `uptime`（uptime_s/idle_s）+ `disks[]` + `network[]` + `alerts[]` + `history` + `warnings[]` |
| `monitor.processes` | `{"limit":20,"sort":"cpu"}`；`limit` ∈ [1,500]，`sort` ∈ `cpu`/`mem` | `schema/sampled_at/sample_interval_ms/sort/limit/count/total` + `items[]`：`{pid, comm, name, state, cpu_percent, mem_rss_bytes, mem_rss_human, mem_percent, threads}` + `warnings[]` |
| `monitor.disks` | `{"path":"/var"}`（可选，绝对路径） | `items[]`：挂载点容量 + `inodes_total/used/free/used_percent` + `/proc/diskstats` 的 `read_bytes/write_bytes/reads/writes`；`path` 为空返回全部真实块设备挂载点，否则返回最长前缀匹配的单个挂载点 |
| `monitor.temperature` | 无 | `available` + `zones[]`：`{zone, temp_c, type}`（`/sys/class/thermal/thermal_zone*`） |

### 历史曲线（`system.history`）

- 短期环形缓冲，默认保留最近 **60** 个采样点（`history_points` ∈ [2,720]），采样间隔默认
  **60 秒**（`history_interval_s` ∈ [1,3600]）。只有距上次采样 ≥ 间隔时才追加，否则原样返回。
- 落盘 `<KORE_STATE_DIR>/history.json`（**0600**，原子替换），进程 / 主壳重启后自动接续。
- 返回**列式数组**，下标一一对应、可直接画折线：
  `timestamps`（Unix 毫秒）+ `cpu_percent[]` + `memory_percent[]` + `load1[]` + `disk_percent[]`，
  另带 `schema/interval_s/capacity/count/appended`。磁盘曲线取 `/` 挂载点，没有 `/` 时取占用最高者。

### 阈值告警（`system.alerts`）

- 阈值可配（0 表示关闭该指标，缺省 90/90/90）；`current ≥ threshold` 即标注告警：
  `{"metric":"cpu|memory|disk","current":93.2,"threshold":90,"level":"warning|critical","hint":"..."}`。
- 超出阈值 ≥10 个百分点记 `critical`，否则 `warning`；`hint` 给出下一步可执行动作
  （如 `monitor.processes {"sort":"cpu"}` / `monitor.disks`）。插件不发送任何通知。

- 磁盘只统计 `/proc/mounts` 中设备为 `/dev/*` 且文件系统非虚拟类型（tmpfs / proc / sysfs /
  overlay / squashfs / cgroup 等）的挂载点；`/dev/fuse` 这类字符设备不计入。
- 网络来自 `/proc/net/dev`，默认排除 `lo`；`include_loopback=true` 可保留。
- 进程 `cpu_percent` 按单核归一（多核满载可 >100），两次采样间退出的进程跳过并计入 warning；
  `name` 是 `comm` 的可读别名（二者相同），`mem_percent = VmRSS / MemTotal`。
- 参数非法回 `E_INVALID`，未知方法回 `E_NOTFOUND`，执行面故障回 `E_UPSTREAM`，非 Linux 回 `E_UNSUPPORTED`。
- `info.json` 为四个采集方法逐一声明 `method_capabilities`（`system:read`），并为
  `monitor.config.get`（`system:read`）/ `monitor.config.set`（`system:admin`）声明配置契约方法；
  插件级 `capabilities` 同时包含两者，内核按方法粒度鉴权与审计。
- **配置契约**（PLUGIN-CONFIG）：`monitor.config.get` 返回 `{schema,values,source}` 表单描述，
  `monitor.config.set` 校验后原子落盘 `<KORE_STATE_DIR>/config.json`（0600）并热生效；
  可配历史曲线点数/间隔、三类告警阈值、进程列表默认条数与排序、请求内采样间隔。
  优先级 `config.json` > `KORE_MONITOR_*` 环境变量 > 代码默认值；全部为请求内读取，
  保存后下一次调用即用新值，`restart_required=false`。无需凭据类字段。

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
  虚拟文件系统过滤、`net/dev`、`processes` 排序与 delta、thermal 缺失、部分文件缺失降级；
  历史曲线追加 / 环形覆盖 / 容量裁剪 / 重启接续 / 间隔未到不追加 / 损坏重建；
  阈值边界（等于 / 超出 / 远离 / 关闭 / 多指标）与 TOP 排序契约。
- 仓根 `method_capabilities_test.go`：读 `info.json` 的方法清单，逐项断言 `Call` 有对应分发分支
  （声明 vs 实现一致性，双向无遗漏）；`g19_test.go`：静态门禁扫描全仓 `.go`，禁止
  `time.NewTicker(` / `time.AfterFunc(` / `time.Tick(`，定时器只能来自内核 `scheduler`。
- `tests/e2e/`：本机 Linux 真实 E2E——CPU% ∈ [0,100]、内存 total>0、真实挂载点与网卡、
  5 个真实进程、inode、thermal、两次连续调用 CPU% 不同、单次调用 RSS 增量 ≤2 MiB、耗时 ≤1s。
- `tests/fault/` 四类故障注入（kill / timeout / disk / cgroup）验证方法级 `E_UPSTREAM`。

## 资源

`info.json` 声明 `lifecycle: "lazy"`（按需冷启、空闲自动退出，不占常驻槽位）、
`memory_mb: 6`（单插件目标 12 MiB / 红线 24 MiB 之内）、`capabilities: ["system:read","system:admin"]`
与六条 `method_capabilities`（四个采集方法 `system:read` + `config.get` `system:read` + `config.set` `system:admin`），无触发式唤醒（`triggers: []`）。
`monitor.*` 为请求内采集：一次调用完成即返回，不常驻采样、零 ticker（G-19）。

## License

MIT © pengtang1024


## 支持本项目

如果 系统监控插件 帮你省了时间，欢迎在 [GitHub](https://github.com/kore-pl/kore-plugin-monitor) 点个 ⭐ Star——这是作者继续维护的最大动力。也欢迎提 issue、PR 或参与讨论。

<!-- oss:begin -->
## 开源与贡献

[![CI](https://github.com/kore-pl/kore-plugin-monitor/actions/workflows/ci.yml/badge.svg)](https://github.com/kore-pl/kore-plugin-monitor/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)

本仓库是 **kore** 的 `系统监控插件`（id `monitor`），独立仓库、独立版本线、独立 CI：GitHub Actions 只手动触发（`workflow_dispatch`），按版本 tag 自动触发在 Gitea Actions（`.gitea/workflows/ci.yml`，push `v*` + 手动）。内核与协议见 [kore](https://github.com/kore-pl/kore)。

- **许可**：MIT（inbound = outbound，不要求 CLA）；提交用 `git commit -s` 签 DCO。
- **贡献**：[CONTRIBUTING.md](CONTRIBUTING.md)（构建、门禁、发版、边界）。
- **安全**：[SECURITY.md](SECURITY.md)——漏洞不要开公开 issue。
- **行为准则**：[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。
- **兼容性**：`protocol: kore.plugin.v1` · `requires_kore: >=0.1.0 <0.2.0` · `lifecycle: lazy` · 深度等级 `production`。
- **验证方式**：本地 `make check` 优先（GitHub CI 只手动触发，Gitea CI 按版本 tag 触发；发布前 `make dist` 连跑两次核对 sha 一致）。
- **发布产物**：`make dist` → `dist/monitor-0.5.0.tar.gz` + `.sha256`（位级可复现，连跑两次 sha 相同）。
- **相关仓库**：[索引 kore-plugins](https://github.com/kore-pl/kore-plugins) · [协议文档](https://github.com/kore-pl/kore/blob/main/docs/PLUGIN-PROTOCOL.md) · [SDK 指南](https://github.com/kore-pl/kore/blob/main/docs/PLUGIN-SDK.md)

<!-- oss:end -->

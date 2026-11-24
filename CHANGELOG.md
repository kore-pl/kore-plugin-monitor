# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.1] - 2026-11-24

### Changed

- **依赖升级：`pkg/pluginapi` v0.1.1 → v0.1.3**（`go.mod` / `go.sum`）。v0.1.3 新增约 2300 行
  （含 `rootpolicy.go` 737 行静态表），编进二进制的只读表从 `.rodata` 迁到 `.data`，包体因此
  增长（0.3.0 的 1344418 B → 0.3.1 实测见索引 `size_bytes`）。**业务源码零改动**：
  `main.go` / `internal/**` 一行未动，四个方法（`system` / `processes` / `disks` / `temperature`）
  的入参与返回契约与 0.3.0 完全一致。
- 同版本必须对应唯一 dist sha（RELEASE §0/§1.1）：依赖升级改变包字节，故以 0.3.1 承载，不覆盖
  `v0.3.0` 的 release 资产与索引记录。

### Added

- **四类边界用例**（`tests/boundary/`，仅测试，不进 dist）：异常输入、并发、权限、资源耗尽。
- **开源资产九件套**（`CONTRIBUTING` / `SECURITY` / `CODE_OF_CONDUCT` / `AGENTS.md` /
  `.github` issue、PR 模板与 CI 少触发）：仅仓库治理文件，不进 dist。

## [0.3.0] - 2026-11-23

### Added

- **方法级 capability 声明**（PLUGIN-MATURITY §3.5 #12）：`info.json` 新增 `method_capabilities`，
  `monitor.system` / `monitor.processes` / `monitor.disks` / `monitor.temperature` 四条只读方法
  全部声明 `system:read`，内核可按方法粒度鉴权与审计；`manifest.json` 同步版本号。
- **历史曲线（短期环形缓冲，零 ticker）**：`monitor.system` 结果新增 `history`——
  `timestamps`（Unix 毫秒）+ `cpu_percent[]` / `memory_percent[]` / `load1[]` / `disk_percent[]`
  列式数组，可直接画折线。默认保留最近 60 点、采样间隔 60s，可用
  `history_points`（2..720）与 `history_interval_s`（1..3600）调整；只在「距上次采样 ≥ 间隔」时
  追加，不做任何后台采集。缓冲以 **0600** 原子落盘到 `<KORE_STATE_DIR>/history.json`，
  进程 / 主壳重启后自动接续；文件损坏只写 warning 并重建，不影响采集。
- **阈值告警**：`monitor.system` 结果新增 `alerts[]`，可配 CPU% / 内存% / 磁盘% 阈值
  （缺省 90/90/90，0 表示关闭），`current ≥ threshold` 即标注
  `{metric,current,threshold,level,hint}`；超出 ≥10 个百分点记 `critical`，否则 `warning`，
  hint 给出下一步命令。插件只回传不发送通知（通知归主壳告警中心，不出网、不违 G-10）。
- **进程 TOP 契约补全**：`monitor.processes` 的 `items[]` 新增 `name`（与 `comm` 相同）与
  `mem_percent`（VmRSS / MemTotal），仍支持 `sort` ∈ `cpu`/`mem` 与 `limit` ∈ [1,500]；
  `mem_percent` 分母读取失败只降级为 0 + warning。
- 测试：声明 vs 实现一致性（读 `info.json` 方法清单逐项断言 `Call` 有分发分支）、历史缓冲
  （追加 / 环形覆盖 / 容量裁剪 / 重启接续 / 间隔未到 / 损坏重建）、阈值边界（等于 / 超出 /
  远离 / 关闭 / 多指标）、TOP 排序（CPU / 内存 / 同值 pid）与 `g19_test.go` 零 ticker 静态门禁；
  非 Linux 以 `!linux` 构建标签断言四个方法均显式 `E_UNSUPPORTED`。

### Changed

- `monitor.system` 新增可选参数 `history_points` / `history_interval_s` / `thresholds`，
  越界参数回 `E_INVALID`；原有 `per_cpu` / `include_loopback` 与返回字段保持不变（向后兼容）。
- 历史曲线与阈值告警都是请求内计算，`lifecycle` 保持 `lazy`，零 ticker / 零 goroutine / 零出网。

## [0.2.1] - 2026-11-23

### Fixed

- **生命周期声明由 `resident` 改为 `lazy`**（P1 事故）：`MaxSlots=3` 下 monitor 作为第 3 个常驻插件
  占满槽位，而 resident 永不驱逐，导致 db-mysql / file / logs / cron / ssl / process / discover /
  container / backup / firewall / redis 共 11 个 lazy 插件唤醒时全部 `429 E_RATELIMIT`（常驻槽位已满）。
- 改为 lazy 后按需冷启、空闲自动退出，不再占据常驻槽位；采集能力不变——`monitor.system` /
  `monitor.processes` / `monitor.disks` / `monitor.temperature` 仍为请求内真实读 `/proc` 与 `/sys`，
  插件本身零 ticker（G-19），lazy 化不引入任何后台轮询。
- `manifest.json` 同步版本号（该文件只含握手字段，不含 `lifecycle`）。

## [0.2.0] - 2026-11-23

### Added

- `monitor.system`：真实读取 `/proc/stat` 两次（间隔 200ms）计算 CPU 使用率（可选 `per_cpu` 分核），
  并汇总 `/proc/meminfo`、`/proc/loadavg`、`/proc/uptime`、`/proc/mounts`（`statfs` 容量）、
  `/proc/net/dev`；单个文件读取失败只写 `warnings[]`，不中断整次调用。
- `monitor.processes {limit,sort}`：两次扫描 `/proc/<pid>/stat` + `status` 求 CPU tick 差分，
  返回 top N 的 `{pid, comm, state, cpu_percent, mem_rss_bytes, threads}`。
- `monitor.disks {path?}`：挂载点容量 + inode 使用率 + `/proc/diskstats` 累计读写；
  `path` 取最长前缀匹配挂载点。
- `monitor.temperature`：读取 `/sys/class/thermal/thermal_zone*/temp`，无 zone 时
  `available=false` + 空数组，不报错。
- 单元测试（CPU delta / meminfo / loadavg / mounts 过滤 / net/dev / processes 排序与 delta /
  thermal 缺失 / 部分文件缺失降级）与 `tests/e2e/` 真实 Linux E2E（含两次 CPU% 不同、
  单次 RSS 增量 ≤2 MiB、单次耗时 ≤1s）。
- 人类可读字段：所有容量/字节字段附 `*_human`（如 `used_human: "3.2 GiB"`），数值仍为 `int64` 字节。

### Changed

- 执行面由 `placeholderExecutor` 替换为真实 `/proc` + `/sys` 采集；CPU% 零 ticker，
  全部用请求内两次采样 + `time.Sleep(200ms)`。
- 非 Linux 平台 `monitor.*` 显式返回 `E_UNSUPPORTED`。
- `README.md` 同步四方法契约、参数、降级与测试口径。

### Removed

- 移除占位方法 `monitor.sample` 与不落盘的 `monitor.alert.set`（0.x 破坏性变更；
  监控采集契约由上述四个真实方法承载）。

## [0.1.1] - 2026-11-23

- build: 位级可复现打包（stage chmod + tar --mode 归一、size_bytes 收敛），二进制改嵌 manifest.json，消除同版本 sha 漂移

## [0.1.0] - 2026-11-22

### Added

- 系统监控插件骨架：复用 site 协议骨架（hello 握手 + 会话循环 + ping/reload/shutdown）。
- `monitor.sample`：结构化占位采样（cpu/memory/disk），不真读 `/proc`，无副作用。
- `monitor.alert.set`：阈值告警配置，校验 metric/comparator/threshold/rule_id，返回结构化确认。
- 四类故障注入测试（kill/timeout/disk/cgroup），验证方法级错误返回（G-20）。
- 确定性打包 Makefile：显式列条目 + gzip -9n，`size_bytes` 与包体积偏差为 0。

### Design notes

- 硬约束 G-19：插件内零 ticker / AfterFunc / 周期循环；采样由宿主按需调用。
- 依赖白名单：`go.mod` 只依赖 `pluginapi v0.1.1`。
- 单文件 ≤ 200 行。

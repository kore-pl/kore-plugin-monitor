# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

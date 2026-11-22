# kore-plugin-monitor

kore 的系统监控插件：采样 + 阈值告警。生产级（`depth_level=production`），
属 M5 批 1「无依赖基础件」，一句话能力对齐 proposal §2.1：**CPU / 内存 / 磁盘采样 + 阈值告警**。

## 设计约束

- **kore 零轮询（G-19）**：插件内**不起 ticker / AfterFunc / 周期循环**。
  采样只暴露方法接口，由宿主按需调用；会话循环纯事件驱动，收帧才处理。
- **骨架口径**：当前为方法骨架，采样返回结构化占位数据（`source: "placeholder"`），
  **不真读 `/proc`**、不读运行配置、无副作用；告警配置只返回结构化确认，不落盘。
- **薄壳依赖**：`go.mod` 只依赖 `github.com/pengtang1024/kore/pkg/pluginapi v0.1.1`，
  无第三方依赖；静态单二进制，`CGO_ENABLED=0` + `-trimpath` + `-s -w`。
- **数据形状显式**：对外结构体字段全部显式列出，不使用 `object` / `any` / `map[string]any`。

## 协议方法

| 方法 | 入参 | 结果 |
| --- | --- | --- |
| `monitor.sample` | `{"metrics":["cpu","memory","disk"]}`（可选，省略=全部） | `SampleResult`：schema/source/sampled_at + cpu/memory/disk 采样字段 |
| `monitor.alert.set` | `{"rule_id","metric","comparator","threshold","enabled"}` | `AlertResult`：rule_id/applied/digest |

- `metrics` 仅允许 `cpu` / `memory` / `disk`。
- `comparator` 仅允许 `gt` / `gte` / `lt` / `lte`；`threshold` 范围 `[0,100]`。
- 参数非法回 `E_INVALID`，未知方法回 `E_NOTFOUND`，执行面故障回 `E_UPSTREAM`。

握手复用 site 骨架：`kore.hello` → 会话循环 → `kore.ping` / `kore.reload` / `kore.shutdown`。

## 构建与验收

```bash
gofmt -l .
go vet ./...
go test -timeout 200s ./...
make build
make dist
```

`make dist` 输出 `dist/monitor-0.1.0.tar.gz`（条目：`info.json`、`bin/monitor`、`SHA256SUMS`），
tar 首条目为 `info.json`；确定性打包保证同源码两次出包 sha256 一致，`size_bytes` 与实测包体积偏差 0。

## 故障注入

`tests/fault/` 四类用例（G-20 / proposal §4.8 S-04）：`kill_test.go`（信号 9）、
`timeout_test.go`（请求超时）、`disk_test.go`（ENOSPC）、`cgroup_test.go`（memory.max）。
每类对两业务方法注入故障，断言 `nil` 结果 + `E_UPSTREAM` + 类别关键词；
`sanityNoFault` 反证断言不是假通过。

## 资源

`info.json` 声明常驻 `memory_mb: 6`（单插件目标 12 MiB / 红线 24 MiB 之内），
`capabilities: ["system:read"]`，无触发式唤醒（`triggers: []`）。

## License

MIT © pengtang1024

# kore-plugin-monitor (system monitoring plugin)

## What it does

Reads real Linux /proc and /sys data and returns on demand a system overview, top processes, disk usage and temperatures. Production grade (depth_level=production), a dependency-free base component.

Read-only methods declare system:read.

## Key points

- No polling (G-19): no ticker, AfterFunc or background goroutine. CPU and process CPU% take two samples within one request with time.Sleep(200ms) between them; the call then returns. History points are appended only when monitor.system is called and the interval has elapsed, so idle costs zero CPU.
- Threshold alerts are returned only: alerts[] carries metric, current value, threshold, level and an actionable hint; the plugin sends no notification, email or webhook and makes no network calls (G-10). Notifications belong to the core alert center.
- No third-party dependencies: only github.com/pengtang1024/kore/pkg/pluginapi plus the standard library.
- Methods: monitor.system, monitor.processes, monitor.disks, monitor.temperature (full parameter tables in DETAIL.md).

## Installation and verification

Install `monitor` from the plugin marketplace or import dist/monitor-0.5.0.tar.gz built with make dist. Verify with make check (gofmt, go vet, go test -timeout 200s), including boundary and fault suites.

---

简体中文原文见 [DETAIL.md](DETAIL.md)。

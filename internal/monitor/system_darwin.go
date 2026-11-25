//go:build darwin

package monitor

// macOS 真实采集：固定 argv 调 sysctl / vm_stat / df -k / netstat / ps / top / powermetrics。
// 平台 API 只在本文件及其同 tag 兄弟文件里出现（G-16 薄壳边界），内核不感知。

const darwinSource = "sysctl/ps"

func newPlatformExecutor() Executor {
	return newShellExecutor("darwin", darwinSource, darwinCommands(), shellParsers{
		system: darwinParseSystem, processes: darwinParseProcesses,
		disks: darwinParseDisks, temperature: darwinParseTemperature,
	})
}

func darwinCommands() shellCommands {
	return shellCommands{
		CPU:         commandSpec{program: "sysctl", args: []string{"-n", "kern.cp_time"}},
		CPUPer:      commandSpec{program: "top", args: []string{"-l", "1", "-stats", "cpu"}},
		MemoryTotal: commandSpec{program: "sysctl", args: []string{"-n", "hw.memsize"}},
		MemoryPages: commandSpec{program: "vm_stat"},
		Load:        commandSpec{program: "sysctl", args: []string{"-n", "vm.loadavg"}},
		BootTime:    commandSpec{program: "sysctl", args: []string{"-n", "kern.boottime"}},
		NCPU:        commandSpec{program: "sysctl", args: []string{"-n", "hw.ncpu"}},
		Disks:       commandSpec{program: "df", args: []string{"-kP"}},
		Network:     commandSpec{program: "netstat", args: []string{"-ib"}},
		Processes:   commandSpec{program: "ps", args: []string{"-axo", "pid=,comm=,stat=,time=,rss=,pcpu=,threads="}},
		Temperature: commandSpec{program: "powermetrics", args: []string{"-n", "1", "-i", "1", "--samplers", "smc"}},
	}
}

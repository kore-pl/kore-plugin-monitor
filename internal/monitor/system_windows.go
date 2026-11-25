//go:build windows

package monitor

// Windows 真实采集：固定 argv 调 PowerShell -NoProfile -Command + 内置 WMI/CIM 计数器。
// 不接受用户输入拼命令（G-10 安全 / G-16 平台专有 API 收在本文件及同 tag 兄弟文件）。

const windowsSource = "powershell/wmi"

func newPlatformExecutor() Executor {
	return newShellExecutor("windows", windowsSource, windowsCommands(), shellParsers{
		system: windowsParseSystem, processes: windowsParseProcesses,
		disks: windowsParseDisks, temperature: windowsParseTemperature,
	})
}

func ps(script string) commandSpec {
	return commandSpec{program: "powershell.exe", args: []string{"-NoProfile", "-NonInteractive", "-Command", script}}
}

func windowsCommands() shellCommands {
	return shellCommands{
		CPU:         ps("$s=Get-CimInstance Win32_PerfFormattedData_PerfOS_Processor | Where-Object {$_.Name -eq '_Total'}; Write-Output ($s.PercentProcessorTime)"),
		CPUPer:      ps("Get-CimInstance Win32_PerfFormattedData_PerfOS_Processor | ForEach-Object { Write-Output ($_.Name + ' ' + $_.PercentProcessorTime) }"),
		MemoryTotal: ps("$o=Get-CimInstance Win32_OperatingSystem; Write-Output ([math]::Round($o.TotalVisibleMemorySize/1024))"),
		MemoryPages: ps("$o=Get-CimInstance Win32_OperatingSystem; Write-Output ([math]::Round($o.FreePhysicalMemory/1024))"),
		Load:        ps("$q=Get-CimInstance Win32_PerfFormattedData_PerfOS_System; Write-Output ($q.ProcessorQueueLength)"),
		BootTime:    ps("$o=Get-CimInstance Win32_OperatingSystem; Write-Output ($o.LastBootUpTime.ToString('o'))"),
		NCPU:        ps("(Get-CimInstance Win32_ComputerSystem).NumberOfLogicalProcessors"),
		Disks:       ps("Get-CimInstance Win32_LogicalDisk -Filter \"DriveType=3\" | ForEach-Object { Write-Output ($_.DeviceID + ' NTFS ' + $_.Size + ' ' + $_.FreeSpace + ' ' + $_.Caption) }"),
		Network:     ps("Get-CimInstance Win32_PerfFormattedData_Tcpip_NetworkInterface | ForEach-Object { Write-Output ($_.Name + ' ' + $_.BytesReceivedPersec + ' ' + $_.BytesSentPersec + ' ' + $_.PacketsReceivedPersec + ' ' + $_.PacketsSentPersec) }"),
		Processes:   ps("Get-Process | ForEach-Object { Write-Output ($_.Id.ToString() + '|' + $_.ProcessName + '|' + [math]::Round($_.CPU,2).ToString() + '|' + [math]::Round($_.WorkingSet64/1KB).ToString() + '|' + $_.Threads.Count.ToString()) }"),
		Temperature: ps("$t=Get-CimInstance -Namespace root/wmi -ClassName MSAcpi_ThermalZoneTemperature -ErrorAction SilentlyContinue; $t | ForEach-Object { Write-Output ($_.InstanceName + ' ' + $_.CurrentTemperature) }"),
	}
}

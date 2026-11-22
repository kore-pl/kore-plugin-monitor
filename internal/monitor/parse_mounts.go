package monitor

import (
	"sort"
	"strings"
)

// virtualFSTypes 是不对应真实块设备的虚拟/伪文件系统，磁盘统计必须排除。
var virtualFSTypes = map[string]bool{
	"autofs": true, "binfmt_misc": true, "bpf": true, "cgroup": true, "cgroup2": true,
	"configfs": true, "debugfs": true, "devpts": true, "devtmpfs": true, "efivarfs": true,
	"fusectl": true, "hugetlbfs": true, "mqueue": true, "nsfs": true, "overlay": true,
	"proc": true, "pstore": true, "ramfs": true, "rootfs": true, "securityfs": true,
	"selinuxfs": true, "squashfs": true, "sysfs": true, "tmpfs": true, "tracefs": true,
}

// mountEntry 是 /proc/mounts 中一个真实块设备挂载点。
type mountEntry struct {
	Device string
	Mount  string
	FSType string
}

// parseMounts 解析 /proc/mounts：只保留设备为 /dev/* 且文件系统不是虚拟类型的挂载点，
// 按挂载点字典序排序并去重（同一挂载点保留最后一条）。
func parseMounts(data string) []mountEntry {
	byMount := make(map[string]mountEntry)
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		device := unescapeMount(fields[0])
		mount := unescapeMount(fields[1])
		fstype := fields[2]
		// /dev/fuse 是字符设备，statfs 只报 0，不是真实块设备挂载点。
		if !strings.HasPrefix(device, "/dev/") || device == "/dev/fuse" || virtualFSTypes[fstype] {
			continue
		}
		byMount[mount] = mountEntry{Device: device, Mount: mount, FSType: fstype}
	}
	entries := make([]mountEntry, 0, len(byMount))
	for _, entry := range byMount {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Mount < entries[j].Mount })
	return entries
}

// unescapeMount 还原 /proc/mounts 的八进制转义（空格 / 制表 / 换行 / 反斜杠）。
func unescapeMount(raw string) string {
	if !strings.Contains(raw, `\`) {
		return raw
	}
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(raw)
}

// matchMount 返回包含 path 的最长前缀挂载点（"path 必须是绝对路径"由参数校验保证）。
func matchMount(mounts []mountEntry, path string) (mountEntry, bool) {
	best := mountEntry{}
	found := false
	for _, entry := range mounts {
		if entry.Mount != "/" && path != entry.Mount && !strings.HasPrefix(path, entry.Mount+"/") {
			continue
		}
		if !found || len(entry.Mount) > len(best.Mount) {
			best, found = entry, true
		}
	}
	return best, found
}

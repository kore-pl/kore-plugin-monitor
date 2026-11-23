package monitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// 历史曲线是请求内维护的短期环形缓冲：每次 monitor.system 调用只在
// 「距上次采样 ≥ interval_s」时追加一个点，否则原样返回已有缓冲。
// 缓冲区以 0600 落盘到 <KORE_STATE_DIR>/history.json，进程/主壳重启后接续。
// 它不引入任何后台定时器、定时回调或常驻 goroutine（G-19）。
const (
	DefaultHistoryPoints   = 60
	MinHistoryPoints       = 2
	MaxHistoryPoints       = 720
	DefaultHistoryInterval = 60 // 秒
	MinHistoryInterval     = 1
	MaxHistoryInterval     = 3600
	historyFileName        = "history.json"
	historyFileVersion     = 1
)

// historyMu 串行化历史文件的读改写；会话循环本身串行，这里只防御并发调用。
var historyMu sync.Mutex

// HistorySample 是历史曲线上的一个采样点（内部存储形状，磁盘与内存共用）。
type HistorySample struct {
	TSUnixMS      int64   `json:"ts_unix_ms"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	Load1         float64 `json:"load1"`
	DiskPercent   float64 `json:"disk_percent"`
}

// historyFile 是 <KORE_STATE_DIR>/history.json 的显式形状（G-24：不使用 map[string]any）。
type historyFile struct {
	Version   int             `json:"version"`
	IntervalS int64           `json:"interval_s"`
	Capacity  int             `json:"capacity"`
	Samples   []HistorySample `json:"samples"`
}

// HistoryResult 是 monitor.system 结果里的历史曲线：时间戳数组 + 各指标数组，
// 下标一一对应，前端直接画折线，不需要二次计算。
type HistoryResult struct {
	Schema        string    `json:"schema"`
	IntervalS     int64     `json:"interval_s"`
	Capacity      int       `json:"capacity"`
	Count         int       `json:"count"`
	Appended      bool      `json:"appended"`
	Timestamps    []int64   `json:"timestamps"`
	CPUPercent    []float64 `json:"cpu_percent"`
	MemoryPercent []float64 `json:"memory_percent"`
	Load1         []float64 `json:"load1"`
	DiskPercent   []float64 `json:"disk_percent"`
}

// historyPath 返回历史文件路径；KORE_STATE_DIR 未设置时返回空串（只保留进程内缓冲）。
func (c *Collector) historyPath() string {
	if c.stateDir == "" {
		return ""
	}
	return filepath.Join(c.stateDir, historyFileName)
}

// recordHistory 读取缓冲并按需追加本次采样点，返回可直接绘图的列式结果。
// 单个文件读写失败只写 warning，不影响本次采集结果。
func (c *Collector) recordHistory(sample HistorySample, params SystemParams, warnings *[]string) HistoryResult {
	path := c.historyPath()
	historyMu.Lock()
	defer historyMu.Unlock()
	file, loadWarning := loadHistoryFile(path)
	if loadWarning != "" {
		addWarning(warnings, "%s", loadWarning)
	}
	file.IntervalS = int64(params.HistoryIntervalS)
	file.Capacity = params.HistoryPoints
	if file.Capacity < 1 {
		file.Capacity = DefaultHistoryPoints
	}
	trimmed := false
	if len(file.Samples) > file.Capacity {
		// 容量被调小时立即裁剪，避免返回超过 N 的旧点。
		file.Samples = append([]HistorySample(nil), file.Samples[len(file.Samples)-file.Capacity:]...)
		trimmed = true
	}
	last := int64(0)
	if len(file.Samples) > 0 {
		last = file.Samples[len(file.Samples)-1].TSUnixMS
	}
	appended := len(file.Samples) == 0 || sample.TSUnixMS-last >= file.IntervalS*1000
	if appended {
		appendHistorySample(&file, sample)
	}
	if (appended || trimmed) && path != "" {
		if err := saveHistoryFile(path, file); err != nil {
			addWarning(warnings, "保存历史曲线失败: %v", err)
		}
	}
	return historyResult(file, appended)
}

// appendHistorySample 追加一个点；容量满后丢弃最旧的点（环形覆盖语义）。
func appendHistorySample(file *historyFile, sample HistorySample) {
	if len(file.Samples) >= file.Capacity {
		copy(file.Samples, file.Samples[len(file.Samples)-file.Capacity+1:])
		file.Samples = file.Samples[:file.Capacity-1]
	}
	file.Samples = append(file.Samples, sample)
}

// historyResult 把内部样本转成列式数组；空缓冲返回空数组而不是 null。
func historyResult(file historyFile, appended bool) HistoryResult {
	result := HistoryResult{
		Schema: "monitor.history.v1", IntervalS: file.IntervalS, Capacity: file.Capacity,
		Count: len(file.Samples), Appended: appended,
		Timestamps: make([]int64, 0, len(file.Samples)), CPUPercent: make([]float64, 0, len(file.Samples)),
		MemoryPercent: make([]float64, 0, len(file.Samples)), Load1: make([]float64, 0, len(file.Samples)),
		DiskPercent: make([]float64, 0, len(file.Samples)),
	}
	for _, sample := range file.Samples {
		result.Timestamps = append(result.Timestamps, sample.TSUnixMS)
		result.CPUPercent = append(result.CPUPercent, sample.CPUPercent)
		result.MemoryPercent = append(result.MemoryPercent, sample.MemoryPercent)
		result.Load1 = append(result.Load1, sample.Load1)
		result.DiskPercent = append(result.DiskPercent, sample.DiskPercent)
	}
	return result
}

// loadHistoryFile 读取历史文件：不存在返回空缓冲；损坏时返回空缓冲 + warning。
func loadHistoryFile(path string) (historyFile, string) {
	empty := historyFile{Version: historyFileVersion, Samples: []HistorySample{}}
	if path == "" {
		return empty, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return empty, ""
		}
		return empty, fmt.Sprintf("读取历史曲线 %s 失败: %v", path, err)
	}
	var file historyFile
	if err := json.Unmarshal(data, &file); err != nil {
		return empty, fmt.Sprintf("历史曲线文件损坏，已重建: %v", err)
	}
	if file.Version != historyFileVersion {
		// 旧版本文件无法解释字段语义时重建，避免把未知形状当成有效曲线。
		return empty, fmt.Sprintf("历史曲线版本 %d 与当前 %d 不一致，已重建", file.Version, historyFileVersion)
	}
	if file.Samples == nil {
		file.Samples = []HistorySample{}
	}
	return file, ""
}

// saveHistoryFile 原子写历史文件（同目录临时文件 0600 → rename），保留 0600 权限。
func saveHistoryFile(path string, file historyFile) error {
	data, err := json.Marshal(file)
	if err != nil {
		return fmt.Errorf("序列化历史曲线: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建历史目录 %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".history-*.tmp")
	if err != nil {
		return fmt.Errorf("创建历史临时文件: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		cleanup()
		return fmt.Errorf("写历史临时文件: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("设置历史文件权限: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("关闭历史临时文件: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("替换历史文件: %w", err)
	}
	return nil
}

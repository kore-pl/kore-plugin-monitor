package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoBackgroundTimers 是 G-19 的静态门禁：全仓 .go（含测试）不得出现周期定时源。
// 令牌用字符串拼接构造，避免门禁文件自身命中字面量；扫描到 0 个文件按失败处理。
func TestNoBackgroundTimers(t *testing.T) {
	forbidden := []string{
		"time." + "NewTicker(",
		"time." + "AfterFunc(",
		"time." + "Tick(",
	}
	files := 0
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "bin", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		content := string(data)
		for _, token := range forbidden {
			if strings.Contains(content, token) {
				t.Errorf("%s 出现被 G-19 禁止的周期定时源 %q（定时器只允许内核 scheduler）", path, token)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历源码: %v", err)
	}
	if files == 0 {
		t.Fatal("未扫描到任何 .go 源文件，门禁形同虚设")
	}
	t.Logf("零 ticker 门禁扫描 %d 个 .go 文件，未发现 NewTicker/AfterFunc/Tick", files)
}

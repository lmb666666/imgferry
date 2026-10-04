// Package store 持久化 {旧URL→新URL} 映射表。
// 映射表是幂等（重跑跳过已迁移）、断点续传与回滚的基础，每完成一次上传即落盘。
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry 记录一次成功迁移。
type Entry struct {
	NewURL     string    `json:"new_url"`
	Name       string    `json:"name"`
	UploadedAt time.Time `json:"uploaded_at"`
}

// Map 是内存中的映射表，带原子落盘能力。
type Map struct {
	path string

	CreatedAt time.Time        `json:"created_at"`
	Entries   map[string]Entry `json:"entries"`
}

// Load 读取映射表；文件不存在时返回空表（不视为错误）。
func Load(path string) (*Map, error) {
	m := &Map{path: path, CreatedAt: time.Now(), Entries: map[string]Entry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("映射表 %s 损坏: %w", path, err)
	}
	if m.Entries == nil {
		m.Entries = map[string]Entry{}
	}
	return m, nil
}

// Get 查询一个旧 URL 的迁移记录。
func (m *Map) Get(oldURL string) (Entry, bool) {
	e, ok := m.Entries[oldURL]
	return e, ok
}

// Set 记录一次迁移结果。
func (m *Map) Set(oldURL string, e Entry) {
	m.Entries[oldURL] = e
}

// Clear 清空全部映射记录（幂等重跑将重新迁移所有链接）。
func (m *Map) Clear() {
	m.Entries = map[string]Entry{}
}

// Save 原子写入映射表（临时文件 + rename）。
func (m *Map) Save() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

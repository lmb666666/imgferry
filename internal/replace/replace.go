// Package replace 将 {旧URL→新URL} 映射精确应用到文件：
// 字节级字符串替换（保留原有转义、换行符等一切格式），原子写入。
package replace

import (
	"os"
	"path/filepath"
	"strings"
)

// Apply 把 mappings 应用到 files。backup 为 true 时先写 path+".bak"
// （已存在 .bak 则保留首次备份，避免用中间状态覆盖最初的原始文件）。
// 返回发生变化的文件数与替换总次数。
func Apply(files []string, mappings map[string]string, backup bool) (changed, replacements int, err error) {
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			return changed, replacements, err
		}
		newContent := content
		fileReplaced := 0
		for old, new := range mappings {
			if c := strings.Count(string(newContent), old); c > 0 {
				newContent = []byte(strings.ReplaceAll(string(newContent), old, new))
				fileReplaced += c
			}
		}
		if fileReplaced == 0 {
			continue
		}
		if backup {
			if err := backupFile(f); err != nil {
				return changed, replacements, err
			}
		}
		if err := writeFileAtomic(f, newContent); err != nil {
			return changed, replacements, err
		}
		changed++
		replacements += fileReplaced
	}
	return changed, replacements, nil
}

func backupFile(path string) error {
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode()
	}
	return os.WriteFile(bak, data, mode)
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".imgferry-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if info, err := os.Stat(path); err == nil {
		if err := tmp.Chmod(info.Mode()); err != nil {
			tmp.Close()
			return err
		}
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

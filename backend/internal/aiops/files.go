package aiops

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxFileBytes = 32 * 1024

// Reject links as well as traversal; os.Root enforces containment again at the actual I/O boundary.
func checkPath(root *os.Root, name string) error {
	if name != "." && !filepath.IsLocal(name) {
		return errors.New("路径必须是工作区内的相对路径")
	}
	if strings.Contains(name, "\\") || strings.ContainsRune(name, 0) {
		return errors.New("无效路径")
	}
	current := ""
	for _, part := range strings.Split(filepath.Clean(name), string(filepath.Separator)) {
		if strings.EqualFold(part, ".git") || strings.HasPrefix(part, ".ai-write-") {
			return errors.New("禁止访问内部元数据")
		}
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("禁止通过符号链接访问文件")
		}
	}
	return nil
}
func readFile(root *os.Root, path string) (map[string]any, error) {
	if err := checkPath(root, path); err != nil {
		return nil, err
	}
	// Nonblocking open prevents a malicious FIFO from hanging a worker before Stat.
	f, err := root.OpenFile(path, os.O_RDONLY|nonblock, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("仅支持普通文本文件")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return nil, errors.New("文件不是 UTF-8 文本或超过 32 KiB")
	}
	hash := sha256.Sum256(data)
	return map[string]any{"content": string(data), "sha256": hex.EncodeToString(hash[:])}, nil
}
func writeFile(root *os.Root, path, content, expected string) (any, error) {
	if path == "." || len(content) > maxFileBytes || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return nil, errors.New("文件内容无效或超过 32 KiB")
	}
	if err := checkPath(root, path); err != nil {
		return nil, err
	}
	mode := os.FileMode(0600)
	info, err := root.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("仅支持普通文本文件")
		}
		old, e := readFile(root, path)
		if e != nil {
			return nil, e
		}
		if expected == "" || old["sha256"] != expected {
			return nil, errors.New("文件已更改或未提供 sha256，请重新读取")
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return nil, err
	} else if expected != "" {
		return nil, errors.New("文件已不存在")
	}
	// Work within the pinned parent handle. Atomic replacement also avoids writing through hard links.
	parent, err := root.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return nil, err
	}
	tmp := ".ai-write-" + hex.EncodeToString(token)
	f, err := parent.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return nil, err
	}
	defer parent.Remove(tmp)
	_, err = io.WriteString(f, content)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err = parent.Rename(tmp, filepath.Base(path)); err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(content))
	return map[string]any{"written": path, "sha256": fmt.Sprintf("%x", hash)}, nil
}

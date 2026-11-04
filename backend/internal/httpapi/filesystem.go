package httpapi

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

type fileEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Directory bool   `json:"is_directory"`
}

// Directory metadata only: this endpoint never returns file contents or changes files.
func (s *Server) browseFilesystem(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		path, _ = os.Getwd()
		if _, err := s.Docker.Allowed(path); err != nil && len(s.Docker.Roots) > 0 {
			path = s.Docker.Roots[0]
		}
	}
	path, err := s.Docker.Allowed(path)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		fail(c, 400, "请选择目录，Compose 文件请在文件列表中勾选")
		return
	}
	dir, err := os.Open(path)
	if err != nil {
		fail(c, 400, fmt.Sprintf("无法打开目录 %s，请检查服务账户的访问权限", path))
		return
	}
	defer dir.Close()
	info, err = dir.Stat()
	if err != nil || !info.IsDir() {
		fail(c, 400, "请选择目录，Compose 文件请在文件列表中勾选")
		return
	}
	offset := bounded(c.Query("offset"), 0, 0, 1000000)
	limit := bounded(c.Query("limit"), 200, 1, 500)
	hidden := c.Query("hidden") == "1"
	// Read incrementally rather than accumulating arbitrarily large directories.
	for skip := 0; skip < offset; {
		batch, readErr := dir.Readdir(min(256, offset-skip))
		skip += len(batch)
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			fail(c, 400, "读取目录失败，请检查访问权限")
			return
		}
	}
	entries := []fileEntry{}
	scanned := offset
	hasMore := false
	for len(entries) < limit {
		if c.Request.Context().Err() != nil {
			return
		}
		batch, readErr := dir.Readdir(1)
		if len(batch) == 0 {
			if readErr != nil && readErr != io.EOF {
				fail(c, 400, "读取目录失败，请检查访问权限")
				return
			}
			break
		}
		scanned++
		item := batch[0]
		name := item.Name()
		if !hidden && strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(path, name)
		isDir := item.IsDir()
		isRegular := item.Mode().IsRegular()
		if item.Mode()&os.ModeSymlink != 0 {
			real, err := s.Docker.Allowed(full)
			if err != nil {
				continue
			}
			target, err := os.Stat(real)
			if err != nil {
				continue
			}
			isDir = target.IsDir()
			isRegular = target.Mode().IsRegular()
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !isDir && (!isRegular || (ext != ".yaml" && ext != ".yml" && ext != ".json")) {
			continue
		}
		entries = append(entries, fileEntry{Name: name, Path: full, Directory: isDir})
	}
	if len(entries) == limit {
		hasMore = true
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Directory != entries[j].Directory {
			return entries[i].Directory
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	parent := filepath.Dir(path)
	if parent == path {
		parent = ""
	} else if _, err = s.Docker.Allowed(parent); err != nil {
		parent = ""
	}
	suggested := []string{}
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
		candidate := filepath.Join(path, name)
		real, err := s.Docker.Allowed(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(real); err == nil && info.Mode().IsRegular() {
			suggested = append(suggested, real)
		}
	}
	c.JSON(200, gin.H{"data": gin.H{"path": path, "parent": parent, "entries": entries, "next_offset": scanned, "has_more": hasMore, "suggested_files": suggested, "restricted": len(s.Docker.Roots) > 0, "roots": s.Docker.Roots}})
}

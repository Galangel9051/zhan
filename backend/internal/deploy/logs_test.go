package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogRedactionAcrossWritesAndPagination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.log")
	out, err := newLog(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	out.Write([]byte("password=split-"))
	out.Write([]byte("secret https://user:token@example.com/repo\n"))
	out.Write([]byte(strings.Repeat("x", 70000)))
	out.Write([]byte(" hidden-token\nlast line\n"))
	if err = out.Close(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "split-secret") || strings.Contains(string(data), "user:token") || strings.Contains(string(data), "hidden-token") {
		t.Fatal("log leaked secret")
	}
	first, offset, err := ReadLog(path, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	second, next, err := ReadLog(path, offset, 1024)
	if err != nil || first+second != string(data) || next != int64(len(data)) {
		t.Fatal("log pagination lost content")
	}
}

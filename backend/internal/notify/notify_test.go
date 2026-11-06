package notify

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"zhan/backend/internal/auth"
	"zhan/backend/internal/database"
	"zhan/backend/internal/model"
)

func TestFailureNotificationDedupAndIndependentError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var delivered atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				fmt.Fprint(conn, "220 fixture ESMTP\r\n")
				reader := bufio.NewReader(conn)
				inData := false
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSpace(line)
					if inData {
						if line == "." {
							inData = false
							delivered.Add(1)
							fmt.Fprint(conn, "250 accepted\r\n")
						}
						continue
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						fmt.Fprint(conn, "250 fixture\r\n")
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						fmt.Fprint(conn, "250 ok\r\n")
					case line == "DATA":
						inData = true
						fmt.Fprint(conn, "354 send data\r\n")
					case line == "QUIT":
						fmt.Fprint(conn, "221 bye\r\n")
						return
					default:
						fmt.Fprint(conn, "250 ok\r\n")
					}
				}
			}()
		}
	}()
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "zhan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(db, dir, 12)
	if err != nil {
		t.Fatal(err)
	}
	n := &Notifier{DB: db, Auth: a}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	cfg := model.SMTP{Host: "127.0.0.1", Port: p, TLS: "none", From: "sender@example.invalid", Recipients: []string{"recipient@example.invalid"}, Stages: []string{"build"}, Cooldown: 30}
	if err = n.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s := model.Stack{Name: "fixture", ProjectName: "fixture", Directory: "/fixture", Files: []string{"compose.yaml"}, Desired: "running", Interval: 300, Notify: true}
	if err = db.SaveStack(&s, true); err != nil {
		t.Fatal(err)
	}
	task := model.Task{StackID: s.ID, Type: "rebuild", Trigger: "manual"}
	db.CreateTask(&task)
	task.Status = "failed"
	task.Stage = "build"
	task.Commit = "commit-a"
	task.Error = "fixture failure"
	task.ExitCode = 42
	db.Finish(task)
	n.Failure(task, s)
	n.Failure(task, s)
	if delivered.Load() != 1 {
		t.Fatalf("mail not deduplicated: %d", delivered.Load())
	}
	cfg.Enabled = new(false)
	if err := n.Save(cfg); err != nil {
		t.Fatal(err)
	}
	task.Commit = "disabled-commit"
	n.Failure(task, s)
	if err := n.TestConfig(cfg); err == nil {
		t.Fatal("disabled test mail accepted")
	}
	if delivered.Load() != 1 {
		t.Fatal("disabled notification sent mail")
	}
	var skippedLogs int
	if err := db.QueryRow("SELECT count(*) FROM notification_logs").Scan(&skippedLogs); err != nil {
		t.Fatal(err)
	}
	if skippedLogs != 1 {
		t.Fatal("disabled mail created delivery records")
	}
	cfg.Enabled = new(true)
	if err := n.Save(cfg); err != nil {
		t.Fatal(err)
	}
	n.Failure(task, s)
	if delivered.Load() != 2 {
		t.Fatal("re-enabling did not resume mail delivery")
	}

	listener.Close()
	task.Commit = "commit-b"
	n.Failure(task, s)
	var notifications int
	db.QueryRow("SELECT count(*) FROM notification_logs WHERE status='failed'").Scan(&notifications)
	if notifications != 1 {
		t.Fatal("SMTP error not recorded")
	}
	stored, _ := db.Task(task.ID)
	if stored.Error != "fixture failure" || stored.ExitCode != 42 {
		t.Fatal("SMTP failure overwrote build failure")
	}
}

func TestUnsavedSMTPTestUsesEnteredPasswordWithoutSaving(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	authValues := make(chan string, 2)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				fmt.Fprint(conn, "220 fixture ESMTP\r\n")
				reader := bufio.NewReader(conn)
				inData := false
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSpace(line)
					if inData {
						if line == "." {
							inData = false
							fmt.Fprint(conn, "250 accepted\r\n")
						}
						continue
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						fmt.Fprint(conn, "250-fixture\r\n250 AUTH PLAIN\r\n")
					case strings.HasPrefix(line, "AUTH PLAIN "):
						value, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
						authValues <- string(value)
						fmt.Fprint(conn, "235 authenticated\r\n")
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						fmt.Fprint(conn, "250 ok\r\n")
					case line == "DATA":
						inData = true
						fmt.Fprint(conn, "354 send data\r\n")
					case line == "QUIT":
						fmt.Fprint(conn, "221 bye\r\n")
						return
					default:
						fmt.Fprint(conn, "250 ok\r\n")
					}
				}
			}()
		}
	}()
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "zhan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(db, dir, 12)
	if err != nil {
		t.Fatal(err)
	}
	n := &Notifier{DB: db, Auth: a}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	entered := model.SMTP{Host: "127.0.0.1", Port: p, TLS: "none", Username: "test-user", Password: "temporary-password", From: "sender@example.invalid", Recipients: []string{"recipient@example.invalid"}, Cooldown: 30}
	if err = n.TestConfig(entered); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-authValues:
		if value != "\x00test-user\x00temporary-password" {
			t.Fatal("entered password was ignored")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP auth was not attempted")
	}
	saved, err := n.Settings()
	if err != nil || saved.Host != "" || saved.PasswordSet {
		t.Fatal("test unexpectedly saved submitted settings or password")
	}
	var count int
	db.QueryRow("SELECT count(*) FROM notification_logs WHERE notification_type='test' AND status='success'").Scan(&count)
	if count != 1 {
		t.Fatal("test result was not recorded")
	}
	if err = n.Save(entered); err != nil {
		t.Fatal(err)
	}
	entered.Password = ""
	if err = n.TestConfig(entered); err != nil {
		t.Fatal("saved password could not be reused: ", err)
	}
	select {
	case value := <-authValues:
		if value != "\x00test-user\x00temporary-password" {
			t.Fatal("saved password was not used")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("saved auth not attempted")
	}
	entered.Username = "changed-user"
	if err = n.TestConfig(entered); err == nil || !strings.Contains(err.Error(), "重新填写密码") {
		t.Fatal("stored password was reused for changed connection settings")
	}
}

func TestSMTPEnableSwitchPreservesConfigurationAndCredentials(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(db, dir, 12)
	if err != nil {
		t.Fatal(err)
	}
	n := &Notifier{DB: db, Auth: a}
	cfg := model.SMTP{Host: "smtp.example.invalid", Port: 587, Username: "sender@example.invalid", Password: "existing-password", From: "sender@example.invalid", Recipients: []string{"recipient@example.invalid"}, TLS: "starttls", Cooldown: 30}
	if err := n.Save(cfg); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.QueryRow("SELECT ciphertext FROM credentials WHERE name='smtp_password'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	cfg.Password = ""
	cfg.Enabled = new(false)
	if err := n.Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := n.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled == nil || *got.Enabled || got.Host != cfg.Host || got.Username != cfg.Username || !got.PasswordSet || len(got.Recipients) != 1 {
		t.Fatalf("configuration lost: %+v", got)
	}
	if err := n.Send(got, "test", "body"); err == nil {
		t.Fatal("disabled sender accepted mail")
	}
	var after string
	if err := db.QueryRow("SELECT ciphertext FROM credentials WHERE name='smtp_password'").Scan(&after); err != nil || before != after {
		t.Fatal("switch replaced saved credential")
	}
	cfg.Enabled = new(true)
	if err := n.Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err = n.Settings()
	if err != nil || got.Enabled == nil || !*got.Enabled || !got.PasswordSet {
		t.Fatal("could not re-enable existing configuration")
	}
	// Old backups and settings omit enabled; their behavior remains host-based.
	cfg.Enabled = nil
	if err := db.Setting("smtp", cfg); err != nil {
		t.Fatal(err)
	}
	got, err = n.Settings()
	if err != nil || got.Enabled == nil || !*got.Enabled {
		t.Fatal("legacy configured SMTP was disabled")
	}
	cfg.Host = ""
	if err := db.Setting("smtp", cfg); err != nil {
		t.Fatal(err)
	}
	got, err = n.Settings()
	if err != nil || got.Enabled == nil || *got.Enabled {
		t.Fatal("unconfigured SMTP reported enabled")
	}
	disabled := model.SMTP{Enabled: new(false), Host: "incomplete host", Cooldown: 30}
	if err := n.Save(disabled); err != nil {
		t.Fatal(err)
	}
	disabled.Enabled = new(true)
	if err := n.Save(disabled); err == nil {
		t.Fatal("enabled incomplete configuration was accepted")
	}
}

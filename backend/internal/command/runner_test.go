package command

import (
	"strings"
	"testing"
)

func TestRedactPrivateKeyAndCredentials(t *testing.T) {
	for _, kind := range []string{"OPENSSH PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY", "PRIVATE KEY", "ENCRYPTED PRIVATE KEY"} {
		t.Run(kind, func(t *testing.T) {
			for _, ending := range []string{"\n-----END " + kind + "-----\nafter", ""} {
				input := "before\n-----BEGIN " + kind + "-----\nprivate-material" + ending
				output := Redact(input)
				if strings.Contains(output, "private-material") || !strings.Contains(output, "[PRIVATE KEY REDACTED]") {
					t.Fatal("private key not redacted")
				}
			}
		})
	}
	got := Redact("token=secret https://user:password@example.invalid Bearer abc123")
	for _, secret := range []string{"=secret", "user:password", "abc123"} {
		if strings.Contains(got, secret) {
			t.Fatal("credential leaked")
		}
	}
}

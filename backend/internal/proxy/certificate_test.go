package proxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDNS01IssuanceAndEncryptedAccount(t *testing.T) {
	for _, email := range []string{"", "test@example.org"} {
		name := "without_email"
		if email != "" {
			name = "with_email"
		}
		t.Run(name, func(t *testing.T) { testDNS01Issuance(t, email) })
	}
}

func testDNS01Issuance(t *testing.T, email string) {
	t.Helper()
	m := setup(t, &fakeRunner{})
	c := fixture()
	c.Hosts[0].HTTPS = true
	c.Settings.DuckDomain = "home.duckdns.org"
	c.Settings.DuckToken = "test-token"
	c.Settings.Email = email
	c.Settings.AgreeTerms = true
	c, e := m.Save(c)
	if e != nil {
		t.Fatal(e)
	}
	c, _ = m.load()
	accepted := false
	txtSet := false
	cleaned := false
	var cert []byte
	m.acmeDirectory = "https://acme.test/directory"
	m.checkTXT = func(ctx context.Context, domain, want string) error {
		if !txtSet || want == "" || domain != c.Hosts[0].Domain {
			t.Fatal("incorrect TXT validation")
		}
		return nil
	}
	m.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		header := http.Header{"Replay-Nonce": []string{base64.RawURLEncoding.EncodeToString([]byte(time.Now().String()))}, "Content-Type": []string{"application/json"}}
		code := 200
		var body any
		if r.URL.Host == "www.duckdns.org" {
			if r.URL.Query().Get("token") != "test-token" {
				t.Fatal("incorrect token")
			}
			if r.URL.Query().Has("txt") {
				if r.URL.Query().Get("clear") == "true" {
					cleaned = true
				} else {
					txtSet = true
				}
			}
			return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader("OK")), Request: r}, nil
		}
		switch r.URL.Path {
		case "/directory":
			body = map[string]any{"newNonce": "https://acme.test/nonce", "newAccount": "https://acme.test/account", "newOrder": "https://acme.test/new-order"}
		case "/nonce":
			body = map[string]any{}
		case "/account":
			var envelope struct{ Payload string }
			if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			raw, err := base64.RawURLEncoding.DecodeString(envelope.Payload)
			if err != nil {
				t.Fatal(err)
			}
			var registration struct{ Contact []string }
			if err := json.Unmarshal(raw, &registration); err != nil {
				t.Fatal(err)
			}
			if email == "" {
				if len(registration.Contact) != 0 {
					t.Fatal("empty email must not send a mailto contact")
				}
			} else if len(registration.Contact) != 1 || registration.Contact[0] != "mailto:"+email {
				t.Fatal("provided email not registered correctly")
			}
			code = 201
			header.Set("Location", "https://acme.test/acct/1")
			body = map[string]any{"status": "valid"}
		case "/new-order":
			code = 201
			header.Set("Location", "https://acme.test/order/1")
			body = map[string]any{"status": "pending", "authorizations": []string{"https://acme.test/auth/1"}, "finalize": "https://acme.test/finalize/1"}
		case "/auth/1":
			status := "pending"
			if accepted {
				status = "valid"
			}
			body = map[string]any{"status": status, "identifier": map[string]string{"type": "dns", "value": c.Hosts[0].Domain}, "challenges": []any{map[string]string{"type": "dns-01", "url": "https://acme.test/challenge/1", "token": "challenge-token", "status": status}}}
		case "/challenge/1":
			accepted = true
			body = map[string]string{"type": "dns-01", "url": "https://acme.test/challenge/1", "token": "challenge-token", "status": "valid"}
		case "/order/1":
			body = map[string]any{"status": "ready", "finalize": "https://acme.test/finalize/1"}
		case "/finalize/1":
			var envelope struct{ Payload string }
			json.NewDecoder(r.Body).Decode(&envelope)
			data, _ := base64.RawURLEncoding.DecodeString(envelope.Payload)
			var payload struct{ CSR string }
			json.Unmarshal(data, &payload)
			der, _ := base64.RawURLEncoding.DecodeString(payload.CSR)
			csr, err := x509.ParseCertificateRequest(der)
			if err != nil {
				t.Fatal(err)
			}
			key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: c.Hosts[0].Domain}, DNSNames: csr.DNSNames, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			der, err = x509.CreateCertificate(rand.Reader, template, template, csr.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			cert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
			body = map[string]any{"status": "valid", "certificate": "https://acme.test/cert/1"}
		case "/cert/1":
			return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(string(cert))), Request: r}, nil
		default:
			t.Fatalf("unexpected ACME request %s", r.URL)
		}
		raw, _ := json.Marshal(body)
		return &http.Response{StatusCode: code, Header: header, Body: io.NopCloser(strings.NewReader(string(raw))), Request: r}, nil
	})}
	if e = m.issue(context.Background(), c.Hosts[0], c.Settings); e != nil {
		t.Fatal(e)
	}
	if !accepted || !cleaned {
		t.Fatal("DNS challenge/cleanup did not complete")
	}
	if _, e = m.certificate(c.Hosts[0], c.Settings); e != nil {
		t.Fatal(e)
	}
	var encrypted string
	if e = m.DB.GetSetting("proxy_acme_account_false", &encrypted); e != nil || strings.Contains(encrypted, "PRIVATE KEY") {
		t.Fatal("account key not encrypted")
	}
	// Reissuing atomically replaces both certificate and private key, keeping nginx paths stable.
	if e = m.issue(context.Background(), c.Hosts[0], c.Settings); e != nil {
		t.Fatal(e)
	}
	if _, e = m.certificate(c.Hosts[0], c.Settings); e != nil {
		t.Fatal(e)
	}
}

func TestCertificateEmailOptionalValidation(t *testing.T) {
	c := fixture()
	c.Settings.DuckDomain = "home.duckdns.org"
	c.Settings.DuckToken = "test-token"
	c.Settings.AgreeTerms = true
	m := &Manager{}
	if err := Normalize(&c); err != nil {
		t.Fatal(err)
	}
	if err := m.validateIssue(c.Hosts[0], c.Settings); err != nil {
		t.Fatalf("empty email rejected: %v", err)
	}
	c.Settings.AgreeTerms = false
	if err := m.validateIssue(c.Hosts[0], c.Settings); err == nil {
		t.Fatal("terms acceptance must still be required")
	}
	c.Settings.Email = "invalid-email"
	if err := Normalize(&c); err == nil {
		t.Fatal("invalid nonempty email accepted")
	}
}

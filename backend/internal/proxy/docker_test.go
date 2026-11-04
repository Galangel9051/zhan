package proxy

import (
	"compose-manager/backend/internal/command"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
func TestManagedNginxHTTPAndHTTPS(t *testing.T) {
	if os.Getenv("CM_PROXY_DOCKER_TEST") != "1" {
		t.Skip("set CM_PROXY_DOCKER_TEST=1 for isolated Docker gateway integration")
	}
	m := setup(t, command.OSRunner{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		m.compose(cleanup, filepath.Join(m.Dir, "compose.yaml"), "down", "--remove-orphans")
	})
	listener, e := net.Listen("tcp4", "0.0.0.0:0")
	if e != nil {
		t.Fatal(e)
	}
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "cm-proxy-fixture|%s|%s", r.Host, r.Header.Get("X-Forwarded-Proto"))
	}))
	up.Listener = listener
	up.Start()
	defer up.Close()
	c := fixture()
	c.Settings.HTTPPort = freePort(t)
	c.Settings.HTTPSPort = freePort(t)
	c.Hosts[0].Upstream = "http://host.docker.internal:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	c.Hosts[0].HTTPS = true
	c.Hosts[0].Redirect = false
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(22), DNSNames: []string{c.Hosts[0].Domain}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	dir := filepath.Join(m.Dir, "certs", certName(c.Hosts[0], c.Settings))
	os.MkdirAll(dir, 0700)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	atomicWrite(filepath.Join(dir, "fullchain.pem"), certPEM)
	atomicWrite(filepath.Join(dir, "privkey.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if _, e = m.execute(ctx, "apply", Host{}, c); e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: c.Hosts[0].Domain}}}
	check := func(scheme string, port int) {
		t.Helper()
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s://127.0.0.1:%d/check", scheme, port), nil)
		req.Host = c.Hosts[0].Domain
		response, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		b, _ := io.ReadAll(response.Body)
		if response.StatusCode != 200 || !strings.Contains(string(b), "cm-proxy-fixture|"+c.Hosts[0].Domain+"|"+scheme) {
			t.Fatalf("upstream failed %d: %s", response.StatusCode, b)
		}
	}
	check("http", c.Settings.HTTPPort)
	check("https", c.Settings.HTTPSPort)
	broken := c
	broken.Hosts = append([]Host{}, c.Hosts...)
	broken.Hosts[0].Upstream = "http://cm-proxy-does-not-exist.invalid:1234"
	if _, e = m.execute(ctx, "apply", Host{}, broken); e == nil {
		t.Fatal("expected nginx -t failure")
	}
	check("https", c.Settings.HTTPSPort)
	c.Hosts[0].Redirect = true
	if _, e = m.execute(ctx, "apply", Host{}, c); e != nil {
		t.Fatal(e)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/path?q=1", c.Settings.HTTPPort), nil)
	req.Host = c.Hosts[0].Domain
	resp, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 308 || resp.Header.Get("Location") != fmt.Sprintf("https://%s:%d/path?q=1", c.Hosts[0].Domain, c.Settings.HTTPSPort) {
		t.Fatalf("invalid redirect: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if e = m.reload(ctx); e != nil {
		t.Fatal(e)
	}
}

package proxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"golang.org/x/crypto/acme"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const stagingURL = "https://acme-staging-v02.api.letsencrypt.org/directory"

func (m *Manager) validateIssue(h Host, s Settings) error {
	if s.DuckDomain == "" || s.DuckToken == "" {
		return errors.New("请先保存 DuckDNS 主域名与 Token")
	}
	if h.Domain != s.DuckDomain && !strings.HasSuffix(h.Domain, "."+s.DuckDomain) {
		return fmt.Errorf("自动证书仅支持 %s 及其子域名", s.DuckDomain)
	}
	if !s.AgreeTerms || s.Email == "" {
		return errors.New("请先填写证书联系邮箱并同意 Let's Encrypt 服务条款")
	}
	return nil
}
func (m *Manager) duck(ctx context.Context, s Settings, txt string, clear bool) error {
	token, err := m.Auth.Decrypt(s.DuckToken)
	if err != nil {
		return errors.New("DuckDNS Token 解密失败，请重新保存 Token")
	}
	q := url.Values{"domains": {strings.TrimSuffix(s.DuckDomain, ".duckdns.org")}, "token": {token}}
	if txt != "" || clear {
		q.Set("txt", txt)
		if clear {
			q.Set("clear", "true")
		}
	} else {
		q.Set("ip", s.DuckIP)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://www.duckdns.org/update?"+q.Encode(), nil)
	if err != nil {
		return errors.New("无法创建 DuckDNS 请求")
	}
	client := &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if m.httpClient != nil {
		client = m.httpClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("连接 DuckDNS 失败，请检查服务器网络后重试")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil || resp.StatusCode != 200 || strings.TrimSpace(string(b)) != "OK" {
		return errors.New("DuckDNS 更新失败：请检查域名是否已注册、Token 是否正确，以及 API 访问频率")
	}
	return nil
}
func (m *Manager) account(staging bool) (*ecdsa.PrivateKey, error) {
	setting := fmt.Sprintf("proxy_acme_account_%t", staging)
	var encrypted string
	err := m.DB.GetSetting(setting, &encrypted)
	if err == nil {
		raw, e := m.Auth.Decrypt(encrypted)
		if e != nil {
			return nil, e
		}
		block, _ := pem.Decode([]byte(raw))
		if block == nil {
			return nil, errors.New("ACME 账户密钥无效")
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	raw, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	encrypted, err = m.Auth.Encrypt(string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: raw})))
	if err != nil {
		return nil, err
	}
	if err = m.DB.Setting(setting, encrypted); err != nil {
		return nil, err
	}
	return key, nil
}
func waitTXT(ctx context.Context, domain, want string) error {
	// Query public recursive DNS so split-horizon LAN DNS does not hide ACME TXT records.
	for {
		for _, address := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
			resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, address)
			}}
			records, err := resolver.LookupTXT(ctx, "_acme-challenge."+domain)
			if err == nil {
				for _, r := range records {
					if r == want {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待 DNS TXT 传播超时，请检查 DNS 或稍后重试：%w", ctx.Err())
		case <-time.After(8 * time.Second):
		}
	}
}
func (m *Manager) issue(ctx context.Context, h Host, s Settings) error {
	if err := m.validateIssue(h, s); err != nil {
		return err
	}
	key, err := m.account(s.Staging)
	if err != nil {
		return err
	}
	directory := acme.LetsEncryptURL
	if s.Staging {
		directory = stagingURL
	}
	if m.acmeDirectory != "" {
		directory = m.acmeDirectory
	}
	transport := m.httpClient
	if transport == nil {
		transport = &http.Client{Timeout: 30 * time.Second}
	}
	client := &acme.Client{Key: key, DirectoryURL: directory, HTTPClient: transport, UserAgent: "Compose-Manager/1.0"}
	_, err = client.Register(ctx, &acme.Account{Contact: []string{"mailto:" + s.Email}}, acme.AcceptTOS)
	if err != nil {
		return fmt.Errorf("注册证书账户失败：%w", err)
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(h.Domain))
	if err != nil {
		return fmt.Errorf("创建证书订单失败：%w", err)
	}
	for _, authURL := range order.AuthzURLs {
		az, err := client.GetAuthorization(ctx, authURL)
		if err != nil {
			return err
		}
		if az.Status == acme.StatusValid {
			continue
		}
		var challenge *acme.Challenge
		for _, v := range az.Challenges {
			if v.Type == "dns-01" {
				challenge = v
				break
			}
		}
		if challenge == nil {
			return errors.New("证书机构没有提供 DNS-01 验证")
		}
		txt, err := client.DNS01ChallengeRecord(challenge.Token)
		if err != nil {
			return err
		}
		if err = m.duck(ctx, s, txt, false); err != nil {
			return err
		}
		// DuckDNS TXT is shared by all child domains; manager serializes issuance and DDNS.
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			_ = m.duck(cleanup, s, "", true)
		}()
		propagation, cancel := context.WithTimeout(ctx, 4*time.Minute)
		check := m.checkTXT
		if check == nil {
			check = waitTXT
		}
		err = check(propagation, h.Domain, txt)
		cancel()
		if err != nil {
			return err
		}
		if _, err = client.Accept(ctx, challenge); err != nil {
			return err
		}
		if _, err = client.WaitAuthorization(ctx, authURL); err != nil {
			return fmt.Errorf("DNS 验证失败：%w", err)
		}
	}
	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return err
	}
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{}, DNSNames: []string{h.Domain}}, certKey)
	if err != nil {
		return err
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return fmt.Errorf("签发证书失败：%w", err)
	}
	var certPEM []byte
	for _, der := range chain {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	keyDER, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if err = cert.VerifyHostname(h.Domain); err != nil {
		return err
	}
	// Switch both files atomically using a relative directory symlink inside the bind mount.
	base := filepath.Join(m.Dir, "certs")
	version, err := os.MkdirTemp(base, ".version-")
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(version)
		}
	}()
	if err = atomicWrite(filepath.Join(version, "fullchain.pem"), certPEM); err != nil {
		return err
	}
	if err = atomicWrite(filepath.Join(version, "privkey.pem"), keyPEM); err != nil {
		return err
	}
	link := filepath.Join(base, certName(h, s))
	pending := version + "-link"
	if err = os.Symlink(filepath.Base(version), pending); err != nil {
		return err
	}
	defer os.Remove(pending)
	if err = os.Rename(pending, link); err != nil {
		return err
	}
	ok = true
	return nil
}

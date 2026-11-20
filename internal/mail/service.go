package mail

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	netmail "net/mail"
	"strings"
	"time"

	secret "github.com/nyakooo/meldmail/internal/crypto"
	"github.com/nyakooo/meldmail/internal/mail/adapter"
	"github.com/nyakooo/meldmail/internal/mail/auth"
	"github.com/nyakooo/meldmail/internal/mail/imapclient"
	"github.com/nyakooo/meldmail/internal/mail/smtpclient"
	"github.com/nyakooo/meldmail/internal/mail/syncer"
	"github.com/nyakooo/meldmail/internal/proxy"
	"github.com/nyakooo/meldmail/internal/sse"
	"github.com/nyakooo/meldmail/internal/storage"
)

type Service struct {
	Store       *storage.Store
	Cipher      *secret.Cipher
	Limiter     *syncer.Limiter
	TLSConfig   *tls.Config
	Events      *sse.Broker
	Workers     *syncer.Manager
	interactive chan struct{}
	bodyMemory  chan struct{}
	flagWake    chan struct{}
}

func New(store *storage.Store, key []byte) (*Service, error) {
	cipher, err := secret.New(key)
	if err != nil {
		return nil, err
	}
	return &Service{Store: store, Cipher: cipher, Limiter: syncer.NewLimiter(), Events: sse.New(), interactive: make(chan struct{}, 3), bodyMemory: make(chan struct{}, 1), flagWake: make(chan struct{}, 1)}, nil
}

type AccountInput struct {
	storage.Account
	Password    string  `json:"password"`
	AccessToken string  `json:"access_token"`
	ProxyURL    *string `json:"proxy_url"`
}

func (s *Service) Prepare(ctx context.Context, input AccountInput, existing *storage.Account) (storage.Account, error) {
	a := input.Account
	if existing != nil {
		a.ID = existing.ID
	}
	a.Email = strings.TrimSpace(a.Email)
	address, err := netmail.ParseAddress(a.Email)
	if err != nil || address.Address != a.Email {
		return a, fmt.Errorf("account: invalid email")
	}
	if a.Provider == "" {
		a.Provider = "custom"
	}
	if p, ok := adapter.Provider(a.Provider); ok {
		if a.IMAPHost == "" {
			a.IMAPHost = p.IMAPHost
		}
		if a.IMAPPort == 0 {
			a.IMAPPort = p.IMAPPort
		}
		if a.IMAPTLS == "" {
			a.IMAPTLS = p.IMAPTLS
		}
		if a.SMTPHost == "" {
			a.SMTPHost = p.SMTPHost
		}
		if a.SMTPPort == 0 {
			a.SMTPPort = p.SMTPPort
		}
		if a.SMTPTLS == "" {
			a.SMTPTLS = p.SMTPTLS
		}
	} else if a.Provider != "custom" {
		return a, fmt.Errorf("account: unknown provider")
	}
	for _, host := range []string{a.IMAPHost, a.SMTPHost} {
		if host == "" || strings.ContainsAny(host, "/\\\r\n\t :") && net.ParseIP(host) == nil {
			return a, fmt.Errorf("account: invalid server hostname")
		}
	}
	if a.IMAPPort < 1 || a.IMAPPort > 65535 || a.SMTPPort < 1 || a.SMTPPort > 65535 {
		return a, fmt.Errorf("account: ports must be 1-65535")
	}
	if (a.IMAPTLS != "implicit" && a.IMAPTLS != "starttls") || (a.SMTPTLS != "implicit" && a.SMTPTLS != "starttls") {
		return a, fmt.Errorf("account: use implicit TLS or STARTTLS")
	}
	if a.AuthType == "" {
		a.AuthType = "password"
	}
	if a.AuthType != "password" && a.AuthType != "oauth2" {
		return a, fmt.Errorf("account: unsupported authentication")
	}
	if len(a.DisplayName) > 256 || len(a.Email) > 320 || len(a.IMAPHost) > 253 || len(a.SMTPHost) > 253 {
		return a, fmt.Errorf("account: field too long")
	}
	if a.Status == "" {
		a.Status = "active"
	}
	if a.Status != "active" && a.Status != "paused" {
		return a, fmt.Errorf("account: invalid status")
	}
	a.CredentialsEnc = nil
	a.ProxyURLEnc = nil
	if input.Password != "" || input.AccessToken != "" {
		if a.AuthType == "password" && input.Password == "" || a.AuthType == "oauth2" && input.AccessToken == "" {
			return a, fmt.Errorf("account: credentials do not match authentication type")
		}
		credentials := auth.Credentials{Password: input.Password, AccessToken: input.AccessToken}
		encoded, err := json.Marshal(credentials)
		if err != nil {
			return a, fmt.Errorf("account credentials JSON: %w", err)
		}
		a.CredentialsEnc, err = s.Cipher.Encrypt(encoded)
		if err != nil {
			return a, err
		}
	} else if existing != nil && existing.AuthType == a.AuthType {
		a.CredentialsEnc = existing.CredentialsEnc
	} else {
		return a, fmt.Errorf("account: credentials are required")
	}
	if a.ProxyMode == "" {
		a.ProxyMode = "global"
	}
	endpoint := ""
	if input.ProxyURL != nil {
		endpoint = *input.ProxyURL
	} else if existing != nil && len(existing.ProxyURLEnc) > 0 {
		plain, err := s.Cipher.Decrypt(existing.ProxyURLEnc)
		if err != nil {
			return a, err
		}
		endpoint = string(plain)
	}
	if _, err := proxy.New(a.ProxyMode, endpoint, ""); err != nil {
		return a, err
	}
	if endpoint != "" && a.ProxyMode != "direct" && a.ProxyMode != "global" {
		a.ProxyURLEnc, err = s.Cipher.Encrypt([]byte(endpoint))
		if err != nil {
			return a, err
		}
	}
	return a, nil
}
func (s *Service) Credentials(a storage.Account) (auth.Credentials, error) {
	plain, err := s.Cipher.Decrypt(a.CredentialsEnc)
	if err != nil {
		return auth.Credentials{}, err
	}
	var credentials auth.Credentials
	if err := json.Unmarshal(plain, &credentials); err != nil {
		return credentials, fmt.Errorf("account decode credentials: %w", err)
	}
	return credentials, nil
}
func (s *Service) GlobalProxy(ctx context.Context) (string, error) {
	value, err := s.Store.Setting(ctx, "global_proxy_enc")
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("proxy decode setting: %w", err)
	}
	plain, err := s.Cipher.Decrypt(data)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
func (s *Service) SetGlobalProxy(ctx context.Context, endpoint string) error {
	if _, err := proxy.New("global", "", endpoint); err != nil {
		return err
	}
	if endpoint == "" {
		return s.Store.PutSetting(ctx, "global_proxy_enc", "")
	}
	cipher, err := s.Cipher.Encrypt([]byte(endpoint))
	if err != nil {
		return err
	}
	return s.Store.PutSetting(ctx, "global_proxy_enc", base64.StdEncoding.EncodeToString(cipher))
}
func (s *Service) Dialer(ctx context.Context, a storage.Account) (*proxy.Dialer, error) {
	global := ""
	if a.ProxyMode == "global" || a.ProxyMode == "" {
		var err error
		global, err = s.GlobalProxy(ctx)
		if err != nil {
			return nil, err
		}
	}
	endpoint := ""
	if len(a.ProxyURLEnc) > 0 {
		plain, err := s.Cipher.Decrypt(a.ProxyURLEnc)
		if err != nil {
			return nil, err
		}
		endpoint = string(plain)
	}
	return proxy.New(a.ProxyMode, endpoint, global)
}
func (s *Service) Sync(ctx context.Context, id int64) (count int, result error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	release, err := s.Limiter.Acquire(ctx, id)
	if err != nil {
		return 0, err
	}
	defer release()
	a, err := s.Store.Account(ctx, id)
	if err != nil {
		return 0, err
	}
	if a.Status == "paused" {
		return 0, fmt.Errorf("sync: account is paused")
	}
	credentials, err := s.Credentials(a)
	if err != nil {
		return 0, err
	}
	d, err := s.Dialer(ctx, a)
	if err != nil {
		return 0, err
	}
	client, err := imapclient.Open(ctx, a, credentials, d, s.TLSConfig)
	if err != nil {
		return 0, fmt.Errorf("sync connect: %w", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	return client.Sync(ctx, s.Store, a, func(e storage.Email) error { return s.Events.Publish("new_email", e) })
}

type Diagnostic struct {
	Stage   string `json:"stage"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func (s *Service) Test(ctx context.Context, a storage.Account) []Diagnostic {
	result := make([]Diagnostic, 0, 3)
	d, err := s.Dialer(ctx, a)
	if err != nil {
		return append(result, Diagnostic{"proxy", false, "代理配置无效"})
	}
	result = append(result, Diagnostic{"proxy", true, "代理配置有效；IMAP/SMTP 连接均通过该拨号器"})
	credentials, err := s.Credentials(a)
	if err != nil {
		return append(result, Diagnostic{"credentials", false, "凭据解密失败"})
	}
	imapCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	c, err := imapclient.Open(imapCtx, a, credentials, d, s.TLSConfig)
	if err != nil {
		result = append(result, Diagnostic{"imap", false, diagnose(err)})
	} else {
		err = c.Close()
		result = append(result, Diagnostic{"imap", err == nil, "IMAP TLS、认证与服务商适配完成"})
	}
	cancel()
	smtpCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	smtp, err := smtpclient.Open(smtpCtx, a, credentials, d, s.TLSConfig)
	if err != nil {
		result = append(result, Diagnostic{"smtp", false, diagnose(err)})
	} else {
		err = smtp.Close()
		result = append(result, Diagnostic{"smtp", err == nil, "SMTP TLS 与认证完成（未发送邮件）"})
	}
	return result
}
func diagnose(err error) string {
	// Never expose untrusted server response text, proxy credentials or auth data.
	text := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(text, "timeout"):
		return "连接超时，请检查网络、端口或代理"
	case strings.Contains(text, "proxy"):
		return "代理连接或 CONNECT/SOCKS5 握手失败"
	case strings.Contains(text, "tls") || strings.Contains(text, "certificate"):
		return "TLS 握手或证书校验失败"
	case strings.Contains(text, "authentication") || strings.Contains(text, "login"):
		return "认证失败，请检查授权码或 OAuth2 配置"
	case strings.Contains(text, "provider id"):
		return "服务商 IMAP ID 握手失败"
	case strings.Contains(text, "dial"):
		return "服务器连接失败，请检查主机名、端口与网络"
	default:
		return "协议握手失败，请检查服务商设置"
	}
}

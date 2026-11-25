package mail

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	secret "github.com/nyakooo/meldmail/internal/crypto"
	"github.com/nyakooo/meldmail/internal/proxy"
	"github.com/nyakooo/meldmail/internal/storage"
	"golang.org/x/crypto/pbkdf2"
)

const configIterations = 600000
const maxConfigBytes = 512 * 1024

var ErrInvalidConfig = errors.New("配置密码不正确或文件已损坏")

// The portable envelope contains no plaintext account or credential fields.
type ConfigBundle struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	KDF     string `json:"kdf"`
	Salt    []byte `json:"salt"`
	Data    []byte `json:"data"`
}
type configAccount struct {
	AccountInput
	RefreshToken string `json:"refresh_token,omitempty"`
}
type configPayload struct {
	Accounts    []configAccount `json:"accounts"`
	GlobalProxy string          `json:"global_proxy,omitempty"`
}
type ConfigImportResult struct {
	Added      int `json:"added"`
	Updated    int `json:"updated"`
	Duplicates int `json:"duplicates"`
}

func validConfigPassword(password string) bool {
	return utf8.RuneCountInString(password) >= 12 && len(password) <= 512
}
func configCipher(password string, salt []byte) (*secret.Cipher, error) {
	if !validConfigPassword(password) || len(salt) != 16 {
		return nil, ErrInvalidConfig
	}
	key := pbkdf2.Key([]byte(password), salt, configIterations, 32, sha256.New)
	defer clear(key)
	return secret.New(key)
}
func (s *Service) configSlot(ctx context.Context) (func(), error) {
	select {
	case s.configWork <- struct{}{}:
		return func() { <-s.configWork }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s *Service) ExportConfig(ctx context.Context, password string) (ConfigBundle, error) {
	release, err := s.configSlot(ctx)
	if err != nil {
		return ConfigBundle{}, err
	}
	defer release()
	if !validConfigPassword(password) {
		return ConfigBundle{}, errors.New("配置密码至少 12 个字符，最多 512 字节")
	}
	accounts, err := s.Store.Accounts(ctx)
	if err != nil {
		return ConfigBundle{}, err
	}
	if len(accounts) == 0 || len(accounts) > 100 {
		return ConfigBundle{}, errors.New("可导出 1 至 100 个邮箱配置")
	}
	payload := configPayload{Accounts: make([]configAccount, 0, len(accounts))}
	for _, account := range accounts {
		credentials, err := s.Credentials(account)
		if err != nil {
			return ConfigBundle{}, err
		}
		endpoint := ""
		if len(account.ProxyURLEnc) > 0 {
			plain, err := s.Cipher.Decrypt(account.ProxyURLEnc)
			if err != nil {
				return ConfigBundle{}, err
			}
			endpoint = string(plain)
			clear(plain)
		}
		account.ID = 0
		account.LastError = ""
		account.LastSyncAt = 0
		account.CreatedAt = 0
		account.UpdatedAt = 0
		if account.Status != "paused" {
			account.Status = "active"
		}
		payload.Accounts = append(payload.Accounts, configAccount{AccountInput: AccountInput{Account: account, Password: credentials.Password, AccessToken: credentials.AccessToken, ProxyURL: &endpoint}, RefreshToken: credentials.RefreshToken})
	}
	payload.GlobalProxy, err = s.GlobalProxy(ctx)
	if err != nil {
		return ConfigBundle{}, err
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return ConfigBundle{}, fmt.Errorf("config encode: %w", err)
	}
	defer clear(plain)
	if len(plain) > maxConfigBytes {
		return ConfigBundle{}, errors.New("配置文件超过大小限制")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return ConfigBundle{}, fmt.Errorf("config salt: %w", err)
	}
	cipher, err := configCipher(password, salt)
	if err != nil {
		return ConfigBundle{}, err
	}
	data, err := cipher.Encrypt(plain)
	if err != nil {
		return ConfigBundle{}, err
	}
	if err := ctx.Err(); err != nil {
		return ConfigBundle{}, err
	}
	return ConfigBundle{Format: "meldmail-config", Version: 1, KDF: "pbkdf2-sha256-600000", Salt: salt, Data: data}, nil
}
func (s *Service) ImportConfig(ctx context.Context, password string, bundle ConfigBundle) (ConfigImportResult, error) {
	result := ConfigImportResult{}
	release, err := s.configSlot(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	if bundle.Format != "meldmail-config" || bundle.Version != 1 || bundle.KDF != "pbkdf2-sha256-600000" || len(bundle.Data) > maxConfigBytes+28 {
		return result, ErrInvalidConfig
	}
	cipher, err := configCipher(password, bundle.Salt)
	if err != nil {
		return result, ErrInvalidConfig
	}
	plain, err := cipher.Decrypt(bundle.Data)
	if err != nil {
		return result, ErrInvalidConfig
	}
	defer clear(plain)
	var payload configPayload
	if err := json.Unmarshal(plain, &payload); err != nil || len(payload.Accounts) == 0 || len(payload.Accounts) > 100 {
		return result, ErrInvalidConfig
	}
	if _, err := proxy.New("global", "", payload.GlobalProxy); err != nil {
		return result, errors.New("导入的默认代理配置无效")
	}
	// Last complete connection wins; an omitted credential never erases an earlier one.
	unique := make([]configAccount, 0, len(payload.Accounts))
	positions := make(map[string]int)
	for _, input := range payload.Accounts {
		key := strings.ToLower(strings.TrimSpace(input.Email))
		input.Email = key
		input.ID = 0
		if index, ok := positions[key]; ok {
			previous := unique[index]
			if input.DisplayName == "" {
				input.DisplayName = previous.DisplayName
			}
			if input.Password == "" {
				input.Password = previous.Password
			}
			if input.AccessToken == "" {
				input.AccessToken = previous.AccessToken
			}
			if input.RefreshToken == "" {
				input.RefreshToken = previous.RefreshToken
			}
			if input.ProxyURL == nil {
				input.ProxyURL = previous.ProxyURL
			}
			unique[index] = input
			result.Duplicates++
		} else {
			positions[key] = len(unique)
			unique = append(unique, input)
		}
	}
	prepared := make([]storage.Account, 0, len(unique))
	for _, input := range unique {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		account, err := s.Prepare(ctx, input.AccountInput, nil)
		if err != nil {
			return result, errors.New("邮箱配置不完整或无效，未写入任何配置")
		}
		if input.RefreshToken != "" {
			credentials, err := s.Credentials(account)
			if err != nil {
				return result, err
			}
			credentials.RefreshToken = input.RefreshToken
			data, err := json.Marshal(credentials)
			if err != nil {
				return result, err
			}
			account.CredentialsEnc, err = s.Cipher.Encrypt(data)
			clear(data)
			if err != nil {
				return result, err
			}
		}
		prepared = append(prepared, account)
	}
	var global *string
	if payload.GlobalProxy != "" {
		data, err := s.Cipher.Encrypt([]byte(payload.GlobalProxy))
		if err != nil {
			return result, err
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		global = &encoded
	}
	result.Added, result.Updated, err = s.Store.MergeAccounts(ctx, prepared, global)
	if err != nil {
		return ConfigImportResult{}, err
	}
	s.AccountsChanged()
	return result, nil
}

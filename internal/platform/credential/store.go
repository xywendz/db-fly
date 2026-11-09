// Package credential 负责连接密码等敏感信息的加密存储。
// 配置与凭证分离：config.json 永不包含密码，密码单独放在 secrets.json。
package credential

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// ErrNotFound 表示指定键没有对应凭证。
var ErrNotFound = errors.New("credential: 未找到凭证")

// Cipher 是平台相关的加解密实现端口。
type Cipher interface {
	Name() string
	Encrypt(plain []byte) ([]byte, error)
	Decrypt(data []byte) ([]byte, error)
}

// Store 是凭证存储端口，Windows 使用 DPAPI，其他平台使用主密码 + AES-GCM。
type Store interface {
	Get(key string) (string, error)
	Put(key, value string) error
	Delete(key string) error
	Keys() ([]string, error)
	Name() string
}

type fileFormat struct {
	Version int               `json:"version"`
	Cipher  string            `json:"cipher"`
	Items   map[string]string `json:"items"`
}

// FileStore 将加密后的凭证以 JSON 文件保存。
type FileStore struct {
	path   string
	cipher Cipher
	mu     sync.Mutex
}

// New 创建凭证存储，dir 为数据目录。
func New(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建凭证目录失败: %w", err)
	}
	c, err := platformCipher(dir)
	if err != nil {
		return nil, err
	}
	return &FileStore{path: filepath.Join(dir, "secrets.json"), cipher: c}, nil
}

func (s *FileStore) Name() string { return s.cipher.Name() }

func (s *FileStore) read() (fileFormat, error) {
	f := fileFormat{Version: 1, Cipher: s.cipher.Name(), Items: make(map[string]string)}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("读取凭证库失败: %w", err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("凭证库格式错误: %w", err)
	}
	if f.Items == nil {
		f.Items = make(map[string]string)
	}
	if f.Cipher != "" && f.Cipher != s.cipher.Name() {
		return f, fmt.Errorf("凭证库由 %s 加密，当前实现为 %s，无法读取", f.Cipher, s.cipher.Name())
	}
	return f, nil
}

func (s *FileStore) write(f fileFormat) error {
	f.Version = 1
	f.Cipher = s.cipher.Name()
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(b, '\n'), 0o600)
}

func (s *FileStore) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.read()
	if err != nil {
		return "", err
	}
	encoded, ok := f.Items[key]
	if !ok {
		return "", ErrNotFound
	}
	// 空密码不经过加密（DPAPI 拒绝空输入），以空标记存储。
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("凭证编码错误: %w", err)
	}
	plain, err := s.cipher.Decrypt(raw)
	if err != nil {
		return "", fmt.Errorf("解密凭证失败（可能更换了系统用户或主密码）: %w", err)
	}
	return string(plain), nil
}

func (s *FileStore) Put(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	encoded := ""
	if value != "" {
		encrypted, err := s.cipher.Encrypt([]byte(value))
		if err != nil {
			return err
		}
		encoded = base64.StdEncoding.EncodeToString(encrypted)
	}
	f, err := s.read()
	if err != nil {
		return err
	}
	f.Items[key] = encoded
	return s.write(f)
}

func (s *FileStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.read()
	if err != nil {
		return err
	}
	if _, ok := f.Items[key]; !ok {
		return nil
	}
	delete(f.Items, key)
	return s.write(f)
}

func (s *FileStore) Keys() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(f.Items))
	for k := range f.Items {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

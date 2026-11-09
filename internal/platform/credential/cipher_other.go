//go:build !windows

package credential

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// 非 Windows 平台使用主密码 + AES-GCM。
// 主密码来自环境变量 DB_FLY_MASTER_KEY（兼容旧变量 DBCLIENT_MASTER_KEY）；未设置时退化为机器本地派生值，
// 安全性较弱，CLI 会在启动时给出警告。生产环境建议显式设置该变量。

const (
	masterKeyEnv       = "DB_FLY_MASTER_KEY"
	legacyMasterKeyEnv = "DBCLIENT_MASTER_KEY"
)

type aesCipher struct {
	key [32]byte
}

func (aesCipher) Name() string { return "aes-gcm" }

func platformCipher(dir string) (Cipher, error) {
	saltPath := filepath.Join(dir, "credential.salt")
	salt, err := os.ReadFile(saltPath)
	if errors.Is(err, os.ErrNotExist) {
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return nil, fmt.Errorf("生成盐值失败: %w", err)
		}
		if err := os.WriteFile(saltPath, salt, 0o600); err != nil {
			return nil, fmt.Errorf("写入盐值失败: %w", err)
		}
	} else if err != nil {
		return nil, err
	}

	secret := os.Getenv(masterKeyEnv)
	if secret == "" {
		secret = os.Getenv(legacyMasterKeyEnv)
	}
	if secret == "" {
		secret = localFallbackSecret()
	}
	return &aesCipher{key: deriveKey(secret, salt)}, nil
}

func deriveKey(secret string, salt []byte) [32]byte {
	seed := make([]byte, 0, len(secret)+len(salt))
	seed = append(seed, secret...)
	seed = append(seed, salt...)
	key := sha256.Sum256(seed)
	for i := 0; i < 10000; i++ {
		key = sha256.Sum256(key[:])
	}
	return key
}

func localFallbackSecret() string {
	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	// 兼容升级：本地派生种子保持旧值，改动会导致已有 secrets.json 无法解密。
	return "dbclient-local|" + host + "|" + home + "|" + os.Getenv("USER") + os.Getenv("USERNAME")
}

func (c *aesCipher) Encrypt(plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func (c *aesCipher) Decrypt(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("密文过短")
	}
	nonce, body := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	return gcm.Open(nil, nonce, body, nil)
}

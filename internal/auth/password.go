// Package auth 负责用户库（PBKDF2 哈希）与内存会话。
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
)

// PBKDF2Iter 是哈希迭代次数（OWASP 对 PBKDF2-HMAC-SHA256 的建议量级）。
const PBKDF2Iter = 600_000

const (
	saltLen = 16
	keyLen  = 32
	// MinPasswordLen 是密码最小长度。
	MinPasswordLen = 8
)

// ErrWeakPassword 表示密码过短。
var ErrWeakPassword = fmt.Errorf("密码至少需要 %d 位", MinPasswordLen)

// HashPassword 返回 base64 盐、base64 派生密钥与迭代次数。
func HashPassword(password string) (salt, hash string, iter int, err error) {
	raw := make([]byte, saltLen)
	if _, err := rand.Read(raw); err != nil {
		return "", "", 0, fmt.Errorf("生成盐失败: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, raw, PBKDF2Iter, keyLen)
	if err != nil {
		return "", "", 0, fmt.Errorf("派生密钥失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw),
		base64.StdEncoding.EncodeToString(key),
		PBKDF2Iter, nil
}

// VerifyPassword 用常量时间比较校验密码。
func VerifyPassword(password, saltB64, hashB64 string, iter int) bool {
	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(hashB64)
	if err != nil || len(want) == 0 {
		return false
	}
	if iter <= 0 {
		iter = PBKDF2Iter
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePassword 生成 16 位随机密码，排除易混字符（0/O/1/l/I）。
func GeneratePassword() string {
	const n = 16
	out := make([]byte, n)
	limit := big.NewInt(int64(len(passwordAlphabet)))
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, limit)
		if err != nil {
			panic(err) // crypto/rand 失败意味着系统熵不可用
		}
		out[i] = passwordAlphabet[idx.Int64()]
	}
	return string(out)
}

func validatePassword(pw string) error {
	if len(pw) < MinPasswordLen {
		return ErrWeakPassword
	}
	return nil
}

// ErrNoUsers 表示用户库为空。
var ErrNoUsers = errors.New("用户库为空")

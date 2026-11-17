//go:build platform

package platformauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// 同一进程最多两个哈希任务和十六个等待任务，防止认证请求耗尽内存。
type passwordHasher struct {
	running  chan struct{}
	admitted chan struct{}
}

var processPasswordHasher = newPasswordHasher()

func newPasswordHasher() *passwordHasher {
	return &passwordHasher{running: make(chan struct{}, 2), admitted: make(chan struct{}, 18)}
}

func validatePassword(password string) error {
	if len(password) > 512 || !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || utf8.RuneCountInString(password) > 128 {
		return invalid("密码长度必须为 12—128 个字符")
	}
	return nil
}

func (h *passwordHasher) derive(ctx context.Context, password string, salt []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case h.admitted <- struct{}{}:
	default:
		return nil, busyPassword()
	}
	defer func() { <-h.admitted }()
	select {
	case h.running <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-h.running }()
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32), nil
}

func (h *passwordHasher) hash(ctx context.Context, password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := h.derive(ctx, password, salt)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func (h *passwordHasher) verify(ctx context.Context, password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// 仅接受当前发布参数，损坏的凭据不能控制内存或计算量。
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=1" {
		return false, nil
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false, nil
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != 32 {
		return false, nil
	}
	key, err := h.derive(ctx, password, salt)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(key, want) == 1, nil
}

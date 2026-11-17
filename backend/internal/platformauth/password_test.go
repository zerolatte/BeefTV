//go:build platform

package platformauth

import (
	"context"
	"strings"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hasher := newPasswordHasher()
	ctx := context.Background()
	first, err := hasher.hash(ctx, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hasher.hash(ctx, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("相同密码必须使用独立随机盐")
	}
	for _, encoded := range []string{first, second} {
		valid, err := hasher.verify(ctx, testPassword, encoded)
		if err != nil || !valid {
			t.Fatalf("密码哈希不能还原验证: %v", err)
		}
		valid, err = hasher.verify(ctx, replacementPassword, encoded)
		if err != nil || valid {
			t.Fatalf("错误密码被接受: %v", err)
		}
	}
	if _, err := hasher.hash(ctx, "short"); err == nil {
		t.Fatal("不允许弱长度密码")
	}
	if _, err := hasher.hash(ctx, strings.Repeat("a", 129)); err == nil {
		t.Fatal("不允许超长密码")
	}
	if _, err := hasher.hash(ctx, strings.Repeat("中", 12)); err != nil {
		t.Fatal("密码长度应按字符计数", err)
	}
	valid, err := hasher.verify(ctx, testPassword, "$argon2id$v=19$m=4294967295,t=3,p=1$AAAA$AAAA")
	if err != nil || valid {
		t.Fatal("损坏参数不应触发不受限分配")
	}
}

package identity

import "testing"

func TestPasswordHashAndCSRF(t *testing.T) {
	hash, err := hashPassword("测试密码至少十二个字符必须足够长")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(hash, "测试密码至少十二个字符必须足够长") || verifyPassword(hash, "different-password") {
		t.Fatal("密码校验错误")
	}
	second, err := hashPassword("测试密码至少十二个字符必须足够长")
	if err != nil || hash == second {
		t.Fatal("盐没有随机生成")
	}
	if _, err := hashPassword("short"); err == nil {
		t.Fatal("接受短密码")
	}
	token, err := randomToken()
	if err != nil || len(token) != 43 {
		t.Fatal("会话令牌生成错误")
	}
	if ValidCSRF(nil, token) {
		t.Fatal("空会话不能通过 CSRF 校验")
	}
}

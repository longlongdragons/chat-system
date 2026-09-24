package auth

import "golang.org/x/crypto/bcrypt"

// HashPassword 用 bcrypt 对明文密码做不可逆哈希（含随机盐），用于注册和改密时落库。
// 使用 DefaultCost（当前为 10），在安全性与登录接口耗时之间取平衡。
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPassword 校验登录时提交的明文密码是否与库中哈希匹配。
// bcrypt 的比较是常量时间的，避免通过响应耗时侧信道猜测密码前缀。
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

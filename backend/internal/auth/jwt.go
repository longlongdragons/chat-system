// Package auth 提供聊天系统的认证基础设施：基于 RSA 私钥签名的
// JWT（访问令牌 + 刷新令牌）签发与校验，以及用户密码的 bcrypt 哈希。
// 令牌采用"短 TTL 访问令牌 + 长 TTL 刷新令牌 + TokenVersion 版本号"的组合，
// 支持在改密、封号等场景下通过递增版本号整体吊销用户的既有令牌。
package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// 令牌类型标识，写入 Claims.Type，用于防止访问令牌与刷新令牌混用
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

var (
	ErrInvalidToken   = errors.New("invalid token")
	ErrWrongTokenType = errors.New("wrong token type")
)

// Claims 是令牌中携带的业务载荷。
// TokenVer 对应用户表中的 token_version：用户改密、被踢下线时版本号递增，
// 旧令牌在校验阶段即被拒绝；DeviceID 用于区分多端登录。
type Claims struct {
	UserID   int64  `json:"uid"`
	DeviceID string `json:"did"`
	TokenVer int    `json:"tv"`
	Type     string `json:"typ"`
	jwt.RegisteredClaims
}

// Manager 持有 RSA 密钥对与两类令牌的有效期，负责令牌的签发与校验。
// 采用 RS256（非对称签名）：签发侧持私钥，校验只需公钥，便于将来拆分部署。
type Manager struct {
	priv       *rsa.PrivateKey
	pub        *rsa.PublicKey
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

// NewManager 从磁盘加载 RSA 密钥（不存在则自动生成并落盘），构造令牌管理器。
func NewManager(privPath, pubPath string, accessTTL, refreshTTL time.Duration) (*Manager, error) {
	priv, pub, err := loadOrGenerateKeys(privPath, pubPath)
	if err != nil {
		return nil, err
	}
	return &Manager{priv: priv, pub: pub, AccessTTL: accessTTL, RefreshTTL: refreshTTL}, nil
}

// loadOrGenerateKeys 优先读取已有私钥（兼容 PKCS#1 / PKCS#8 两种 PEM 格式），
// 读不到时生成 2048 位新密钥并写入磁盘，保证重启后已签发的令牌仍可校验。
func loadOrGenerateKeys(privPath, pubPath string) (*rsa.PrivateKey, *rsa.PublicKey, error) {
	if privPath != "" {
		if raw, err := os.ReadFile(privPath); err == nil {
			block, _ := pem.Decode(raw)
			if block == nil {
				return nil, nil, errors.New("invalid private key PEM")
			}
			if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
				return k, &k.PublicKey, nil
			}
			anyKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, nil, err
			}
			k, ok := anyKey.(*rsa.PrivateKey)
			if !ok {
				return nil, nil, errors.New("not an RSA private key")
			}
			return k, &k.PublicKey, nil
		}
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	// 首次启动：把新密钥持久化。私钥权限 0o600 仅属主可读，公钥 0o644 可分发。
	if privPath != "" {
		_ = os.MkdirAll(filepath.Dir(privPath), 0o700)
		pemBytes := pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key),
		})
		if err := os.WriteFile(privPath, pemBytes, 0o600); err != nil {
			return nil, nil, err
		}
	}
	if pubPath != "" {
		der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			return nil, nil, err
		}
		_ = os.MkdirAll(filepath.Dir(pubPath), 0o700)
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
		if err := os.WriteFile(pubPath, pemBytes, 0o644); err != nil {
			return nil, nil, err
		}
	}
	return key, &key.PublicKey, nil
}

// issue 按指定类型与有效期签发一枚令牌。jti 用 "用户ID-纳秒时间戳" 保证唯一，
// 便于审计或将来做令牌黑名单。
func (m *Manager) issue(userID int64, deviceID string, tokenVer int, typ string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		DeviceID: deviceID,
		TokenVer: tokenVer,
		Type:     typ,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "chat",
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        fmt.Sprintf("%d-%d", userID, now.UnixNano()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(m.priv)
}

// IssuePair 登录或刷新成功后一次性签发访问令牌与刷新令牌，
// 二者共享同一 TokenVer，保证吊销时成对失效。
func (m *Manager) IssuePair(userID int64, deviceID string, tokenVer int) (access, refresh string, err error) {
	access, err = m.issue(userID, deviceID, tokenVer, TokenTypeAccess, m.AccessTTL)
	if err != nil {
		return
	}
	refresh, err = m.issue(userID, deviceID, tokenVer, TokenTypeRefresh, m.RefreshTTL)
	return
}

// Verify 校验令牌签名、有效期与签发者，解析出 Claims。
// 显式限定 RSA 系列签名算法，防止攻击者换用 "none" 或 HS256 等算法伪造令牌。
// 注意：此处只验密码学有效性，不区分令牌类型，也不比对 TokenVer——
// 类型与版本的检查由调用方（如 Auth 中间件）按业务场景完成。
func (m *Manager) Verify(token string) (*Claims, error) {
	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, ErrInvalidToken
		}
		return m.pub, nil
	}, jwt.WithIssuer("chat"))
	if err != nil {
		return nil, err
	}
	if !parsed.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

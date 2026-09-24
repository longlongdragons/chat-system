// Package config 负责从环境变量加载服务配置。
// REST 服务（cmd/api）与 WebSocket 网关（cmd/gateway）共用同一套配置定义，
// 未设置的环境变量回落到本地开发默认值，便于开箱即用地启动整个系统。
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 汇总服务运行所需的全部配置项
type Config struct {
	ServerID    string // 节点标识，多网关部署时用于区分消息来源节点
	HTTPAddr    string // REST 服务监听地址
	WSAddr      string // WebSocket 网关监听地址
	DatabaseURL string // PostgreSQL 连接串
	RedisAddr   string
	RedisPass   string
	RedisDB     int

	JWTPrivateKeyPath string        // RSA 私钥，用于签发 JWT
	JWTPublicKeyPath  string        // RSA 公钥，网关侧用于验签
	AccessTTL         time.Duration // 访问令牌有效期
	RefreshTTL        time.Duration // 刷新令牌有效期

	AllowedOrigins []string // CORS 允许的前端来源
	UploadDir      string   // 附件本地存储目录
	PublicBaseURL  string   // REST 服务外部地址，用于生成附件下载链接

	AdminUsername string // 管理后台账号，为空则禁用管理员登录
	AdminPassword string
}

// Load 读取环境变量并填充默认值。默认值面向本地开发环境，
// 生产部署时应通过环境变量显式覆盖（尤其是密钥路径和管理员口令）。
func Load() *Config {
	c := &Config{
		ServerID:    env("SERVER_ID", hostname()),
		HTTPAddr:    env("HTTP_ADDR", ":8080"),
		WSAddr:      env("WS_ADDR", ":8081"),
		DatabaseURL: env("DATABASE_URL", "postgres://chat:chat@localhost:5432/chat?sslmode=disable"),
		RedisAddr:   env("REDIS_ADDR", "localhost:6379"),
		RedisPass:   env("REDIS_PASSWORD", ""),
		RedisDB:     envInt("REDIS_DB", 0),

		JWTPrivateKeyPath: env("JWT_PRIVATE_KEY", "./secrets/jwt_rsa.pem"),
		JWTPublicKeyPath:  env("JWT_PUBLIC_KEY", "./secrets/jwt_rsa.pub.pem"),
		AccessTTL:         envDur("ACCESS_TTL", 15*time.Minute),
		RefreshTTL:        envDur("REFRESH_TTL", 7*24*time.Hour),

		AllowedOrigins: strings.Split(env("ALLOWED_ORIGINS", "http://localhost:5173,http://localhost:3000"), ","),
		UploadDir:      env("UPLOAD_DIR", "./data/uploads"),
		PublicBaseURL:  env("PUBLIC_BASE_URL", "http://localhost:8080"),

		AdminUsername: env("ADMIN_USERNAME", ""),
		AdminPassword: env("ADMIN_PASSWORD", ""),
	}
	return c
}

// env 读取字符串环境变量，为空时返回默认值 d
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// envInt 读取整型环境变量，缺失或解析失败时返回默认值 d
func envInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return d
}

// envDur 读取时长环境变量（如 "15m"、"168h"），缺失或解析失败时返回默认值 d
func envDur(k string, d time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if n, err := time.ParseDuration(v); err == nil {
			return n
		}
	}
	return d
}

// hostname 取主机名作为默认节点标识，取不到时回落为 "server-1"
func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "server-1"
	}
	return h
}

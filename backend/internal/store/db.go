// Package store 负责聊天系统的基础设施层：PostgreSQL 连接池、
// Redis 客户端的创建，以及内嵌 SQL 文件的数据库结构迁移。
// cmd/api（HTTP 服务）与 cmd/gateway（WebSocket 网关）共用同一套
// 存储连接，所有会话、消息等核心业务数据均落库到 PostgreSQL。
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewDB 按给定 DSN 创建 PostgreSQL 连接池并验证连通性。
// 连接池参数（最大 40 连接、连接存活 1 小时、空闲回收 30 分钟）
// 针对 api 与 gateway 两个长驻服务的高并发读写场景做了保守调优，
// 避免连接无限堆积；创建后立即 Ping，启动期就能发现数据库不可达，
// 而不是等到第一个请求才报错。
func NewDB(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 40
	cfg.MinConns = 4
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

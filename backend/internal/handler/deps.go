// Package handler 承载 REST API（cmd/api）的全部路由处理器。
//
// 该包是从 cmd/api/main.go 拆分出来的薄业务层：main 只负责进程级装配
// （配置加载、依赖构造、迁移、健康检查、优雅关停），具体每个路由的请求
// 校验、业务调用与响应拼装都收敛在本包内，按域拆分为 auth / user /
// conversation / message / attachment / admin 六个文件。
// 拆分严格保持行为不变：路由表、中间件挂载、响应格式、错误码与状态码
// 均与拆分前逐一对应。
package handler

import (
	"github.com/example/chat/internal/audit"
	"github.com/example/chat/internal/auth"
	"github.com/example/chat/internal/config"
	"github.com/example/chat/internal/conversation"
	"github.com/example/chat/internal/storage"
	"github.com/example/chat/internal/user"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Deps 是路由处理器的依赖注入容器：把各域处理器共享的基础设施与仓储
// 聚合成一个结构体，处理器以 *Deps 方法的形式实现，避免每个路由单独
// 传一长串参数。
type Deps struct {
	DB    *pgxpool.Pool      // PostgreSQL 连接池（附件元数据、举报、审计查询等直写 SQL 使用）
	RDB   *redis.Client      // Redis 客户端（限流等中间件使用）
	Users *user.Repo         // 用户仓储
	Convs *conversation.Repo // 会话仓储
	JWT   *auth.Manager      // JWT 签发/验签管理器
	Store storage.Storage    // 附件对象存储
	Audit *audit.Logger      // 异步审计日志器
	Cfg   *config.Config     // 服务配置
}

// NewDeps 构造路由处理器依赖容器。全部参数均为 main 中已装配好的单例，
// 本函数只做聚合，不做任何初始化或 IO。
func NewDeps(db *pgxpool.Pool, rdb *redis.Client, users *user.Repo, convs *conversation.Repo,
	jwtMgr *auth.Manager, objStore storage.Storage, auditLog *audit.Logger, cfg *config.Config) *Deps {
	return &Deps{
		DB:    db,
		RDB:   rdb,
		Users: users,
		Convs: convs,
		JWT:   jwtMgr,
		Store: objStore,
		Audit: auditLog,
		Cfg:   cfg,
	}
}

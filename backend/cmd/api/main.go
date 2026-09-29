// 本程序是 chat-system 的无状态 HTTP REST API 服务（cmd/api），
// 对外提供认证、用户资料、会话管理、历史消息、已读上报、附件上传下载、
// 用户举报和管理后台接口。它与 cmd/gateway（WebSocket 长连接网关）共享
// PostgreSQL 与 Redis，实时投递由网关经 Redis Pub/Sub 通道完成。
// main 只做进程级装配（配置、依赖、迁移、路由挂载、优雅关停），
// 具体路由与处理器实现见 internal/handler 包。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/chat/internal/audit"
	"github.com/example/chat/internal/auth"
	"github.com/example/chat/internal/config"
	"github.com/example/chat/internal/conversation"
	"github.com/example/chat/internal/handler"
	"github.com/example/chat/internal/middleware"
	"github.com/example/chat/internal/moderation"
	"github.com/example/chat/internal/storage"
	"github.com/example/chat/internal/store"
	"github.com/example/chat/internal/user"
	"github.com/gin-gonic/gin"
)

// main 启动 API 服务：装配依赖、注册路由，收到退出信号后优雅关停。
func main() {
	cfg := config.Load()
	ctx := context.Background()

	db, err := store.NewDB(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer db.Close()

	// 启动时自动执行数据库迁移，保证表结构与代码版本一致
	if err := store.Migrate(ctx, db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	rdb := store.NewRedis(cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB)
	defer rdb.Close()

	jwtMgr, err := auth.NewManager(cfg.JWTPrivateKeyPath, cfg.JWTPublicKeyPath, cfg.AccessTTL, cfg.RefreshTTL)
	if err != nil {
		log.Fatalf("jwt: %v", err)
	}

	users := user.NewRepo(db)
	convs := conversation.NewRepo(db)
	moderationEngine := moderation.NewEngine(db)
	// 加载审核（敏感词）规则；失败仅记日志不阻断启动，避免审核模块故障拖垮整个 API
	if err := moderationEngine.Reload(ctx); err != nil {
		log.Printf("moderation reload failed (non-fatal): %v", err)
	}
	auditLog := audit.NewLogger(db)

	objStore, err := storage.NewLocal(cfg.UploadDir, cfg.PublicBaseURL)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	ensureAdmin(ctx, users, cfg)

	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger(), middleware.CORS(cfg.AllowedOrigins))
	r.MaxMultipartMemory = 32 << 20

	// 健康检查：同时探测 PostgreSQL 与 Redis，任一依赖不可用即返回 503，
	// 供负载均衡/容器编排做存活与就绪判断
	r.GET("/healthz", func(c *gin.Context) {
		if err := db.Ping(c.Request.Context()); err != nil {
			c.JSON(503, gin.H{"status": "db_down"})
			return
		}
		if err := rdb.Ping(c.Request.Context()).Err(); err != nil {
			c.JSON(503, gin.H{"status": "redis_down"})
			return
		}
		c.JSON(200, gin.H{"status": "ok", "server_id": cfg.ServerID})
	})

	// 全部业务路由集中在 handler 包注册，保持路由表一处可查
	deps := handler.NewDeps(db, rdb, users, convs, jwtMgr, objStore, auditLog, cfg)
	handler.RegisterRoutes(r, deps)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("api server listening on %s (server_id=%s)", cfg.HTTPAddr, cfg.ServerID)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("api listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	// 阻塞等待 SIGINT/SIGTERM，收到后 15 秒内优雅关停，等待在途请求处理完毕
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down api server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("api shutdown error: %v", err)
	}
	log.Println("api server stopped")
}

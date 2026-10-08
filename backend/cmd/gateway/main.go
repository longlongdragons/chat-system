// Package main 是 WebSocket 长连接网关（cmd/gateway）的启动入口。
//
// 网关是无状态 HTTP REST 服务（cmd/api）之外的另一条进程：它只负责维护
// 客户端的 WebSocket 长连接，处理消息的实时收发、已读回执、撤回、断线补拉
// 和在线状态维护。网关与 API 服务共享 PostgreSQL 和 Redis，多个网关节点之间
// 通过 Redis Pub/Sub 通道 chat.broadcast 互投广播事件，实现水平扩容。
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

	"github.com/example/chat/internal/auth"
	"github.com/example/chat/internal/bus"
	"github.com/example/chat/internal/config"
	"github.com/example/chat/internal/conversation"
	"github.com/example/chat/internal/message"
	"github.com/example/chat/internal/moderation"
	"github.com/example/chat/internal/presence"
	"github.com/example/chat/internal/ratelimit"
	"github.com/example/chat/internal/store"
	"github.com/example/chat/internal/user"
	"github.com/example/chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 初始化共享基础设施：PostgreSQL（消息、会话等持久化数据）与
	// Redis（在线状态、离线队列、跨节点广播总线）。
	db, err := store.NewDB(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer db.Close()

	rdb := store.NewRedis(cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB)
	defer rdb.Close()

	jwtMgr, err := auth.NewManager(cfg.JWTPrivateKeyPath, cfg.JWTPublicKeyPath, cfg.AccessTTL, cfg.RefreshTTL)
	if err != nil {
		log.Fatalf("jwt: %v", err)
	}

	users := user.NewRepo(db)
	convs := conversation.NewRepo(db)
	presenceStore := presence.NewStore(rdb)
	redisBus := bus.NewRedisBus(rdb)
	defer redisBus.Close()

	moderationEngine := moderation.NewEngine(db)
	if err := moderationEngine.Reload(ctx); err != nil {
		// 敏感词库加载失败只记日志不退出：发消息时由消息服务决定兜底策略
		log.Printf("moderation reload failed: %v", err)
	}

	// hub 是本网关节点的本地连接注册表；Register/Unregister 会同步维护
	// Redis 中的在线状态，跨节点投递则经 Redis Pub/Sub 到达。
	// 传入会话仓储，使 presence 上线/离线事件能精准扇出给共同会话成员；
	// MaxConns/MaxConnsPerUser 是连接数护栏，超限的新连接将被拒绝注册。
	hub := ws.NewHub(ctx, cfg.ServerID, redisBus, presenceStore, ws.HubConfig{
		Convs:           convs,
		MaxConns:        cfg.WSMaxConns,
		MaxConnsPerUser: cfg.WSMaxConnsPerUser,
		Buckets:         cfg.WSHubBuckets,
	})

	// 消息服务负责落库、序号分配、敏感词过滤等业务逻辑；
	// SetBroadcaster 把 hub 注入进去，使 REST/WS 两侧发出的消息都能经网关扇出。
	msgSvc := message.NewService(db, rdb, redisBus, convs, presenceStore, moderationEngine)
	msgSvc.SetBroadcaster(hub)

	// 订阅 chat.broadcast 通道：其他网关节点（或 API 服务）发布的事件
	// 会被本节点接收并推送给落在本节点上的目标用户连接。
	if err := hub.StartSubscriber(ctx); err != nil {
		log.Fatalf("subscribe broadcast: %v", err)
	}

	// 消息发送限流器：按用户做滑动窗口限流（与 HTTP 接口限流共用同一 Redis 实现）
	msgLimiter := ratelimit.New(rdb)
	handler := ws.NewHandler(jwtMgr, users, convs, msgSvc, presenceStore, hub, cfg.AllowedOrigins,
		msgLimiter, cfg.WSMsgRateLimit)

	r := gin.New()
	r.Use(gin.Recovery())
	// 网关只暴露一个业务路由 /ws（升级 WebSocket），其余为运维探活与监控指标。
	r.GET("/ws", handler.ServeWS)
	// 健康检查：上报本节点 id 与本地连接/用户数，便于负载均衡和容量观测。
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":      "ok",
			"server_id":   cfg.ServerID,
			"local_conns": hub.LocalConnCount(),
			"local_users": hub.LocalUserCount(),
		})
	})
	r.GET("/metrics", func(c *gin.Context) {
		c.String(200, "# TYPE ws_connections gauge\nws_connections{server=%q} %d\n",
			cfg.ServerID, hub.LocalConnCount())
	})

	srv := &http.Server{
		Addr:              cfg.WSAddr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("ws gateway listening on %s (server_id=%s)", cfg.WSAddr, cfg.ServerID)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("gateway listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// 优雅停机顺序：先让客户端感知并主动重连到其他节点，再停止接入，
	// 最后给在途消息留出处理时间，尽量避免消息丢失。
	log.Println("shutting down gateway...")
	// 1) 通知客户端重连
	hub.Shutdown("server_shutdown")
	// 2) 停止接受新连接
	shutdownCtx, sc := context.WithTimeout(context.Background(), 10*time.Second)
	defer sc()
	_ = srv.Shutdown(shutdownCtx)
	// 3) 等待现有消息处理完毕
	time.Sleep(2 * time.Second)
	cancel()
	log.Println("gateway stopped")
}

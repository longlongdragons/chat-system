// 本程序是 chat-system 的无状态 HTTP REST API 服务（cmd/api），
// 对外提供注册/登录/刷新/登出等认证接口，以及用户资料、会话管理、
// 历史消息、已读上报、附件上传下载、用户举报和管理后台（封禁、审计日志）。
// 它与 cmd/gateway（WebSocket 长连接网关）共享 PostgreSQL 与 Redis，
// 本身不负责消息的实时收发，实时投递由网关经 Redis Pub/Sub 通道完成。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/example/chat/internal/audit"
	"github.com/example/chat/internal/auth"
	"github.com/example/chat/internal/config"
	"github.com/example/chat/internal/conversation"
	"github.com/example/chat/internal/httpx"
	"github.com/example/chat/internal/middleware"
	"github.com/example/chat/internal/model"
	"github.com/example/chat/internal/moderation"
	"github.com/example/chat/internal/storage"
	"github.com/example/chat/internal/store"
	"github.com/example/chat/internal/user"
	"github.com/gin-gonic/gin"
)

// main 启动 API 服务：装配数据库、Redis、JWT、对象存储等依赖，
// 注册全部 HTTP 路由，并在收到退出信号后优雅关停。
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

	// 管理员初始化：配置了管理员账号且库中尚不存在时，自动创建超级管理员，
	// 便于全新部署的环境直接获得管理入口（已存在则跳过，保证幂等）
	if cfg.AdminUsername != "" && cfg.AdminPassword != "" {
		if _, err := users.ByLogin(ctx, cfg.AdminUsername); errors.Is(err, user.ErrNotFound) {
			hash, _ := auth.HashPassword(cfg.AdminPassword)
			if _, err := users.Create(ctx, user.CreateInput{
				Username:     cfg.AdminUsername,
				PasswordHash: hash,
				Nickname:     "Administrator",
				Role:         model.RoleSuper,
			}); err != nil {
				log.Printf("create admin failed: %v", err)
			} else {
				log.Printf("admin user %q created", cfg.AdminUsername)
			}
		}
	}

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

	v1 := r.Group("/api/v1")

	// ---------- Auth ----------
	authGroup := v1.Group("/auth")
	// 认证接口按客户端 IP 限流（每分钟 30 次），缓解密码爆破与注册刷量
	authGroup.Use(middleware.RateLimit(rdb, "auth", time.Minute, 30, middleware.KeyByIP))
	{
		authGroup.POST("/register", func(c *gin.Context) {
			var req struct {
				Username string `json:"username"`
				Email    string `json:"email"`
				Phone    string `json:"phone"`
				Password string `json:"password"`
				Nickname string `json:"nickname"`
				DeviceID string `json:"device_id"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				httpx.BadRequest(c, "invalid_body", err.Error())
				return
			}
			req.Username = strings.TrimSpace(req.Username)
			req.Email = strings.TrimSpace(req.Email)
			req.Phone = strings.TrimSpace(req.Phone)
			if len(req.Password) < 6 {
				httpx.BadRequest(c, "weak_password", "password must be at least 6 characters")
				return
			}
			if req.Username == "" && req.Email == "" && req.Phone == "" {
				httpx.BadRequest(c, "missing_identity", "username/email/phone required")
				return
			}
			// 预先查重，避免唯一约束冲突时直接暴露数据库错误；
			// 真正的并发兜底仍依赖 users 表的唯一索引
			exists, err := users.Exists(c.Request.Context(), req.Username, req.Email, req.Phone)
			if err != nil {
				httpx.ServerError(c, err.Error())
				return
			}
			if exists {
				httpx.Fail(c, http.StatusConflict, "already_exists", "user already exists")
				return
			}
			hash, err := auth.HashPassword(req.Password)
			if err != nil {
				httpx.ServerError(c, err.Error())
				return
			}
			u, err := users.Create(c.Request.Context(), user.CreateInput{
				Username: req.Username, Email: req.Email, Phone: req.Phone,
				PasswordHash: hash, Nickname: req.Nickname, Role: model.RoleUser,
			})
			if err != nil {
				httpx.ServerError(c, err.Error())
				return
			}
			if req.DeviceID == "" {
				req.DeviceID = "default"
			}
			access, refresh, err := jwtMgr.IssuePair(u.ID, req.DeviceID, u.TokenVersion)
			if err != nil {
				httpx.ServerError(c, err.Error())
				return
			}
			_ = users.TouchDevice(c.Request.Context(), u.ID, req.DeviceID, "web", c.GetHeader("User-Agent"))
			httpx.Created(c, gin.H{"user": u, "access_token": access, "refresh_token": refresh})
		})

		authGroup.POST("/login", func(c *gin.Context) {
			var req struct {
				Login    string `json:"login"`
				Password string `json:"password"`
				DeviceID string `json:"device_id"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				httpx.BadRequest(c, "invalid_body", err.Error())
				return
			}
			u, err := users.ByLogin(c.Request.Context(), req.Login)
			// 账号不存在与密码错误返回同一错误码，避免向攻击者泄露账号是否存在
			if err != nil || !auth.CheckPassword(u.PasswordHash, req.Password) {
				httpx.Unauthorized(c, "invalid_credentials")
				return
			}
			if u.Status != model.UserStatusNormal {
				httpx.Forbidden(c, "user_disabled")
				return
			}
			if req.DeviceID == "" {
				req.DeviceID = "default"
			}
			access, refresh, err := jwtMgr.IssuePair(u.ID, req.DeviceID, u.TokenVersion)
			if err != nil {
				httpx.ServerError(c, err.Error())
				return
			}
			_ = users.TouchDevice(c.Request.Context(), u.ID, req.DeviceID, "web", c.GetHeader("User-Agent"))
			httpx.OK(c, gin.H{"user": u, "access_token": access, "refresh_token": refresh})
		})

		authGroup.POST("/refresh", func(c *gin.Context) {
			var req struct {
				RefreshToken string `json:"refresh_token"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				httpx.BadRequest(c, "invalid_body", err.Error())
				return
			}
			claims, err := jwtMgr.Verify(req.RefreshToken)
			if err != nil || claims.Type != auth.TokenTypeRefresh {
				httpx.Unauthorized(c, "invalid_refresh_token")
				return
			}
			u, err := users.ByID(c.Request.Context(), claims.UserID)
			// 通过比对 token_version 实现令牌吊销：登出/封禁会递增版本号，
			// 使该用户此前签发的所有令牌（含 refresh token）立即失效
			if err != nil || u.Status != model.UserStatusNormal || u.TokenVersion != claims.TokenVer {
				httpx.Unauthorized(c, "token_revoked")
				return
			}
			access, refresh, err := jwtMgr.IssuePair(u.ID, claims.DeviceID, u.TokenVersion)
			if err != nil {
				httpx.ServerError(c, err.Error())
				return
			}
			httpx.OK(c, gin.H{"access_token": access, "refresh_token": refresh})
		})
	}

	// ---------- 需要鉴权的路由 ----------
	authed := v1.Group("")
	authed.Use(middleware.Auth(jwtMgr, users))

	authed.POST("/auth/logout", func(c *gin.Context) {
		u := middleware.CurrentUser(c)
		// 登出通过递增 token_version 吊销该用户全部已签发令牌，实现全端强制下线
		if err := users.BumpTokenVersion(c.Request.Context(), u.ID); err != nil {
			httpx.ServerError(c, err.Error())
			return
		}
		auditLog.Log(audit.Entry{OperatorID: u.ID, Action: "user.logout", TargetType: "user", TargetID: u.ID, IP: c.ClientIP()})
		httpx.OK(c, gin.H{"ok": true})
	})

	authed.GET("/users/me", func(c *gin.Context) {
		httpx.OK(c, middleware.CurrentUser(c))
	})

	authed.GET("/users/:id", func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		u, err := users.ByID(c.Request.Context(), id)
		if err != nil {
			httpx.NotFound(c, "user_not_found")
			return
		}
		httpx.OK(c, u)
	})

	authed.PATCH("/users/me", func(c *gin.Context) {
		// 字段用指针类型，区分"未传该字段"与"显式置空"，支持部分更新资料
		var req struct {
			Nickname  *string `json:"nickname"`
			AvatarURL *string `json:"avatar_url"`
			Signature *string `json:"signature"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpx.BadRequest(c, "invalid_body", err.Error())
			return
		}
		u := middleware.CurrentUser(c)
		updated, err := users.UpdateProfile(c.Request.Context(), u.ID, user.UpdateProfileInput{
			Nickname: req.Nickname, AvatarURL: req.AvatarURL, Signature: req.Signature,
		})
		if err != nil {
			httpx.ServerError(c, err.Error())
			return
		}
		httpx.OK(c, updated)
	})

	// ---------- 会话 ----------
	authed.GET("/conversations", func(c *gin.Context) {
		u := middleware.CurrentUser(c)
		views, err := convs.ListForUser(c.Request.Context(), u.ID)
		if err != nil {
			httpx.ServerError(c, err.Error())
			return
		}
		// 附加单聊对端资料：会话列表只含会话本身信息，这里批量查出
		// 每个单聊的对端用户并回填，便于前端直接渲染会话标题/头像
		convIDs := make([]int64, 0, len(views))
		for _, v := range views {
			convIDs = append(convIDs, v.ID)
		}
		peers, _ := convs.PeerIDs(c.Request.Context(), convIDs, u.ID)
		peerIDs := make([]int64, 0, len(peers))
		for _, pid := range peers {
			peerIDs = append(peerIDs, pid)
		}
		peerMap, _ := users.ListByIDs(c.Request.Context(), peerIDs)
		for _, v := range views {
			if pid, ok := peers[v.ID]; ok {
				v.Peer = peerMap[pid]
			}
		}
		httpx.OK(c, gin.H{"conversations": views})
	})

	authed.POST("/conversations/direct", func(c *gin.Context) {
		var req struct {
			UserID int64 `json:"user_id" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpx.BadRequest(c, "invalid_body", err.Error())
			return
		}
		u := middleware.CurrentUser(c)
		// 创建（或复用）与目标用户的单聊会话；同一对用户重复调用返回同一会话，保证幂等
		convID, err := convs.CreateDirect(c.Request.Context(), u.ID, req.UserID)
		if err != nil {
			httpx.BadRequest(c, "create_direct_failed", err.Error())
			return
		}
		conv, err := convs.Get(c.Request.Context(), convID)
		if err != nil {
			httpx.ServerError(c, err.Error())
			return
		}
		httpx.OK(c, conv)
	})

	authed.POST("/conversations/group", func(c *gin.Context) {
		var req struct {
			Name      string  `json:"name" binding:"required"`
			MemberIDs []int64 `json:"member_ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpx.BadRequest(c, "invalid_body", err.Error())
			return
		}
		u := middleware.CurrentUser(c)
		convID, err := convs.CreateGroup(c.Request.Context(), u.ID, req.Name, req.MemberIDs)
		if err != nil {
			httpx.BadRequest(c, "create_group_failed", err.Error())
			return
		}
		conv, err := convs.Get(c.Request.Context(), convID)
		if err != nil {
			httpx.ServerError(c, err.Error())
			return
		}
		httpx.Created(c, conv)
	})

	authed.POST("/conversations/:id/members", func(c *gin.Context) {
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var req struct {
			UserIDs []int64 `json:"user_ids" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpx.BadRequest(c, "invalid_body", err.Error())
			return
		}
		u := middleware.CurrentUser(c)
		// 拉人入群仅群主/管理员有权限
		member, err := convs.GetMember(c.Request.Context(), convID, u.ID)
		if err != nil || (member.Role != model.MemberRoleOwner && member.Role != model.MemberRoleAdmin) {
			httpx.Forbidden(c, "no_permission")
			return
		}
		if err := convs.AddMembers(c.Request.Context(), convID, req.UserIDs); err != nil {
			httpx.BadRequest(c, "add_members_failed", err.Error())
			return
		}
		httpx.OK(c, gin.H{"ok": true})
	})

	authed.DELETE("/conversations/:id/members/:uid", func(c *gin.Context) {
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		targetID, _ := strconv.ParseInt(c.Param("uid"), 10, 64)
		u := middleware.CurrentUser(c)
		// 踢人出群同样仅群主/管理员有权限
		member, err := convs.GetMember(c.Request.Context(), convID, u.ID)
		if err != nil || (member.Role != model.MemberRoleOwner && member.Role != model.MemberRoleAdmin) {
			httpx.Forbidden(c, "no_permission")
			return
		}
		if err := convs.RemoveMember(c.Request.Context(), convID, targetID); err != nil {
			httpx.ServerError(c, err.Error())
			return
		}
		httpx.OK(c, gin.H{"ok": true})
	})

	authed.GET("/conversations/:id/messages", func(c *gin.Context) {
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		fromSeq, _ := strconv.ParseInt(c.DefaultQuery("from_seq", "0"), 10, 64)
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
		u := middleware.CurrentUser(c)

		// 只有会话成员才能拉取历史消息，防止越权读取他人聊天内容
		if _, err := convs.GetMember(c.Request.Context(), convID, u.ID); err != nil {
			httpx.Forbidden(c, "not_member")
			return
		}
		// 按消息序号增量拉取：返回 seq > from_seq 的 limit 条，供客户端翻页/补拉离线消息
		msgs, err := convs.ListMessages(c.Request.Context(), convID, fromSeq, limit)
		if err != nil {
			httpx.ServerError(c, err.Error())
			return
		}
		httpx.OK(c, gin.H{"messages": msgs})
	})

	authed.POST("/conversations/:id/read", func(c *gin.Context) {
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var req struct {
			LastReadSeq int64 `json:"last_read_seq" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpx.BadRequest(c, "invalid_body", err.Error())
			return
		}
		u := middleware.CurrentUser(c)
		// 上报已读位置（last_read_seq），服务端据此计算未读数并生成已读回执；
		// 该操作幂等，重复上报同一序号无副作用
		if err := convs.MarkRead(c.Request.Context(), convID, u.ID, req.LastReadSeq); err != nil {
			httpx.ServerError(c, err.Error())
			return
		}
		httpx.OK(c, gin.H{"ok": true})
	})

	// ---------- 附件上传 ----------
	authed.POST("/attachments", func(c *gin.Context) {
		fileHeader, err := c.FormFile("file")
		if err != nil {
			httpx.BadRequest(c, "missing_file", err.Error())
			return
		}
		if fileHeader.Size > 50<<20 {
			httpx.BadRequest(c, "file_too_large", "max 50MB")
			return
		}
		u := middleware.CurrentUser(c)
		// 保留原始扩展名，下载/预览时可据此推断文件类型
		ext := ""
		if i := strings.LastIndex(fileHeader.Filename, "."); i >= 0 {
			ext = fileHeader.Filename[i:]
		}
		key := storage.NewObjectKey(u.ID, ext)

		src, err := fileHeader.Open()
		if err != nil {
			httpx.ServerError(c, err.Error())
			return
		}
		defer src.Close()

		if err := objStore.Put(c.Request.Context(), key, src, fileHeader.Size); err != nil {
			httpx.ServerError(c, err.Error())
			return
		}

		var att model.Attachment
		// 先写对象存储再落库元数据；status=2 表示附件已就绪、可被消息引用
		err = db.QueryRow(c.Request.Context(), `
			INSERT INTO attachments (uploader_id, object_key, file_name, mime_type, size_bytes, status)
			VALUES ($1,$2,$3,$4,$5,2)
			RETURNING id, uploader_id, object_key, file_name, mime_type, size_bytes, status, created_at`,
			u.ID, key, fileHeader.Filename, fileHeader.Header.Get("Content-Type"), fileHeader.Size).
			Scan(&att.ID, &att.UploaderID, &att.ObjectKey, &att.FileName, &att.MimeType, &att.SizeBytes, &att.Status, &att.CreatedAt)
		if err != nil {
			httpx.ServerError(c, err.Error())
			return
		}

		httpx.Created(c, gin.H{
			"attachment": att,
			"url":        objStore.URL(key),
		})
	})

	// 附件读取（流式，带 Content-Type 探测）
	r.GET("/api/v1/attachments/file/*key", func(c *gin.Context) {
		key := strings.TrimPrefix(c.Param("key"), "/")
		rc, err := objStore.Get(key)
		if err != nil {
			httpx.NotFound(c, "file_not_found")
			return
		}
		defer rc.Close()

		buf, err := io.ReadAll(io.LimitReader(rc, 50<<20))
		if err != nil {
			httpx.ServerError(c, err.Error())
			return
		}
		http.ServeContent(c.Writer, c.Request, path.Base(key), time.Time{}, bytes.NewReader(buf))
	})

	// 上面占位实现改为直接流式返回
	r.GET("/api/v1/attachments/raw/*key", func(c *gin.Context) {
		key := strings.TrimPrefix(c.Param("key"), "/")
		rc, err := objStore.Get(key)
		if err != nil {
			httpx.NotFound(c, "file_not_found")
			return
		}
		defer rc.Close()
		// 附件内容按对象键不可变，设置一年强缓存避免重复下载
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.Status(http.StatusOK)
		_, _ = c.Writer.Write(mustReadAll(rc))
	})

	// ---------- 举报 ----------
	// 用户举报入口：target_type 区分被举报对象（用户/消息等），
	// 仅入库留痕，后续由管理后台人工处理
	authed.POST("/reports", func(c *gin.Context) {
		var req struct {
			TargetType int16  `json:"target_type" binding:"required"`
			TargetID   int64  `json:"target_id" binding:"required"`
			Reason     string `json:"reason"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpx.BadRequest(c, "invalid_body", err.Error())
			return
		}
		u := middleware.CurrentUser(c)
		_, err := db.Exec(c.Request.Context(), `
			INSERT INTO reports (reporter_id, target_type, target_id, reason)
			VALUES ($1,$2,$3,$4)`, u.ID, req.TargetType, req.TargetID, req.Reason)
		if err != nil {
			httpx.ServerError(c, err.Error())
			return
		}
		httpx.Created(c, gin.H{"ok": true})
	})

	// ---------- 管理后台 ----------
	// 所有 /admin 接口要求管理员及以上角色（RequireRole 中间件校验）
	admin := authed.Group("/admin")
	admin.Use(middleware.RequireRole(model.RoleAdmin))
	{
		admin.GET("/users", func(c *gin.Context) {
			keyword := c.Query("keyword")
			offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
			limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
			if limit <= 0 || limit > 200 {
				limit = 20
			}
			list, total, err := users.ListAdmin(c.Request.Context(), keyword, offset, limit)
			if err != nil {
				httpx.ServerError(c, err.Error())
				return
			}
			httpx.OK(c, gin.H{"users": list, "total": total})
		})

		admin.POST("/users/:id/ban", func(c *gin.Context) {
			targetID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			var req struct {
				Reason   string `json:"reason"`
				ExpireAt string `json:"expire_at"`
			}
			_ = c.ShouldBindJSON(&req)

			operator := middleware.CurrentUser(c)
			if err := users.SetStatus(c.Request.Context(), targetID, model.UserStatusBanned); err != nil {
				httpx.ServerError(c, err.Error())
				return
			}
			// 封禁后立即递增 token_version，强制被ban用户所有在线会话下线
			_ = users.BumpTokenVersion(c.Request.Context(), targetID)

			// expire_at 为空表示永久封禁；格式非法时静默忽略，不影响封禁主流程
			var expireAt *time.Time
			if req.ExpireAt != "" {
				if t, err := time.Parse(time.RFC3339, req.ExpireAt); err == nil {
					expireAt = &t
				}
			}
			_, _ = db.Exec(c.Request.Context(), `
				INSERT INTO bans (user_id, type, reason, expire_at, operator)
				VALUES ($1, 1, $2, $3, $4)`, targetID, req.Reason, expireAt, operator.ID)

			auditLog.Log(audit.Entry{
				OperatorID: operator.ID, Action: "user.ban",
				TargetType: "user", TargetID: targetID,
				Detail: gin.H{"reason": req.Reason}, IP: c.ClientIP(),
			})
			httpx.OK(c, gin.H{"ok": true})
		})

		admin.GET("/audit", func(c *gin.Context) {
			limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
			if limit <= 0 || limit > 500 {
				limit = 50
			}
			rows, err := db.Query(c.Request.Context(), `
				SELECT id, operator_id, action, target_type, target_id, detail, ip, created_at
				FROM audit_logs ORDER BY id DESC LIMIT $1`, limit)
			if err != nil {
				httpx.ServerError(c, err.Error())
				return
			}
			defer rows.Close()
			type row struct {
				ID         int64           `json:"id"`
				OperatorID int64           `json:"operator_id"`
				Action     string          `json:"action"`
				TargetType *string         `json:"target_type"`
				TargetID   *int64          `json:"target_id"`
				Detail     json.RawMessage `json:"detail"`
				IP         *string         `json:"ip"`
				CreatedAt  time.Time       `json:"created_at"`
			}
			var out []row
			for rows.Next() {
				var rr row
				if err := rows.Scan(&rr.ID, &rr.OperatorID, &rr.Action, &rr.TargetType,
					&rr.TargetID, &rr.Detail, &rr.IP, &rr.CreatedAt); err != nil {
					httpx.ServerError(c, err.Error())
					return
				}
				out = append(out, rr)
			}
			httpx.OK(c, gin.H{"logs": out})
		})
	}

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
	// 阻塞等待 SIGINT/SIGTERM，收到信号后在 15 秒内优雅关停，
	// 停止接收新请求并等待在途请求处理完毕，避免部署/重启时请求被硬切断
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

// mustReadAll 将输入流全部读入内存返回，供 /attachments/raw 接口使用；
// 附件上传时已限制单文件最大 50MB，此处整体读入不会导致内存失控。
func mustReadAll(r interface{ Read([]byte) (int, error) }) []byte {
	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 8192)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return buf
		}
	}
}

var _ = fmt.Sprintf

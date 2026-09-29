package main

import (
	"context"
	"errors"
	"log"

	"github.com/example/chat/internal/auth"
	"github.com/example/chat/internal/config"
	"github.com/example/chat/internal/model"
	"github.com/example/chat/internal/user"
)

// ensureAdmin 管理员初始化：配置了管理员账号且库中尚不存在时，自动创建超级管理员，
// 便于全新部署的环境直接获得管理入口（已存在则跳过，保证幂等）。
func ensureAdmin(ctx context.Context, users *user.Repo, cfg *config.Config) {
	if cfg.AdminUsername == "" || cfg.AdminPassword == "" {
		return
	}
	if _, err := users.ByLogin(ctx, cfg.AdminUsername); !errors.Is(err, user.ErrNotFound) {
		return
	}
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

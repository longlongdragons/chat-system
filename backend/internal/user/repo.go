// Package user 提供用户与设备维度的数据访问层（仓储），
// 封装 users / devices 两张表的查询与写入，供认证、资料、管理后台等上层服务调用。
package user

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/example/chat/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound 是用户不存在时的统一哨兵错误，上层可据此返回 404，
// 避免把 pgx.ErrNoRows 这类存储层细节泄露到业务层。
var ErrNotFound = errors.New("user not found")

// Repo 是用户仓储，基于 pgx 连接池访问 PostgreSQL。
type Repo struct {
	db *pgxpool.Pool
}

// NewRepo 创建用户仓储实例。
func NewRepo(db *pgxpool.Pool) *Repo { return &Repo{db: db} }

// userCols 集中维护 users 表的列清单，保证各查询的 SELECT/RETURNING 与 scanUser 的扫描顺序一致。
const userCols = `id, username, email, phone, password_hash, nickname, avatar_url,
                  signature, status, role, token_version, created_at, updated_at`

// scanUser 把一行查询结果扫描为 model.User；
// 将 pgx.ErrNoRows 归一化为 ErrNotFound，让调用方用统一的错误判断"用户不存在"。
func scanUser(row pgx.Row) (*model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.Phone, &u.PasswordHash,
		&u.Nickname, &u.AvatarURL, &u.Signature, &u.Status, &u.Role,
		&u.TokenVersion, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// ByID 按主键查询用户。
func (r *Repo) ByID(ctx context.Context, id int64) (*model.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

// ByLogin 按登录标识查询用户：支持 username / email / phone 任一匹配，
// 对应登录页"一个输入框可填三种账号"的产品形态。
func (r *Repo) ByLogin(ctx context.Context, login string) (*model.User, error) {
	login = strings.TrimSpace(login)
	return scanUser(r.db.QueryRow(ctx,
		`SELECT `+userCols+` FROM users
		 WHERE username=$1 OR email=$1 OR phone=$1 LIMIT 1`, login))
}

// Exists 检查用户名/邮箱/手机号是否已被占用，用于注册前的唯一性预校验。
// 空字符串参数不参与匹配，调用方可以只传关心的字段。
func (r *Repo) Exists(ctx context.Context, username, email, phone string) (bool, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM users
		WHERE ($1 <> '' AND username=$1) OR ($2 <> '' AND email=$2) OR ($3 <> '' AND phone=$3)`,
		username, email, phone).Scan(&n)
	return n > 0, err
}

// CreateInput 是创建用户的入参。
type CreateInput struct {
	Username     string
	Email        string
	Phone        string
	PasswordHash string
	Nickname     string
	Role         int16
}

// Create 写入新用户并返回完整记录。
func (r *Repo) Create(ctx context.Context, in CreateInput) (*model.User, error) {
	// 昵称缺省时按 username → 邮箱前缀 → 兜底"用户"的顺序生成，保证前端展示永远有值
	if in.Nickname == "" {
		switch {
		case in.Username != "":
			in.Nickname = in.Username
		case in.Email != "":
			in.Nickname = strings.Split(in.Email, "@")[0]
		default:
			in.Nickname = "用户"
		}
	}
	if in.Role == 0 {
		in.Role = model.RoleUser
	}
	// NULLIF 把空字符串转为 NULL：username/email/phone 允许只填其一，
	// 存 NULL 才能与列上的唯一约束共存（多个空串会互相冲突）
	return scanUser(r.db.QueryRow(ctx, `
		INSERT INTO users (username, email, phone, password_hash, nickname, role)
		VALUES (NULLIF($1,''), NULLIF($2,''), NULLIF($3,''), $4, $5, $6)
		RETURNING `+userCols,
		in.Username, in.Email, in.Phone, in.PasswordHash, in.Nickname, in.Role))
}

// UpdateProfileInput 是资料更新的入参；字段为指针，nil 表示本次不修改该字段。
type UpdateProfileInput struct {
	Nickname  *string
	AvatarURL *string
	Signature *string
}

// UpdateProfile 部分更新用户资料并返回最新记录。
// COALESCE 保证 nil 字段保留原值，实现"传什么改什么"的 PATCH 语义。
func (r *Repo) UpdateProfile(ctx context.Context, id int64, in UpdateProfileInput) (*model.User, error) {
	return scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET
			nickname   = COALESCE($2, nickname),
			avatar_url = COALESCE($3, avatar_url),
			signature  = COALESCE($4, signature),
			updated_at = now()
		WHERE id=$1
		RETURNING `+userCols, id, in.Nickname, in.AvatarURL, in.Signature))
}

// BumpTokenVersion 将用户的 token_version 加一，使该用户此前签发的所有 JWT 立即失效，
// 用于修改密码、强制下线等"踢出所有登录态"的场景。
func (r *Repo) BumpTokenVersion(ctx context.Context, id int64) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET token_version = token_version + 1, updated_at = now() WHERE id=$1`, id)
	return err
}

// SetStatus 更新用户状态（正常/封禁/删除），供管理后台使用。
func (r *Repo) SetStatus(ctx context.Context, id int64, status int16) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	return err
}

// TouchDevice 登记/刷新用户设备。按 (user_id, device_id) 幂等 upsert：
// 同一设备重复登录只更新最近活跃时间，不产生重复记录；平台与 UA 为空时保留旧值。
func (r *Repo) TouchDevice(ctx context.Context, userID int64, deviceID, platform, ua string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO devices (user_id, device_id, platform, user_agent)
		VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''))
		ON CONFLICT (user_id, device_id)
		DO UPDATE SET last_active_at = now(),
		              platform       = COALESCE(EXCLUDED.platform, devices.platform),
		              user_agent     = COALESCE(EXCLUDED.user_agent, devices.user_agent)`,
		userID, deviceID, platform, ua)
	return err
}

// ListByIDs 批量按 ID 查询用户，返回 ID → 用户的映射，
// 供会话列表、消息列表等场景一次性补齐用户信息，避免逐条查询的 N+1 问题。
func (r *Repo) ListByIDs(ctx context.Context, ids []int64) (map[int64]*model.User, error) {
	out := make(map[int64]*model.User, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `SELECT `+userCols+` FROM users WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out[u.ID] = u
	}
	return out, rows.Err()
}

// ListAdmin 是管理后台的用户分页查询：支持按用户名/邮箱/昵称模糊搜索，返回分页数据与总数。
// 占位符编号（$1、$2…）随是否带 keyword 动态计算，因此用 fmt.Sprintf 拼 LIMIT/OFFSET 的序号，
// 参数值本身仍走参数化传递，不存在注入风险。
func (r *Repo) ListAdmin(ctx context.Context, keyword string, offset, limit int) ([]*model.User, int, error) {
	where := ""
	args := []any{}
	if keyword != "" {
		where = `WHERE username ILIKE $1 OR email ILIKE $1 OR nickname ILIKE $1`
		args = append(args, "%"+keyword+"%")
	}
	var total int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM users `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT ` + userCols + ` FROM users ` + where +
		fmt.Sprintf(` ORDER BY id DESC LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
	args = append(args, limit, offset)
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

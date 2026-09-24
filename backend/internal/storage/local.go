// Package storage 封装聊天附件的对象存储能力。
// 当前实现为本地磁盘存储：上传的文件落盘到指定目录，
// 通过 REST 服务的 /api/v1/attachments/file/{key} 路径对外提供访问。
package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Storage 是附件对象存储的抽象接口，key 为对象键（如 "42/2024/05/xxx.png"）。
// 业务层只依赖该接口，存储后端可替换为本地磁盘或云对象存储。
type Storage interface {
	// Put 将上传内容写入对象存储，key 由 NewObjectKey 生成
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	// Get 按 key 读取附件内容，供下载接口流式返回
	Get(key string) (io.ReadCloser, error)
	// URL 返回附件的可访问地址（指向 REST 服务的下载路由）
	URL(key string) string
	// Delete 删除附件，用于撤回消息或清理文件
	Delete(key string) error
}

// Local 是基于本地文件系统的 Storage 实现
type Local struct {
	root      string // 附件存储根目录
	publicURL string // REST 服务的外部访问地址，用于拼接下载链接
}

// NewLocal 创建本地存储实例，并确保根目录存在
func NewLocal(root, publicURL string) (*Local, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Local{root: root, publicURL: strings.TrimRight(publicURL, "/")}, nil
}

// resolve 把对象键映射为根目录下的磁盘绝对路径。
// key 来自外部（用户上传/下载请求），必须先清洗再校验前缀，
// 防止 "../" 之类的路径穿越攻击逃逸出存储根目录。
func (l *Local) resolve(key string) (string, error) {
	// 先拼前导 "/" 再 Clean，把 key 规整为绝对路径形式，去掉 "."、".."
	clean := filepath.Clean("/" + key)
	full := filepath.Join(l.root, clean)
	if !strings.HasPrefix(full, filepath.Clean(l.root)) {
		return "", fmt.Errorf("invalid key")
	}
	return full, nil
}

// Put 把上传流写入 key 对应的本地文件，目录不存在时自动创建
func (l *Local) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	full, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.Create(full)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// Get 打开 key 对应的本地文件，调用方负责关闭返回的 ReadCloser
func (l *Local) Get(key string) (io.ReadCloser, error) {
	full, err := l.resolve(key)
	if err != nil {
		return nil, err
	}
	return os.Open(full)
}

// URL 拼接附件的完整下载地址，实际内容由 REST 服务的附件路由鉴权后返回
func (l *Local) URL(key string) string {
	return fmt.Sprintf("%s/api/v1/attachments/file/%s", l.publicURL, key)
}

// Delete 删除 key 对应的本地文件
func (l *Local) Delete(key string) error {
	full, err := l.resolve(key)
	if err != nil {
		return err
	}
	return os.Remove(full)
}

// NewObjectKey 生成按日期分桶的对象键，格式为 "{用户ID}/{年}/{月}/{UUID}{扩展名}"。
// 按用户和年月分桶可以避免单一目录下文件过多，UUID 保证同名文件上传不会互相覆盖。
func NewObjectKey(userID int64, ext string) string {
	if ext == "" {
		ext = ".bin"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	now := time.Now()
	return fmt.Sprintf("%d/%04d/%02d/%s%s", userID, now.Year(), now.Month(), uuid.NewString(), ext)
}

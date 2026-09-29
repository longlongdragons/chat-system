package handler

import (
	"bytes"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/example/chat/internal/httpx"
	"github.com/example/chat/internal/middleware"
	"github.com/example/chat/internal/model"
	"github.com/example/chat/internal/storage"
	"github.com/gin-gonic/gin"
)

// uploadAttachment 处理 POST /api/v1/attachments（需登录，Header 鉴权）：
// multipart 上传附件，先写对象存储再落库元数据，返回附件记录与下载地址。
func (d *Deps) uploadAttachment(c *gin.Context) {
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

	if err := d.Store.Put(c.Request.Context(), key, src, fileHeader.Size); err != nil {
		httpx.ServerError(c, err.Error())
		return
	}

	var att model.Attachment
	// 先写对象存储再落库元数据；status=2 表示附件已就绪、可被消息引用
	err = d.DB.QueryRow(c.Request.Context(), `
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
		"url":        d.Store.URL(key),
	})
}

// downloadFile 处理 GET /api/v1/attachments/file/*key：附件流式下载，
// 由 http.ServeContent 按文件名后缀探测 Content-Type 并支持 Range/缓存协商。
func (d *Deps) downloadFile(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	rc, err := d.Store.Get(key)
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
}

// downloadRaw 处理 GET /api/v1/attachments/raw/*key：附件裸内容下载，
// 对象键不可变，设置一年强缓存避免重复下载。
func (d *Deps) downloadRaw(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	rc, err := d.Store.Get(key)
	if err != nil {
		httpx.NotFound(c, "file_not_found")
		return
	}
	defer rc.Close()
	// 附件内容按对象键不可变，设置一年强缓存避免重复下载
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Status(http.StatusOK)
	_, _ = c.Writer.Write(mustReadAll(rc))
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

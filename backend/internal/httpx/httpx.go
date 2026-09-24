// Package httpx 定义 REST API 的统一响应格式。
// 所有接口返回 {code, message, data} 结构，
// 便于前端用一致的方式判断业务成功与失败。
package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Envelope 是统一的 API 响应体：
// Code 为机器可识别的业务码（成功固定为 "ok"），Message 为错误说明，Data 为业务数据。
type Envelope struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// OK 返回 200 成功响应
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{Code: "ok", Data: data})
}

// Created 返回 201 成功响应，用于资源创建类接口（如新建会话）
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Envelope{Code: "ok", Data: data})
}

// Fail 返回失败响应，code 为业务错误码（前端据此区分错误类型），msg 为展示信息
func Fail(c *gin.Context, status int, code, msg string) {
	c.JSON(status, Envelope{Code: code, Message: msg})
}

// BadRequest 返回 400，表示请求参数不合法
func BadRequest(c *gin.Context, code, msg string) { Fail(c, http.StatusBadRequest, code, msg) }

// Unauthorized 返回 401，表示未登录或 token 失效
func Unauthorized(c *gin.Context, code string) {
	Fail(c, http.StatusUnauthorized, code, "unauthorized")
}

// Forbidden 返回 403，表示已登录但无权限（如非管理员访问后台）
func Forbidden(c *gin.Context, code string) { Fail(c, http.StatusForbidden, code, "forbidden") }

// NotFound 返回 404，表示资源不存在
func NotFound(c *gin.Context, code string) { Fail(c, http.StatusNotFound, code, "not found") }

// ServerError 返回 500，表示服务端内部错误
func ServerError(c *gin.Context, msg string) {
	Fail(c, http.StatusInternalServerError, "internal_error", msg)
}

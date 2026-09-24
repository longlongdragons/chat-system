// Package audit 记录管理后台的操作审计日志：调用方通过 Log 异步投递，
// 后台协程攒批写入 PostgreSQL，避免审计写库拖慢管理接口的请求路径。
package audit

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Entry 是一条审计记录：谁（OperatorID）对什么目标（TargetType/TargetID）
// 做了什么（Action），附加上下文明细（Detail）与来源 IP。
type Entry struct {
	OperatorID int64
	Action     string
	TargetType string
	TargetID   int64
	Detail     any
	IP         string
}

// Logger 是异步审计日志器；ch 为有缓冲通道，把请求路径与数据库写入解耦。
type Logger struct {
	db *pgxpool.Pool
	ch chan Entry
}

// NewLogger 创建审计日志器，并启动后台批量写入协程。
func NewLogger(db *pgxpool.Pool) *Logger {
	l := &Logger{db: db, ch: make(chan Entry, 1024)}
	go l.loop()
	return l
}

// Log 非阻塞地把审计记录放入缓冲通道；缓冲满时丢弃并告警，
// 保证审计异常（如数据库故障导致积压）永远不阻塞业务请求。
func (l *Logger) Log(e Entry) {
	select {
	case l.ch <- e:
	default:
		// 缓冲满，丢弃并记录（生产可改为降级到文件/本地磁盘队列）
		log.Printf("[audit] buffer full, dropping action=%s", e.Action)
	}
}

// loop 是后台写入协程：攒满 64 条立即 flush，否则每秒定时 flush，
// 兼顾写入吞吐与日志落地时效。
func (l *Logger) loop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	batch := make([]Entry, 0, 64)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, e := range batch {
			detail, _ := json.Marshal(e.Detail)
			// NULLIF 把空字符串/0 转为 NULL，保持可空列语义；IP 显式转为 inet 类型。
			// 单条插入失败仅记录日志并继续，避免一条脏数据阻塞整批写入。
			if _, err := l.db.Exec(ctx, `
				INSERT INTO audit_logs (operator_id, action, target_type, target_id, detail, ip)
				VALUES ($1,$2,NULLIF($3,''),NULLIF($4,0),$5,NULLIF($6,'')::inet)`,
				e.OperatorID, e.Action, e.TargetType, e.TargetID, detail, e.IP); err != nil {
				log.Printf("[audit] insert failed: %v", err)
			}
		}
		batch = batch[:0]
	}
	for {
		select {
		case e, ok := <-l.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= 64 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Package moderation 提供消息敏感词过滤：从数据库加载敏感词表缓存到内存，
// 在消息发送链路上按每个词配置的策略执行打码替换或整条拦截。
package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/example/chat/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 敏感词命中后的处理策略，持久化在 sensitive_words.strategy 字段。
const (
	StrategyReplace int16 = 1 // 打码：把敏感词替换为等长的 *
	StrategyBlock   int16 = 2 // 拦截：整条消息拒绝投递，返回 ErrBlocked
	StrategyWarn    int16 = 3 // 仅警告（当前引擎按放行处理，预留给前端提示等场景）
)

// ErrBlocked 表示消息命中了「拦截」策略，调用方应拒绝投递并向发送者反馈原因。
var ErrBlocked = errors.New("message blocked by moderation")

// Engine 是敏感词过滤引擎。words 为内存词表（小写敏感词 -> 策略），
// Reload 时整体替换，读写经 mu 保护，从而支持运行期热更新词表。
type Engine struct {
	db *pgxpool.Pool

	mu    sync.RWMutex
	words map[string]int16
}

// NewEngine 创建过滤引擎；词表初始为空，需调用 Reload 加载后才会真正生效。
func NewEngine(db *pgxpool.Pool) *Engine {
	return &Engine{db: db, words: map[string]int16{}}
}

// Reload 从数据库全量加载启用的敏感词并原子替换内存词表：先构建新 map 再
// 加锁整体换入，保证过滤时永远不会读到更新到一半的词表。
// 词统一转小写，使后续匹配大小写不敏感。
func (e *Engine) Reload(ctx context.Context) error {
	rows, err := e.db.Query(ctx, `SELECT word, strategy FROM sensitive_words WHERE enabled = true`)
	if err != nil {
		return err
	}
	defer rows.Close()

	next := make(map[string]int16)
	for rows.Next() {
		var w string
		var s int16
		if err := rows.Scan(&w, &s); err != nil {
			return err
		}
		next[strings.ToLower(w)] = s
	}
	e.mu.Lock()
	e.words = next
	e.mu.Unlock()
	return rows.Err()
}

// Filter 返回过滤后的 content；若命中「拦截」策略返回 ErrBlocked。
func (e *Engine) Filter(msgType int16, content json.RawMessage) (json.RawMessage, error) {
	// 只检查文本/引用消息，图片、文件等非文本类型直接放行
	if msgType != model.MsgTypeText && msgType != model.MsgTypeQuote {
		return content, nil
	}
	var payload struct {
		Text string `json:"text"`
	}
	// content 不符合预期 JSON 结构时放行，避免因格式异常误伤正常消息
	if err := json.Unmarshal(content, &payload); err != nil {
		return content, nil
	}

	e.mu.RLock()
	words := e.words
	e.mu.RUnlock()
	// 词表为空时快速返回，省去无谓的 JSON 重建
	if len(words) == 0 {
		return content, nil
	}

	text := payload.Text
	lower := strings.ToLower(text)
	blocked := false
	for w, strategy := range words {
		idx := strings.Index(lower, w)
		if idx < 0 {
			continue
		}
		switch strategy {
		case StrategyBlock:
			// 不立即返回，继续扫描其余词完成替换统计；最终统一按拦截处理
			blocked = true
		case StrategyReplace:
			// 逐处替换为等长星号；每替换一处后重算小写文本，继续查找该词后续出现位置
			for idx >= 0 {
				text = text[:idx] + strings.Repeat("*", len(w)) + text[idx+len(w):]
				lower = strings.ToLower(text)
				idx = strings.Index(lower, w)
			}
		}
	}
	// 拦截优先级最高：只要命中任一「拦截」词，整条消息拒绝投递
	if blocked {
		return nil, ErrBlocked
	}

	out, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return content, nil
	}
	return out, nil
}

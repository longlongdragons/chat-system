# 迭代路线图

> 基于真实现状制定：单聊/群聊、离线消息、已读回执、幂等去重、多端在线、多网关扩展、敏感词、举报、管理端接口、UI 打磨**均已完成**。

## P0 — 基础夯实（下一个迭代，1-2 周）

目标：让项目"敢改、敢发版"。

- 后端核心链路单元测试：`message.Send`（幂等/禁言/敏感词）、`ListForUser`（本次 500 事故的同类回归）、jwt 签发/验签/吊销、ratelimit 滑动窗口
- GitHub Actions CI：`go build` + `go vet` + `go test` + `vue-tsc` + `vite build`
- gateway `/metrics` 接入 Prometheus（连接数、帧吞吐、推送延迟），grafana 面板
- 管理端最小 Web UI（用户列表/封禁，后端接口已有）

## P1 — 体验提升（2-4 周）

目标：功能深度与细节体验。

- 群成员管理 UI（拉人/踢人/群公告，后端接口已有）
- 用户搜索（按昵称/用户名模糊搜，发起聊天不再手输 ID）
- 消息搜索（`to_tsvector` 全文索引）与 @提醒
- 图片消息粘贴/拖拽上传 + 缩略图；文件消息进度条
- 消息列表虚拟滚动（单会话消息量大时替换 `content-visibility` 方案）

## P2 — 能力扩展（1-2 月）

目标：从"能聊"到"好用"。

- WebRTC 音视频通话（1v1 起步：信令复用现有 WS 帧协议加 `call.*` 帧类型）
- 语音消息（MediaRecorder 采集 → 附件通道）
- Hub bucket 分桶 + 广播通道分片（连接数/实例数达到 `docs/performance.md` 触发条件时）
- 消息漫游策略（多设备全量同步、会话置顶/免打扰服务端化——字段已预留）

## P3 — 生产就绪（持续）

目标：可运维、可托付。

- 部署：docker-compose 生产化（健康检查、资源限额、日志驱动）→ k8s Helm Chart
- 监控告警：Prometheus 告警规则（WS 掉线率、消息延迟、DB 慢查询）、日志集中（Loki）
- 容灾：PG 主从 + 定时 pg_dump 备份、Redis AOF（已有）+ 异地冷备
- 安全加固：附件类型白名单收紧、刷新令牌轮换检测（reuse detection）、管理端二次认证、依赖漏洞扫描（govulncheck）

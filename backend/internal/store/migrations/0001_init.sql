-- 用户表：账号主体。username/email/phone 三选一均可作为登录标识（各自唯一且允许为空）。
-- status 表示账号状态（1 正常，封禁时变更）；role 区分普通用户与管理员；
-- token_version 用于 JWT 吊销——改密码/封号后递增，旧 token 立即失效。
-- status 索引服务于管理后台按状态筛选用户列表。
CREATE TABLE IF NOT EXISTS users (
    id              BIGSERIAL PRIMARY KEY,
    username        VARCHAR(64) UNIQUE,
    email           VARCHAR(128) UNIQUE,
    phone           VARCHAR(32) UNIQUE,
    password_hash   VARCHAR(255) NOT NULL,
    nickname        VARCHAR(64) NOT NULL DEFAULT '',
    avatar_url      VARCHAR(512),
    signature       VARCHAR(255),
    status          SMALLINT NOT NULL DEFAULT 1,
    role            SMALLINT NOT NULL DEFAULT 10,
    token_version   INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);

-- 设备表：记录每个用户登录过的终端，支撑多端登录与离线推送。
-- (user_id, device_id) 唯一，重复登录同一设备只更新活跃时间与 push_token；
-- 用户删除时级联清理设备记录。
CREATE TABLE IF NOT EXISTS devices (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id       VARCHAR(128) NOT NULL,
    platform        VARCHAR(32),
    user_agent      VARCHAR(512),
    last_active_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    push_token      VARCHAR(512),
    UNIQUE(user_id, device_id)
);
CREATE INDEX IF NOT EXISTS idx_devices_user ON devices(user_id);

-- 会话表：单聊与群聊统一抽象为 conversation，type 区分二者。
-- last_msg_id / last_msg_at 冗余最新消息信息，供会话列表直接排序展示，
-- 避免每次都回表查 messages；max_seq 是该会话内消息序号（seq）的分配器，
-- 发消息时原子递增，保证同一会话内消息严格有序。
-- last_msg_at DESC 索引支撑"会话列表按最近活跃排序"这一高频查询。
CREATE TABLE IF NOT EXISTS conversations (
    id              BIGSERIAL PRIMARY KEY,
    type            SMALLINT NOT NULL,
    title           VARCHAR(128),
    owner_id        BIGINT,
    last_msg_id     BIGINT,
    last_msg_at     TIMESTAMPTZ,
    max_seq         BIGINT NOT NULL DEFAULT 0,
    status          SMALLINT NOT NULL DEFAULT 1,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_conv_last_msg ON conversations(last_msg_at DESC NULLS LAST);

-- 单聊对表：仅对 type=单聊的会话存在，记录两个参与者。
-- (user_a, user_b) 唯一约束用于防重——同一对用户无论谁先发起，
-- 都只能对应同一个会话（写入前需先把两个 user id 排序归一化）。
CREATE TABLE IF NOT EXISTS direct_pairs (
    conversation_id BIGINT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    user_a          BIGINT NOT NULL,
    user_b          BIGINT NOT NULL,
    UNIQUE(user_a, user_b)
);

-- 会话成员表：记录用户在每个会话中的成员关系与个性化状态。
-- last_read_seq / unread_count 用于未读数与已读回执；deleted 是"从会话
-- 列表删除"的软删除标记（数据保留，仅对用户隐藏）；muted/mute_until
-- 分别支持永久免打扰和限时免打扰。复合主键保证一人一会议一行；
-- (user_id, deleted) 索引支撑"拉取我的会话列表"这一高频查询。
CREATE TABLE IF NOT EXISTS conversation_members (
    conversation_id BIGINT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role            SMALLINT NOT NULL DEFAULT 0,
    last_read_seq   BIGINT NOT NULL DEFAULT 0,
    unread_count    INT NOT NULL DEFAULT 0,
    pinned          BOOLEAN NOT NULL DEFAULT false,
    muted           BOOLEAN NOT NULL DEFAULT false,
    deleted         BOOLEAN NOT NULL DEFAULT false,
    mute_until      TIMESTAMPTZ,
    joined_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_cm_user ON conversation_members(user_id, deleted);

-- 群聊扩展表：仅对 type=群聊的会话存在，主键直接复用会话 id（1:1 扩展模式）。
-- member_limit 控制群容量，all_muted 实现"全员禁言"（仅管理员可发言）。
CREATE TABLE IF NOT EXISTS groups (
    conversation_id BIGINT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    name            VARCHAR(128) NOT NULL,
    announcement    TEXT,
    member_limit    INT NOT NULL DEFAULT 200,
    all_muted       BOOLEAN NOT NULL DEFAULT false
);

-- 消息表：聊天记录主体，content 用 JSONB 容纳文本/图片/文件等不同类型消息体。
-- 三个索引各有明确分工：
--   uk_msg_conv_seq   —— (conversation_id, seq) 唯一，seq 由会话的 max_seq 分配，
--                        保证会话内消息序号单调连续，历史拉取与增量同步都按 seq 进行；
--   uk_msg_client     —— (conversation_id, client_msg_id) 唯一，客户端发送时生成
--                        幂等键，断线重发/重复提交时去重，防止同一条消息入库两次；
--   idx_msg_conv_time —— 按会话倒序翻页拉历史消息（游标分页）。
CREATE TABLE IF NOT EXISTS messages (
    id              BIGSERIAL PRIMARY KEY,
    conversation_id BIGINT NOT NULL,
    seq             BIGINT NOT NULL,
    sender_id       BIGINT NOT NULL,
    client_msg_id   VARCHAR(64) NOT NULL,
    type            SMALLINT NOT NULL,
    content         JSONB NOT NULL,
    quote_msg_id    BIGINT,
    status          SMALLINT NOT NULL DEFAULT 1,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_msg_conv_seq   ON messages(conversation_id, seq);
CREATE UNIQUE INDEX IF NOT EXISTS uk_msg_client     ON messages(conversation_id, client_msg_id);
CREATE INDEX        IF NOT EXISTS idx_msg_conv_time ON messages(conversation_id, id DESC);

-- 消息已读回执表：记录每个用户在每个会话中已读到的最大 seq，
-- 用于向对方展示"已读"进度（如单聊里对方读到哪条）。与
-- conversation_members.last_read_seq 互为冗余：本表面向回执展示的
-- 读写模型，成员表字段服务于未读数计算。
CREATE TABLE IF NOT EXISTS message_receipts (
    conversation_id BIGINT NOT NULL,
    user_id         BIGINT NOT NULL,
    last_read_seq   BIGINT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, user_id)
);

-- 离线消息表：gateway 投递消息时若目标用户不在线，则在此落一份待投递记录；
-- 用户上线后按 (user_id, conversation_id, seq) 索引按序拉取补发，拉完删除。
-- 主键 (user_id, message_id) 保证同一消息对同一用户只补投一次。
CREATE TABLE IF NOT EXISTS offline_messages (
    user_id         BIGINT NOT NULL,
    conversation_id BIGINT NOT NULL,
    message_id      BIGINT NOT NULL,
    seq             BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, message_id)
);
CREATE INDEX IF NOT EXISTS idx_offline_user_conv ON offline_messages(user_id, conversation_id, seq);

-- 附件表：上传文件的元数据。object_key 是文件在存储（本地/对象存储）中的路径；
-- sha256 用于完整性校验与秒传去重；status 默认 2 表示"已上传待关联"，
-- 消息发送后附件才与消息绑定，未被引用的临时附件可由后台任务定期清理。
CREATE TABLE IF NOT EXISTS attachments (
    id           BIGSERIAL PRIMARY KEY,
    uploader_id  BIGINT NOT NULL,
    object_key   VARCHAR(512) NOT NULL,
    file_name    VARCHAR(255),
    mime_type    VARCHAR(128),
    size_bytes   BIGINT,
    sha256       VARCHAR(64),
    status       SMALLINT NOT NULL DEFAULT 2,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 敏感词表：内容风控的词库。strategy 区分处理方式（如 1=替换脱敏、2=拦截整条消息），
-- enabled 支持临时停用某个词而不删除记录；word 唯一防重复录入。
CREATE TABLE IF NOT EXISTS sensitive_words (
    id         BIGSERIAL PRIMARY KEY,
    word       VARCHAR(128) NOT NULL UNIQUE,
    strategy   SMALLINT NOT NULL DEFAULT 1,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 举报表：用户对消息/用户/群组等对象的举报工单。target_type 指明被举报对象类型，
-- target_id 指向具体对象；status 跟踪处理进度（0=待处理），handler_id/handled_at
-- 记录处理人与处理时间，供管理后台流转。
CREATE TABLE IF NOT EXISTS reports (
    id           BIGSERIAL PRIMARY KEY,
    reporter_id  BIGINT NOT NULL,
    target_type  SMALLINT NOT NULL,
    target_id    BIGINT NOT NULL,
    reason       VARCHAR(255),
    status       SMALLINT NOT NULL DEFAULT 0,
    handler_id   BIGINT,
    handled_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 封禁表：管理员对用户实施的处罚记录。user_id 为全局封禁（不可登录/发言），
-- conv_id 配合 user_id 表示仅在某会话内禁言；expire_at 为空表示永久，
-- 否则到期自动失效；operator 记录操作人以便审计追责。
CREATE TABLE IF NOT EXISTS bans (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT,
    conv_id    BIGINT,
    type       SMALLINT NOT NULL,
    reason     VARCHAR(255),
    expire_at  TIMESTAMPTZ,
    operator   BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 审计日志表：管理后台所有敏感操作（封号、删消息、改配置等）的留痕。
-- detail 用 JSONB 存操作前后差异等结构化细节，ip 记录来源地址。
-- 两个索引分别支撑"按操作人查操作历史"和"按操作类型查"两类后台检索。
CREATE TABLE IF NOT EXISTS audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    operator_id BIGINT NOT NULL,
    action      VARCHAR(64) NOT NULL,
    target_type VARCHAR(32),
    target_id   BIGINT,
    detail      JSONB,
    ip          INET,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_operator ON audit_logs(operator_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_action   ON audit_logs(action, created_at DESC);

-- 示例敏感词
INSERT INTO sensitive_words (word, strategy) VALUES
    ('fuck', 1), ('shit', 1), ('赌博', 2), ('诈骗', 2)
ON CONFLICT (word) DO NOTHING;
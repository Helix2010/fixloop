-- =============================================================================
-- FixLoop 初始数据库 Schema
-- =============================================================================
-- 设计原则：
--   1. 软删除：users / projects 使用 deleted_at，避免级联删除破坏历史记录
--   2. 敏感字段（PAT、API Key 等）在应用层 AES-GCM 加密后以 hex 字符串存入 JSON
--   3. agent_runs 按年月 RANGE 分区，避免日志表无限膨胀；输出单独存于 agent_run_outputs
-- =============================================================================

-- -----------------------------------------------------------------------------
-- users：FixLoop 用户，通过 GitHub OAuth 注册
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '用户主键',
    github_id    BIGINT NOT NULL                   COMMENT 'GitHub 用户 ID（来自 OAuth）',
    github_login VARCHAR(64) NOT NULL              COMMENT 'GitHub 用户名，如 octocat',
    tg_chat_id   BIGINT                            COMMENT 'Telegram 私聊 Chat ID；绑定后用于消息推送，NULL 表示未绑定',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '注册时间',
    deleted_at   DATETIME                          COMMENT '软删除时间，NULL 表示正常',
    UNIQUE KEY uq_github_id (github_id)            -- 防止同一 GitHub 账号重复注册
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- projects：用户创建的被监控项目
-- config 列存放完整的项目配置（GitHub 仓库、测试环境、AI 引擎等），
-- 敏感字段在应用层加密，避免多列扩展带来的 schema 变更成本。
-- config_version 随每次 PATCH 递增，agent_runs 记录触发时的版本，
-- 方便排查"用旧配置跑了一次"的问题。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS projects (
    id             BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '项目主键',
    user_id        BIGINT NOT NULL                   COMMENT '所属用户',
    name           VARCHAR(64) NOT NULL              COMMENT '项目名称，同一用户下唯一（含软删除隔离）',
    config         JSON NOT NULL                     COMMENT '项目配置 JSON：GitHub PAT、测试环境、AI 引擎等；敏感字段 AES-GCM 加密',
    config_version INT NOT NULL DEFAULT 1            COMMENT '配置版本号，每次 PATCH 递增；agent_runs 用此字段关联触发时的配置快照',
    status         ENUM('active','paused','error') NOT NULL DEFAULT 'active' COMMENT '项目状态：active=运行中 / paused=已暂停 / error=异常',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    deleted_at     DATETIME                          COMMENT '软删除时间，NULL 表示正常',
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    -- deleted_at 纳入唯一键：允许同名项目被删除后重建，同时禁止活跃项目重名
    UNIQUE KEY uq_user_project_name (user_id, name, deleted_at),
    INDEX idx_user_status (user_id, status)          -- 按用户 + 状态过滤项目列表
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- issues：从 GitHub 同步或由 Explore Agent 自动创建的 Bug/Issue 记录
-- title_hash 对标题规范化（去标点、转小写、提取汉字）后取 SHA-1，
-- 用于跨 Agent 去重，防止同一 Issue 被重复处理。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS issues (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT 'Issue 主键',
    project_id      BIGINT NOT NULL                  COMMENT '所属项目',
    github_number   INT NOT NULL                     COMMENT 'GitHub Issue 编号（#N）',
    title           VARCHAR(512) NOT NULL             COMMENT 'Issue 标题',
    title_hash      CHAR(40) NOT NULL                COMMENT '标题规范化后的 SHA-1 哈希（去除标点/空格/转小写），用于跨 Agent 去重',
    priority        TINYINT NOT NULL DEFAULT 2        COMMENT '优先级：1=P1 紧急 / 2=P2 一般 / 3=P3 低',
    status          ENUM('open','fixing','closed','needs-human') NOT NULL DEFAULT 'open' COMMENT '状态：open=待修复 / fixing=修复中 / closed=已关闭 / needs-human=需人工介入',
    fix_attempts    INT NOT NULL DEFAULT 0            COMMENT '已尝试修复次数，超过阈值后自动转 needs-human',
    accept_failures INT NOT NULL DEFAULT 0            COMMENT '自动验收失败次数（PR 合并后测试仍未通过）',
    fixing_since    DATETIME                          COMMENT '最近一次进入 fixing 状态的时间，用于超时检测',
    closed_at       DATETIME                          COMMENT 'Issue 关闭时间',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '本地记录创建时间',
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    UNIQUE KEY uq_project_issue (project_id, github_number),  -- 同项目内 Issue 编号唯一
    UNIQUE KEY uq_project_title (project_id, title_hash),     -- 防止内容相同的 Issue 重复入库
    -- Fix Agent 轮询时按 status + priority + fix_attempts 排序，此索引覆盖该查询
    INDEX idx_project_status_priority (project_id, status, priority, fix_attempts)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- prs：Fix Agent 提交的修复 Pull Request
-- issue_id 允许为 NULL，兼容未来可能出现的非 Issue 驱动的自动 PR。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS prs (
    id            BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT 'PR 主键',
    project_id    BIGINT NOT NULL                  COMMENT '所属项目',
    issue_id      BIGINT                           COMMENT '关联 Issue；NULL 表示非 Issue 驱动的手动 PR',
    github_number INT NOT NULL                     COMMENT 'GitHub PR 编号',
    branch        VARCHAR(128) NOT NULL            COMMENT 'PR 分支名，格式通常为 fixloop/issue-<N>',
    status        ENUM('open','merged','closed') NOT NULL DEFAULT 'open' COMMENT '状态：open=已开启 / merged=已合并 / closed=已关闭未合并',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '本地记录创建时间',
    merged_at     DATETIME                         COMMENT 'PR 合并时间，NULL 表示未合并',
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE SET NULL,  -- Issue 删除后 PR 记录保留
    UNIQUE KEY uq_project_pr (project_id, github_number),
    INDEX idx_project_status (project_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- pr_reviews：PR 的代码评审记录
-- review_round 支持多轮迭代：Fix Agent 根据 changes_requested 修改后重新提交，
-- round 递增，便于追踪修复历史。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pr_reviews (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '评审记录主键',
    pr_id        BIGINT NOT NULL                  COMMENT '所属 PR',
    reviewer     VARCHAR(64) NOT NULL             COMMENT '评审者 GitHub 登录名',
    state        ENUM('pending','commented','approved','changes_requested') NOT NULL DEFAULT 'pending' COMMENT '评审状态',
    review_round INT NOT NULL DEFAULT 1           COMMENT '第几轮评审（从 1 开始），支持多轮迭代修复',
    reviewed_at  DATETIME                         COMMENT '评审完成时间，NULL 表示尚未评审',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '记录创建时间',
    FOREIGN KEY (pr_id) REFERENCES prs(id) ON DELETE CASCADE,
    INDEX idx_pr_round (pr_id, review_round)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- backlog：Explore Agent 的测试场景队列
-- 由三种来源填充：
--   seed        — 系统内置的基础检查（控制台报错、核心页面加载等）
--   plan        — Plan Agent 根据项目信息 AI 生成
--   auto_expand — 未来扩展，基于已有场景自动衍生
-- test_steps 存储 Playwright 操作步骤 JSON；seed 场景无需 steps，
-- 由 Explore Agent 内置逻辑处理。
-- title_hash 规范化时保留汉字，避免中文场景标题被错误去重。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS backlog (
    id               BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT 'backlog 条目主键',
    project_id       BIGINT NOT NULL                  COMMENT '所属项目',
    related_issue_id BIGINT                           COMMENT '关联 Issue；NULL 表示独立测试场景',
    title            VARCHAR(512) NOT NULL             COMMENT '测试场景标题',
    title_hash       CHAR(40) NOT NULL                COMMENT '标题规范化哈希（含中文字符），用于去重',
    description      TEXT                             COMMENT '场景描述，供 Explore Agent 理解测试意图',
    test_steps       JSON                             COMMENT '操作步骤 JSON 数组，由 Plan Agent 生成；seed 场景可为 NULL',
    scenario_type    ENUM('ui','api') NOT NULL DEFAULT 'ui' COMMENT '场景类型：ui=界面自动化 / api=接口测试',
    priority         TINYINT NOT NULL DEFAULT 2        COMMENT '优先级：1=高 / 2=中 / 3=低',
    status           ENUM('pending','tested','failed','skipped','ignored') NOT NULL DEFAULT 'pending' COMMENT '执行状态：pending=待测 / tested=通过 / failed=失败 / skipped=跳过 / ignored=已忽略',
    source           ENUM('plan','auto_expand','seed') NOT NULL DEFAULT 'plan' COMMENT '来源：plan=Plan Agent 生成 / auto_expand=自动扩展 / seed=系统内置种子场景',
    last_tested_at   DATETIME                         COMMENT '最后一次执行测试的时间，NULL 表示从未测试',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (related_issue_id) REFERENCES issues(id) ON DELETE SET NULL,
    UNIQUE KEY uq_project_scenario (project_id, title_hash),
    -- Explore Agent 轮询时按 priority ASC, last_tested_at ASC（NULL 优先）排序
    INDEX idx_project_status_priority (project_id, status, priority, last_tested_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- agent_runs：所有 Agent 的执行记录
-- 按年月 RANGE 分区（YEAR*100+MONTH），历史分区可直接 DROP PARTITION 清理，
-- 比 DELETE 快且不产生碎片。
-- 输出日志单独存于 agent_run_outputs，避免 MEDIUMTEXT 导致主表行膨胀，
-- 影响状态查询性能。
-- PRIMARY KEY 包含 started_at 是 MySQL 分区表的强制要求（分区键必须在主键中）。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agent_runs (
    id           BIGINT AUTO_INCREMENT          COMMENT '运行记录主键（与 started_at 组成分区联合主键）',
    project_id   BIGINT                         COMMENT '所属项目，NULL 表示全局任务',
    agent_type   ENUM('explore','fix','master','plan') NOT NULL COMMENT 'Agent 类型',
    status       ENUM('running','success','failed','skipped','abandoned') NOT NULL DEFAULT 'running' COMMENT '状态：running=执行中 / success=成功 / failed=失败 / skipped=条件不满足跳过 / abandoned=进程重启时遗弃',
    config_version INT                          COMMENT '触发本次运行时项目 config 的版本号',
    started_at   DATETIME NOT NULL              COMMENT '开始时间（同时作为分区键，按年月 RANGE 分区）',
    finished_at  DATETIME                       COMMENT '结束时间，NULL 表示仍在运行',
    PRIMARY KEY (id, started_at)                -- 分区表要求分区键在主键中
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
PARTITION BY RANGE (YEAR(started_at) * 100 + MONTH(started_at)) (
    PARTITION p202604 VALUES LESS THAN (202605),
    PARTITION p202605 VALUES LESS THAN (202606),
    PARTITION p202606 VALUES LESS THAN (202607),
    PARTITION p_future VALUES LESS THAN MAXVALUE  -- 兜底分区，定期由 master agent 拆分
);

-- -----------------------------------------------------------------------------
-- agent_run_outputs：Agent 运行日志（与 agent_runs 1:1）
-- 单独建表的原因：MEDIUMTEXT 最大 16MB，若放在主表会导致全表扫描时读取大量无用数据。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agent_run_outputs (
    run_id BIGINT NOT NULL  COMMENT '对应 agent_runs.id',
    output MEDIUMTEXT       COMMENT 'Agent 运行的完整日志输出；独立存储避免主表行膨胀',
    PRIMARY KEY (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- notifications：系统通知，支持站内消息和 Telegram 推送
-- tg_sent 标记已推送，防止重复发送（推送 worker 轮询 tg_sent=FALSE 的记录）。
-- project_id 允许为 NULL，兼容与具体项目无关的系统级通知。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS notifications (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '通知主键',
    user_id    BIGINT NOT NULL                  COMMENT '接收通知的用户',
    project_id BIGINT                           COMMENT '关联项目，NULL 表示系统级通知',
    type       VARCHAR(64) NOT NULL             COMMENT '通知类型，如 fix_failed / pr_merged / needs_human',
    content    TEXT NOT NULL                    COMMENT '通知正文（Markdown 格式）',
    read_at    DATETIME                         COMMENT '用户阅读时间，NULL 表示未读',
    tg_sent    BOOLEAN NOT NULL DEFAULT FALSE   COMMENT '是否已通过 Telegram Bot 推送',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '通知创建时间',
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    INDEX idx_user_read (user_id, read_at)      -- 按用户查未读通知
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- system_config：全局 KV 配置表
-- 用于存储运行时可变的系统参数，避免频繁改配置文件重启服务。
-- 当前使用的 key：
--   tg_last_update_id      — Telegram Bot 轮询的最后处理 update_id，断点续传
--   tg_bot_token           — 管理后台配置的 Bot Token（AES-GCM 加密），优先级高于 config.yaml
--   partition_last_cleaned — 分区清理任务的最后执行时间
--   feature_flags          — JSON 格式的功能开关
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS system_config (
    key_name   VARCHAR(64) PRIMARY KEY   COMMENT '配置项键名，全局唯一',
    value      TEXT NOT NULL             COMMENT '配置项值（纯字符串或 JSON）',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '最后更新时间'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO system_config (key_name, value) VALUES
    ('tg_last_update_id', '0'),       -- Telegram 轮询断点
    ('partition_last_cleaned', ''),   -- 分区清理记录
    ('feature_flags', '{}');          -- 功能开关（JSON）

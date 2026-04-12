-- =============================================================================
-- 为所有已存在表的字段补充 COMMENT
-- =============================================================================
-- 背景：000001 建表时未添加字段注释，此 migration 补充说明。
-- 注意：MODIFY COLUMN 仅变更元数据，MySQL 8.0+ 对 comment-only 变更
--       使用 INSTANT 算法，不重建表、不锁行，生产环境可安全执行。
-- =============================================================================

ALTER TABLE users
    MODIFY COLUMN id           BIGINT AUTO_INCREMENT   COMMENT '用户主键',
    MODIFY COLUMN github_id    BIGINT NOT NULL         COMMENT 'GitHub 用户 ID（来自 OAuth）',
    MODIFY COLUMN github_login VARCHAR(64) NOT NULL    COMMENT 'GitHub 用户名，如 octocat',
    MODIFY COLUMN tg_chat_id   BIGINT                  COMMENT 'Telegram 私聊 Chat ID；绑定后用于消息推送，NULL 表示未绑定',
    MODIFY COLUMN created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '注册时间',
    MODIFY COLUMN deleted_at   DATETIME                COMMENT '软删除时间，NULL 表示正常';

ALTER TABLE projects
    MODIFY COLUMN id             BIGINT AUTO_INCREMENT  COMMENT '项目主键',
    MODIFY COLUMN user_id        BIGINT NOT NULL        COMMENT '所属用户',
    MODIFY COLUMN name           VARCHAR(64) NOT NULL   COMMENT '项目名称，同一用户下唯一（含软删除隔离）',
    MODIFY COLUMN config         JSON NOT NULL          COMMENT '项目配置 JSON：GitHub PAT、测试环境、AI 引擎等；敏感字段 AES-GCM 加密',
    MODIFY COLUMN config_version INT NOT NULL DEFAULT 1 COMMENT '配置版本号，每次 PATCH 递增；agent_runs 用此字段关联触发时的配置快照',
    MODIFY COLUMN status         ENUM('active','paused','error') NOT NULL DEFAULT 'active' COMMENT '项目状态：active=运行中 / paused=已暂停 / error=异常',
    MODIFY COLUMN created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    MODIFY COLUMN deleted_at     DATETIME               COMMENT '软删除时间，NULL 表示正常';

ALTER TABLE issues
    MODIFY COLUMN id              BIGINT AUTO_INCREMENT  COMMENT 'Issue 主键',
    MODIFY COLUMN project_id      BIGINT NOT NULL        COMMENT '所属项目',
    MODIFY COLUMN github_number   INT NOT NULL           COMMENT 'GitHub Issue 编号（#N）',
    MODIFY COLUMN title           VARCHAR(512) NOT NULL  COMMENT 'Issue 标题',
    MODIFY COLUMN title_hash      CHAR(40) NOT NULL      COMMENT '标题规范化后的 SHA-1 哈希（去除标点/空格/转小写），用于跨 Agent 去重',
    MODIFY COLUMN priority        TINYINT NOT NULL DEFAULT 2 COMMENT '优先级：1=P1 紧急 / 2=P2 一般 / 3=P3 低',
    MODIFY COLUMN status          ENUM('open','fixing','closed','needs-human') NOT NULL DEFAULT 'open' COMMENT '状态：open=待修复 / fixing=修复中 / closed=已关闭 / needs-human=需人工介入',
    MODIFY COLUMN fix_attempts    INT NOT NULL DEFAULT 0 COMMENT '已尝试修复次数，超过阈值后自动转 needs-human',
    MODIFY COLUMN accept_failures INT NOT NULL DEFAULT 0 COMMENT '自动验收失败次数（PR 合并后测试仍未通过）',
    MODIFY COLUMN fixing_since    DATETIME               COMMENT '最近一次进入 fixing 状态的时间，用于超时检测',
    MODIFY COLUMN closed_at       DATETIME               COMMENT 'Issue 关闭时间',
    MODIFY COLUMN created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '本地记录创建时间';

ALTER TABLE prs
    MODIFY COLUMN id            BIGINT AUTO_INCREMENT   COMMENT 'PR 主键',
    MODIFY COLUMN project_id    BIGINT NOT NULL         COMMENT '所属项目',
    MODIFY COLUMN issue_id      BIGINT                  COMMENT '关联 Issue；NULL 表示非 Issue 驱动的手动 PR',
    MODIFY COLUMN github_number INT NOT NULL            COMMENT 'GitHub PR 编号',
    MODIFY COLUMN branch        VARCHAR(128) NOT NULL   COMMENT 'PR 分支名，格式通常为 fixloop/issue-<N>',
    MODIFY COLUMN status        ENUM('open','merged','closed') NOT NULL DEFAULT 'open' COMMENT '状态：open=已开启 / merged=已合并 / closed=已关闭未合并',
    MODIFY COLUMN created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '本地记录创建时间',
    MODIFY COLUMN merged_at     DATETIME                COMMENT 'PR 合并时间，NULL 表示未合并';

ALTER TABLE pr_reviews
    MODIFY COLUMN id           BIGINT AUTO_INCREMENT   COMMENT '评审记录主键',
    MODIFY COLUMN pr_id        BIGINT NOT NULL         COMMENT '所属 PR',
    MODIFY COLUMN reviewer     VARCHAR(64) NOT NULL    COMMENT '评审者 GitHub 登录名',
    MODIFY COLUMN state        ENUM('pending','commented','approved','changes_requested') NOT NULL DEFAULT 'pending' COMMENT '评审状态',
    MODIFY COLUMN review_round INT NOT NULL DEFAULT 1  COMMENT '第几轮评审（从 1 开始），支持多轮迭代修复',
    MODIFY COLUMN reviewed_at  DATETIME                COMMENT '评审完成时间，NULL 表示尚未评审',
    MODIFY COLUMN created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '记录创建时间';

ALTER TABLE backlog
    MODIFY COLUMN id               BIGINT AUTO_INCREMENT  COMMENT 'backlog 条目主键',
    MODIFY COLUMN project_id       BIGINT NOT NULL        COMMENT '所属项目',
    MODIFY COLUMN related_issue_id BIGINT                 COMMENT '关联 Issue；NULL 表示独立测试场景',
    MODIFY COLUMN title            VARCHAR(512) NOT NULL  COMMENT '测试场景标题',
    MODIFY COLUMN title_hash       CHAR(40) NOT NULL      COMMENT '标题规范化哈希（含中文字符），用于去重',
    MODIFY COLUMN description      TEXT                   COMMENT '场景描述，供 Explore Agent 理解测试意图',
    MODIFY COLUMN test_steps       JSON                   COMMENT '操作步骤 JSON 数组，由 Plan Agent 生成；seed 场景可为 NULL',
    MODIFY COLUMN scenario_type    ENUM('ui','api') NOT NULL DEFAULT 'ui' COMMENT '场景类型：ui=界面自动化 / api=接口测试',
    MODIFY COLUMN priority         TINYINT NOT NULL DEFAULT 2 COMMENT '优先级：1=高 / 2=中 / 3=低',
    MODIFY COLUMN status           ENUM('pending','tested','failed','skipped','ignored') NOT NULL DEFAULT 'pending' COMMENT '执行状态：pending=待测 / tested=通过 / failed=失败 / skipped=跳过 / ignored=已忽略',
    MODIFY COLUMN source           ENUM('plan','auto_expand','seed') NOT NULL DEFAULT 'plan' COMMENT '来源：plan=Plan Agent 生成 / auto_expand=自动扩展 / seed=系统内置种子场景',
    MODIFY COLUMN last_tested_at   DATETIME               COMMENT '最后一次执行测试的时间，NULL 表示从未测试',
    MODIFY COLUMN created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间';

ALTER TABLE agent_runs
    MODIFY COLUMN id             BIGINT AUTO_INCREMENT  COMMENT '运行记录主键（与 started_at 组成分区联合主键）',
    MODIFY COLUMN project_id     BIGINT                 COMMENT '所属项目，NULL 表示全局任务',
    MODIFY COLUMN agent_type     ENUM('explore','fix','master','plan') NOT NULL COMMENT 'Agent 类型',
    MODIFY COLUMN status         ENUM('running','success','failed','skipped','abandoned') NOT NULL DEFAULT 'running' COMMENT '状态：running=执行中 / success=成功 / failed=失败 / skipped=条件不满足跳过 / abandoned=进程重启时遗弃',
    MODIFY COLUMN config_version INT                    COMMENT '触发本次运行时项目 config 的版本号',
    MODIFY COLUMN started_at     DATETIME NOT NULL      COMMENT '开始时间（同时作为分区键，按年月 RANGE 分区）',
    MODIFY COLUMN finished_at    DATETIME               COMMENT '结束时间，NULL 表示仍在运行';

ALTER TABLE agent_run_outputs
    MODIFY COLUMN run_id BIGINT NOT NULL COMMENT '对应 agent_runs.id',
    MODIFY COLUMN output MEDIUMTEXT      COMMENT 'Agent 运行的完整日志输出；独立存储避免主表行膨胀';

ALTER TABLE notifications
    MODIFY COLUMN id         BIGINT AUTO_INCREMENT  COMMENT '通知主键',
    MODIFY COLUMN user_id    BIGINT NOT NULL        COMMENT '接收通知的用户',
    MODIFY COLUMN project_id BIGINT                 COMMENT '关联项目，NULL 表示系统级通知',
    MODIFY COLUMN type       VARCHAR(64) NOT NULL   COMMENT '通知类型，如 fix_failed / pr_merged / needs_human',
    MODIFY COLUMN content    TEXT NOT NULL          COMMENT '通知正文（Markdown 格式）',
    MODIFY COLUMN read_at    DATETIME               COMMENT '用户阅读时间，NULL 表示未读',
    MODIFY COLUMN tg_sent    BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否已通过 Telegram Bot 推送',
    MODIFY COLUMN created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '通知创建时间';

ALTER TABLE system_config
    MODIFY COLUMN key_name   VARCHAR(64) NOT NULL   COMMENT '配置项键名，全局唯一',
    MODIFY COLUMN value      TEXT NOT NULL          COMMENT '配置项值（纯字符串或 JSON）',
    MODIFY COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '最后更新时间';

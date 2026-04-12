-- 移除所有字段注释，回退到 000001 状态。

ALTER TABLE users
    MODIFY COLUMN id           BIGINT AUTO_INCREMENT,
    MODIFY COLUMN github_id    BIGINT NOT NULL,
    MODIFY COLUMN github_login VARCHAR(64) NOT NULL,
    MODIFY COLUMN tg_chat_id   BIGINT,
    MODIFY COLUMN created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    MODIFY COLUMN deleted_at   DATETIME;

ALTER TABLE projects
    MODIFY COLUMN id             BIGINT AUTO_INCREMENT,
    MODIFY COLUMN user_id        BIGINT NOT NULL,
    MODIFY COLUMN name           VARCHAR(64) NOT NULL,
    MODIFY COLUMN config         JSON NOT NULL,
    MODIFY COLUMN config_version INT NOT NULL DEFAULT 1,
    MODIFY COLUMN status         ENUM('active','paused','error') NOT NULL DEFAULT 'active',
    MODIFY COLUMN created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    MODIFY COLUMN deleted_at     DATETIME;

ALTER TABLE issues
    MODIFY COLUMN id              BIGINT AUTO_INCREMENT,
    MODIFY COLUMN project_id      BIGINT NOT NULL,
    MODIFY COLUMN github_number   INT NOT NULL,
    MODIFY COLUMN title           VARCHAR(512) NOT NULL,
    MODIFY COLUMN title_hash      CHAR(40) NOT NULL,
    MODIFY COLUMN priority        TINYINT NOT NULL DEFAULT 2,
    MODIFY COLUMN status          ENUM('open','fixing','closed','needs-human') NOT NULL DEFAULT 'open',
    MODIFY COLUMN fix_attempts    INT NOT NULL DEFAULT 0,
    MODIFY COLUMN accept_failures INT NOT NULL DEFAULT 0,
    MODIFY COLUMN fixing_since    DATETIME,
    MODIFY COLUMN closed_at       DATETIME,
    MODIFY COLUMN created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;

ALTER TABLE prs
    MODIFY COLUMN id            BIGINT AUTO_INCREMENT,
    MODIFY COLUMN project_id    BIGINT NOT NULL,
    MODIFY COLUMN issue_id      BIGINT,
    MODIFY COLUMN github_number INT NOT NULL,
    MODIFY COLUMN branch        VARCHAR(128) NOT NULL,
    MODIFY COLUMN status        ENUM('open','merged','closed') NOT NULL DEFAULT 'open',
    MODIFY COLUMN created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    MODIFY COLUMN merged_at     DATETIME;

ALTER TABLE pr_reviews
    MODIFY COLUMN id           BIGINT AUTO_INCREMENT,
    MODIFY COLUMN pr_id        BIGINT NOT NULL,
    MODIFY COLUMN reviewer     VARCHAR(64) NOT NULL,
    MODIFY COLUMN state        ENUM('pending','commented','approved','changes_requested') NOT NULL DEFAULT 'pending',
    MODIFY COLUMN review_round INT NOT NULL DEFAULT 1,
    MODIFY COLUMN reviewed_at  DATETIME,
    MODIFY COLUMN created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;

ALTER TABLE backlog
    MODIFY COLUMN id               BIGINT AUTO_INCREMENT,
    MODIFY COLUMN project_id       BIGINT NOT NULL,
    MODIFY COLUMN related_issue_id BIGINT,
    MODIFY COLUMN title            VARCHAR(512) NOT NULL,
    MODIFY COLUMN title_hash       CHAR(40) NOT NULL,
    MODIFY COLUMN description      TEXT,
    MODIFY COLUMN test_steps       JSON,
    MODIFY COLUMN scenario_type    ENUM('ui','api') NOT NULL DEFAULT 'ui',
    MODIFY COLUMN priority         TINYINT NOT NULL DEFAULT 2,
    MODIFY COLUMN status           ENUM('pending','tested','failed','skipped','ignored') NOT NULL DEFAULT 'pending',
    MODIFY COLUMN source           ENUM('plan','auto_expand','seed') NOT NULL DEFAULT 'plan',
    MODIFY COLUMN last_tested_at   DATETIME,
    MODIFY COLUMN created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;

ALTER TABLE agent_runs
    MODIFY COLUMN id             BIGINT AUTO_INCREMENT,
    MODIFY COLUMN project_id     BIGINT,
    MODIFY COLUMN agent_type     ENUM('explore','fix','master','plan') NOT NULL,
    MODIFY COLUMN status         ENUM('running','success','failed','skipped','abandoned') NOT NULL DEFAULT 'running',
    MODIFY COLUMN config_version INT,
    MODIFY COLUMN started_at     DATETIME NOT NULL,
    MODIFY COLUMN finished_at    DATETIME;

ALTER TABLE agent_run_outputs
    MODIFY COLUMN run_id BIGINT NOT NULL,
    MODIFY COLUMN output MEDIUMTEXT;

ALTER TABLE notifications
    MODIFY COLUMN id         BIGINT AUTO_INCREMENT,
    MODIFY COLUMN user_id    BIGINT NOT NULL,
    MODIFY COLUMN project_id BIGINT,
    MODIFY COLUMN type       VARCHAR(64) NOT NULL,
    MODIFY COLUMN content    TEXT NOT NULL,
    MODIFY COLUMN read_at    DATETIME,
    MODIFY COLUMN tg_sent    BOOLEAN NOT NULL DEFAULT FALSE,
    MODIFY COLUMN created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;

ALTER TABLE system_config
    MODIFY COLUMN key_name   VARCHAR(64) NOT NULL,
    MODIFY COLUMN value      TEXT NOT NULL,
    MODIFY COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP;

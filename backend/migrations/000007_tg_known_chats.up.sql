CREATE TABLE tg_known_chats (
  chat_id    BIGINT       NOT NULL PRIMARY KEY COMMENT 'Telegram chat ID（群组为负数）',
  title      VARCHAR(255) NOT NULL              COMMENT '群组名称',
  chat_type  VARCHAR(16)  NOT NULL              COMMENT 'group / supergroup / channel',
  active     TINYINT(1)   NOT NULL DEFAULT 1    COMMENT '0 = Bot 已被移出',
  updated_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

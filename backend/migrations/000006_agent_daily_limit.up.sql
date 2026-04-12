ALTER TABLE project_agents
  ADD COLUMN daily_limit INT NOT NULL DEFAULT 30
  COMMENT 'Agent 24h 内最大运行次数，0 表示不限制';

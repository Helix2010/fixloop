-- Create project_agents table
CREATE TABLE project_agents (
  id               BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键',
  project_id       BIGINT NOT NULL               COMMENT '所属项目',
  agent_type       ENUM('explore','fix','master','plan','generic') NOT NULL
                                                 COMMENT 'generic = 用户自定义 agent',
  name             VARCHAR(64) NOT NULL           COMMENT '显示名称，同项目内唯一',
  alias            VARCHAR(32) NOT NULL           COMMENT '短标识符（字母/数字/连字符），用于分支名、PR 标题、日志；同项目内唯一',
  prompt_override  TEXT                           COMMENT '内置 agent：覆盖默认 prompt；generic：完整 prompt',
  rules            TEXT                           COMMENT '追加到 prompt 末尾的约束规则（可选）',
  schedule_minutes INT NOT NULL                  COMMENT '运行间隔分钟，内置默认：explore=10，fix=30，master=10，plan=10080',
  enabled          TINYINT(1) NOT NULL DEFAULT 1  COMMENT '0=暂停',
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_pa_project_name (project_id, name),
  UNIQUE KEY uq_pa_project_alias (project_id, alias),
  CONSTRAINT fk_pa_project FOREIGN KEY (project_id) REFERENCES projects(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Alter agent_runs table to support generic agent type and link to project_agents
ALTER TABLE agent_runs
  MODIFY COLUMN agent_type VARCHAR(64) NOT NULL COMMENT 'explore/fix/master/plan/generic',
  ADD COLUMN project_agent_id BIGINT NULL COMMENT 'FK to project_agents，内置 agent 也填充此值';

-- Seed existing projects with 4 built-in agents each
INSERT INTO project_agents (project_id, agent_type, name, alias, schedule_minutes, enabled)
SELECT id, 'explore', 'Explore Agent', 'explore', 10,
  IF(JSON_EXTRACT(config,'$.explore_disabled')=true, 0, 1)
FROM projects WHERE deleted_at IS NULL;

INSERT INTO project_agents (project_id, agent_type, name, alias, schedule_minutes, enabled, prompt_override)
SELECT id, 'fix', 'Fix Agent', 'fix', 30,
  IF(JSON_EXTRACT(config,'$.fix_disabled')=true, 0, 1),
  NULLIF(JSON_UNQUOTE(JSON_EXTRACT(config,'$.prompt_overrides.fix')), 'null')
FROM projects WHERE deleted_at IS NULL;

INSERT INTO project_agents (project_id, agent_type, name, alias, schedule_minutes, enabled, prompt_override)
SELECT id, 'plan', 'Plan Agent', 'plan', 10080,
  IF(JSON_EXTRACT(config,'$.plan_disabled')=true, 0, 1),
  NULLIF(JSON_UNQUOTE(JSON_EXTRACT(config,'$.prompt_overrides.plan')), 'null')
FROM projects WHERE deleted_at IS NULL;

INSERT INTO project_agents (project_id, agent_type, name, alias, schedule_minutes, enabled)
SELECT id, 'master', 'Master Agent', 'master', 10, 1
FROM projects WHERE deleted_at IS NULL;

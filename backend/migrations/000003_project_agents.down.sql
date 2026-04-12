-- Revert agent_runs table changes
ALTER TABLE agent_runs
  DROP COLUMN project_agent_id,
  MODIFY COLUMN agent_type ENUM('explore','fix','master','plan') NOT NULL COMMENT 'Agent 类型';

-- Drop project_agents table
DROP TABLE IF EXISTS project_agents;

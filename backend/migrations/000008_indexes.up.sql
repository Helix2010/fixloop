-- Speed up TG bind token lookups (key_name prefix search)
CREATE INDEX idx_system_config_key ON system_config(key_name);

-- Speed up daily run count queries in agent limit checks
CREATE INDEX idx_agent_runs_daily ON agent_runs(project_id, agent_type, started_at);

-- Speed up scheduler project_agents lookup
CREATE INDEX idx_project_agents_proj_enabled ON project_agents(project_id, enabled);

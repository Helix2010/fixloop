ALTER TABLE agent_runs
  ADD COLUMN summary VARCHAR(255) NULL COMMENT '运行结果一行摘要，从输出末尾提取';

ALTER TABLE prs
  ADD COLUMN title     VARCHAR(512) NULL COMMENT 'PR 标题',
  ADD COLUMN merged_by VARCHAR(128) NULL COMMENT '合并操作者（GitHub 用户名或 "Master Agent"）';

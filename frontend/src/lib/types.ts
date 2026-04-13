export interface User {
  id: number;
  github_login: string;
  tg_chat_id?: number | null;
}

export interface Project {
  id: number;
  name: string;
  status: 'active' | 'paused' | 'error';
  github: {
    owner: string;
    repo: string;
    fix_base_branch: string; // editable; default "main"
  };
  issue_tracker: {
    owner: string;
    repo: string;
  };
  vercel?: {
    project_id?: string;
    staging_target?: string;
  };
  test?: {
    staging_url?: string;
    staging_auth_type?: string;
  };
  s3?: {
    endpoint?: string;
    bucket?: string;
    region?: string;
    access_key_id?: string;
  };
  ai_runner?: string;
  ai_model?: string;
  notify_events?: string[];
  tg_chat_id?: number | null;
  webhook_tokens?: { id: string; masked: string }[];
  prompt_overrides?: {
    issue_analysis?: string;
  };
  created_at: string;
}

export interface Issue {
  id: number;
  github_number: number;
  title: string;
  priority: number;
  status: string;
  fix_attempts: number;
  accept_failures: number;
  fixing_since?: string;
  closed_at?: string;
  created_at: string;
}

export interface PR {
  id: number;
  issue_id?: number;
  github_number: number;
  branch: string;
  title?: string;
  status: string;
  merged_by?: string;
  created_at: string;
  merged_at?: string;
}

export interface BacklogItem {
  id: number;
  title: string;
  scenario_type: string;
  priority: number;
  status: string;
  source: string;
  last_tested_at?: string;
  created_at: string;
}

export interface AgentRun {
  id: number;
  agent_type: string;
  status: string;
  summary?: string;
  config_version: number;
  started_at: string;
  finished_at?: string;
}

export interface AgentRunDetail extends AgentRun {
  output?: string;
}

export interface Notification {
  id: number;
  project_id?: number;
  type: string;
  content: string;
  read_at?: string;
  tg_sent: boolean;
  created_at: string;
}

export interface ProjectAgent {
  id: number;
  agent_type: 'explore' | 'fix' | 'master' | 'plan' | 'generic';
  name: string;
  alias: string;
  prompt_override?: string;
  rules?: string;
  schedule_minutes: number;
  daily_limit: number;
  enabled: boolean;
  created_at: string;
}

export interface TgChat {
  chat_id: number;
  title: string;
  chat_type: string;
  bound_project_id?: number | null;
  bound_project_name?: string | null;
}

export interface Pagination {
  page: number;
  per_page: number;
  total: number;
}

export interface PagedResponse<T> {
  data: T[];
  pagination: Pagination;
}

export interface SingleResponse<T> {
  data: T;
}

'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import Link from 'next/link';
import ReactMarkdown from 'react-markdown';
import AuthGuard from '@/components/AuthGuard';
import { PageSpinner, Field, inputClass } from '@/components/ui';
import { apiFetch, ApiError } from '@/lib/api';
import { formatSchedule } from '@/lib/utils';
import type { User, Project, SingleResponse, ProjectAgent, TgChat } from '@/lib/types';

// Prompt defaults are compile-time constants — same for every project and session.
// Cache at module scope to avoid a redundant round-trip on every settings mount.
let cachedPromptDefaults: { fix: string; plan: string; issue_analysis: string } | null = null;

type ValidationState = { valid: boolean; error?: string } | 'loading' | null;

const ALL_EVENTS = [
  { key: 'fix_failed',        label: '修复失败' },
  { key: 'review_timeout',    label: 'PR 审核超时' },
  { key: 'pr_merged',         label: 'PR 已合并' },
  { key: 'deploy_failed',     label: '部署失败' },
  { key: 'issue_closed',      label: 'Issue 已关闭' },
  { key: 'needs_human',       label: '需要人工介入' },
  { key: 'acceptance_failed', label: '验收失败' },
] as const;

const ALL_EVENT_KEYS = ALL_EVENTS.map((e) => e.key) as string[];

const AGENT_TYPE_DESC: Record<string, string> = {
  explore: '自动化 UI 测试，扫描页面异常并创建 Issue',
  fix:     '读取 Issue，生成代码修复并提交 PR',
  plan:    '分析现有 Issue，拆解并生成 Backlog 测试场景',
  master:  '审核 PR，触发验收测试并在通过后自动 merge',
  generic: '自定义 AI Agent，按 Prompt 执行任意任务',
};

const NAV_SECTIONS = [
  { id: 'repo',     label: '代码仓库' },
  { id: 'test',     label: '测试环境' },
  { id: 'deploy',   label: '部署' },
  { id: 'ai',       label: 'AI 引擎' },
  { id: 'agents',   label: 'Agents' },
  { id: 'notify',   label: '通知' },
  { id: 'danger',   label: '危险操作' },
] as const;

export default function SettingsPage() {
  return <AuthGuard>{(user) => <Settings user={user} />}</AuthGuard>;
}

function Settings({ user: _user }: { user: User }) {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [project, setProject] = useState<Project | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');
  const [activeSection, setActiveSection] = useState(() => {
    if (typeof window === 'undefined') return 'repo';
    const hash = window.location.hash.slice(1);
    return NAV_SECTIONS.some((s) => s.id === hash) ? hash : 'repo';
  });
  const [confirmDialog, setConfirmDialog] = useState<{ title: string; message: string; confirmText?: string; danger?: boolean; onConfirm: () => void } | null>(null);
  const sectionRefs = useRef<Record<string, HTMLElement | null>>({});

  const [defaultPrompts, setDefaultPrompts] = useState<{ fix: string; plan: string; issue_analysis: string } | null>(cachedPromptDefaults);
  const [promptValidation, setPromptValidation] = useState<Record<string, ValidationState>>({});
  const [expandedDefaults, setExpandedDefaults] = useState<Record<string, boolean>>({});
  const lastValidated = useRef<Record<string, string>>({});

  const [agents, setAgents] = useState<ProjectAgent[]>([]);
  const [agentsLoading, setAgentsLoading] = useState(true);
  const [expandedAgent, setExpandedAgent] = useState<number | null>(null);
  const [agentEdits, setAgentEdits] = useState<Record<number, { prompt_override: string; rules: string; schedule_minutes: number; daily_limit: number; name: string }>>({});
  const [agentSaving, setAgentSaving] = useState<Record<number, boolean>>({});
  const [agentToggling, setAgentToggling] = useState<Record<number, boolean>>({});
  const [agentErrors, setAgentErrors] = useState<Record<number, string>>({});
  const [showNewAgentForm, setShowNewAgentForm] = useState(false);
  const [newAgent, setNewAgent] = useState({ name: '', alias: '', prompt_override: '', rules: '', schedule_minutes: 60 });
  const [newAgentSaving, setNewAgentSaving] = useState(false);
  const [tgChats, setTgChats] = useState<TgChat[]>([]);
  const [tgChatsLoading, setTgChatsLoading] = useState(false);

  const [form, setForm] = useState({
    github_pat: '',
    fix_base_branch: 'main',
    issue_tracker_owner: '',
    issue_tracker_repo: '',
    staging_url: '',
    staging_auth_type: 'none',
    staging_auth_user: '',
    staging_auth_pass: '',
    staging_auth_token: '',
    vercel_project_id: '',
    vercel_token: '',
    vercel_staging_target: 'preview',
    s3_endpoint: '',
    s3_bucket: '',
    s3_region: '',
    s3_access_key_id: '',
    s3_secret_key: '',
    ai_runner: 'claude',
    ai_model: '',
    ai_api_base: '',
    ai_api_key: '',
    notify_events: ALL_EVENT_KEYS,
    tg_chat_id: '',
  });

  useEffect(() => {
    if (cachedPromptDefaults) return;
    apiFetch<{ data: { fix: string; plan: string; issue_analysis: string } }>(`/api/v1/projects/${id}/prompt-defaults`)
      .then((r) => { cachedPromptDefaults = r.data; setDefaultPrompts(r.data); })
      .catch(() => {/* non-critical */});
  }, [id]);

  useEffect(() => {
    if (!id) return;
    apiFetch<{ data: ProjectAgent[] }>(`/api/v1/projects/${id}/agents`)
      .then((r) => setAgents(r.data))
      .catch(() => {})
      .finally(() => setAgentsLoading(false));
  }, [id]);

  const loadTgChats = useCallback(async () => {
    setTgChatsLoading(true);
    try {
      const r = await apiFetch<{ data: TgChat[] }>('/api/v1/admin/tg-chats');
      setTgChats(r.data ?? []);
    } catch {
      // non-critical
    } finally {
      setTgChatsLoading(false);
    }
  }, []);

  useEffect(() => {
    loadTgChats();
    apiFetch<SingleResponse<Project>>(`/api/v1/projects/${id}`)
      .then((r) => {
        const p = r.data;
        setProject(p);
        setForm({
          github_pat: '',
          fix_base_branch: p.github?.fix_base_branch || 'main',
          issue_tracker_owner: p.issue_tracker?.owner ?? '',
          issue_tracker_repo: p.issue_tracker?.repo ?? '',
          staging_url: p.test?.staging_url ?? '',
          staging_auth_type: p.test?.staging_auth_type ?? 'none',
          staging_auth_user: '',
          staging_auth_pass: '',
          staging_auth_token: '',
          vercel_project_id: p.vercel?.project_id ?? '',
          vercel_token: '',
          vercel_staging_target: p.vercel?.staging_target ?? 'preview',
          s3_endpoint: p.s3?.endpoint ?? '',
          s3_bucket: p.s3?.bucket ?? '',
          s3_region: p.s3?.region ?? '',
          s3_access_key_id: p.s3?.access_key_id ?? '',
          s3_secret_key: '',
          ai_runner: p.ai_runner ?? 'claude',
          ai_model: p.ai_model ?? '',
          ai_api_base: '',
          ai_api_key: '',
          notify_events: p.notify_events?.length ? p.notify_events : ALL_EVENT_KEYS,
          tg_chat_id: p.tg_chat_id ? String(p.tg_chat_id) : '',
        });
      })
      .finally(() => setLoading(false));
  }, [id]);

  // After page load, scroll to the hash section if present
  useEffect(() => {
    if (loading) return;
    const hash = window.location.hash.slice(1);
    if (hash && NAV_SECTIONS.some((s) => s.id === hash)) {
      setTimeout(() => {
        sectionRefs.current[hash]?.scrollIntoView({ behavior: 'instant', block: 'start' });
      }, 50);
    }
  }, [loading]);

  // Scrollspy: track which section is in view and sync hash
  useEffect(() => {
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setActiveSection(entry.target.id);
            window.history.replaceState(null, '', `#${entry.target.id}`);
            break;
          }
        }
      },
      { rootMargin: '-30% 0px -60% 0px', threshold: 0 }
    );
    NAV_SECTIONS.forEach(({ id: sId }) => {
      const el = sectionRefs.current[sId];
      if (el) observer.observe(el);
    });
    return () => observer.disconnect();
  }, [loading]);

  const set =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) =>
      setForm((f) => ({
        ...f,
        [k]: e.target.type === 'checkbox' ? (e.target as HTMLInputElement).checked : e.target.value,
      }));

  const handleRunnerChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    setForm((f) => ({ ...f, ai_runner: e.target.value, ai_model: '', ai_api_base: '', ai_api_key: '' }));
  };

  const validatePrompt = useCallback(async (key: string, tmpl: string) => {
    if (!tmpl) {
      setPromptValidation((v) => ({ ...v, [key]: null }));
      return;
    }
    if (lastValidated.current[key] === tmpl) return;
    lastValidated.current[key] = tmpl;
    setPromptValidation((v) => ({ ...v, [key]: 'loading' }));
    try {
      const res = await apiFetch<{ data: { valid: boolean; error?: string } }>(`/api/v1/projects/${id}/validate-prompt`, {
        method: 'POST',
        body: JSON.stringify({ template: tmpl }),
      });
      setPromptValidation((v) => ({ ...v, [key]: res.data }));
    } catch {
      setPromptValidation((v) => ({ ...v, [key]: null }));
    }
  }, [id]);

  const saveAgent = useCallback(async (agentId: number) => {
    const agent = agents.find((a) => a.id === agentId);
    const edit = agentEdits[agentId] ?? (agent ? {
      prompt_override: agent.prompt_override ?? '',
      rules: agent.rules ?? '',
      schedule_minutes: agent.schedule_minutes,
      daily_limit: agent.daily_limit,
      name: agent.name,
    } : null);
    if (!agent || !edit) return;
    setAgentSaving((s) => ({ ...s, [agentId]: true }));
    try {
      const res = await apiFetch<{ data: ProjectAgent }>(`/api/v1/projects/${id}/agents/${agentId}`, {
        method: 'PATCH',
        body: JSON.stringify({
          name: edit.name || undefined,
          prompt_override: edit.prompt_override,
          rules: edit.rules,
          schedule_minutes: edit.schedule_minutes,
          daily_limit: edit.daily_limit,
          enabled: agent.enabled,
        }),
      });
      setAgents((prev) => prev.map((a) => a.id === agentId ? res.data : a));
      setAgentEdits((e) => { const ne = { ...e }; delete ne[agentId]; return ne; });
      setAgentErrors((e) => { const ne = { ...e }; delete ne[agentId]; return ne; });
    } catch (e) {
      setAgentErrors((prev) => ({ ...prev, [agentId]: e instanceof Error ? e.message : '保存失败' }));
    } finally {
      setAgentSaving((s) => ({ ...s, [agentId]: false }));
    }
  }, [id, agents, agentEdits]);

  const toggleEnabled = useCallback(async (agentId: number) => {
    if (agentToggling[agentId]) return;
    const agent = agents.find((a) => a.id === agentId);
    if (!agent) return;
    setAgentToggling((s) => ({ ...s, [agentId]: true }));
    try {
      const res = await apiFetch<{ data: ProjectAgent }>(`/api/v1/projects/${id}/agents/${agentId}`, {
        method: 'PATCH',
        body: JSON.stringify({ enabled: !agent.enabled }),
      });
      setAgents((prev) => prev.map((a) => a.id === agentId ? res.data : a));
    } catch (e) {
      setAgentErrors((prev) => ({ ...prev, [agentId]: e instanceof Error ? e.message : '操作失败' }));
    } finally {
      setAgentToggling((s) => ({ ...s, [agentId]: false }));
    }
  }, [id, agents, agentToggling]);

  const deleteAgent = useCallback((agentId: number, agentName: string) => {
    setConfirmDialog({
      title: '删除 Agent',
      message: `确定要删除 "${agentName}"？此操作不可撤销。`,
      confirmText: '删除',
      danger: true,
      onConfirm: async () => {
        try {
          await apiFetch(`/api/v1/projects/${id}/agents/${agentId}`, { method: 'DELETE' });
          setAgents((prev) => prev.filter((a) => a.id !== agentId));
        } catch (e) {
          setAgentErrors((prev) => ({ ...prev, [agentId]: e instanceof Error ? e.message : '删除失败' }));
        }
      },
    });
  }, [id]);

  const scrollTo = (sectionId: string) => {
    window.history.replaceState(null, '', `#${sectionId}`);
    sectionRefs.current[sectionId]?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError('');
    setSaved(false);
    try {
      const testPatch: Record<string, string> = {
        staging_url: form.staging_url,
        staging_auth_type: form.staging_auth_type,
      };
      if (form.staging_auth_type === 'basic' && (form.staging_auth_user || form.staging_auth_pass)) {
        testPatch.staging_auth = JSON.stringify({ user: form.staging_auth_user, pass: form.staging_auth_pass });
      }
      if (form.staging_auth_type === 'bearer' && form.staging_auth_token) {
        testPatch.staging_auth = JSON.stringify({ token: form.staging_auth_token });
      }

      const vercelPatch: Record<string, string> = {
        project_id: form.vercel_project_id,
        staging_target: form.vercel_staging_target,
      };
      if (form.vercel_token) vercelPatch.token = form.vercel_token;

      const s3Patch: Record<string, string> = {
        endpoint: form.s3_endpoint,
        bucket: form.s3_bucket,
        region: form.s3_region,
        access_key_id: form.s3_access_key_id,
      };
      if (form.s3_secret_key) s3Patch.secret_key = form.s3_secret_key;

      const githubPatch: Record<string, string> = {
        fix_base_branch: form.fix_base_branch,
      };
      if (form.github_pat) githubPatch.pat = form.github_pat;

      const patch: Record<string, unknown> = {
        github: githubPatch,
        issue_tracker: { owner: form.issue_tracker_owner, repo: form.issue_tracker_repo },
        test: testPatch,
        vercel: vercelPatch,
        s3: s3Patch,
        ai_runner: form.ai_runner,
        notify_events: form.notify_events.length === ALL_EVENT_KEYS.length ? [] : form.notify_events,
      };
      if (form.ai_model) patch.ai_model = form.ai_model;
      if (form.ai_api_base) patch.ai_api_base = form.ai_api_base;
      if (form.ai_api_key) patch.ai_api_key = form.ai_api_key;
      const tgChatIdParsed = parseInt(form.tg_chat_id, 10);
      patch.tg_chat_id = isNaN(tgChatIdParsed) ? 0 : tgChatIdParsed;

      await apiFetch(`/api/v1/projects/${id}`, {
        method: 'PATCH',
        body: JSON.stringify(patch),
      });
      setSaved(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const handleProjectAction = async (action: 'pause' | 'resume') => {
    try {
      await apiFetch(`/api/v1/projects/${id}/${action}`, { method: 'POST' });
      router.push('/dashboard');
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  };

  const handleDelete = () => {
    setConfirmDialog({
      title: '删除项目',
      message: `确定要删除项目 "${project?.name}" 吗？此操作不可撤销。`,
      confirmText: '删除',
      danger: true,
      onConfirm: async () => {
        try {
          await apiFetch(`/api/v1/projects/${id}`, { method: 'DELETE' });
          router.push('/dashboard');
        } catch (err) {
          setError(err instanceof ApiError ? err.message : String(err));
        }
      },
    });
  };

  if (loading) return <PageSpinner />;

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200 sticky top-0 z-20">
        <div className="max-w-5xl mx-auto px-6 py-4 flex items-center gap-3">
          <Link href={`/projects/${id}`} className="text-gray-400 hover:text-gray-600 text-sm">← 项目详情</Link>
          <span className="text-gray-300">/</span>
          <span className="font-semibold text-sm">{project?.name}</span>
          <span className="text-gray-300">/</span>
          <span className="text-sm text-gray-500">设置</span>
        </div>
      </header>

      <div className="max-w-5xl mx-auto px-6 flex gap-8 py-8">
        {/* Sidebar nav */}
        <nav className="w-44 flex-shrink-0">
          <ul className="sticky top-20 space-y-0.5">
            {NAV_SECTIONS.map(({ id: sId, label }) => (
              <li key={sId}>
                <button
                  type="button"
                  onClick={() => scrollTo(sId)}
                  className={`w-full text-left px-3 py-2 rounded-lg text-sm transition-colors ${
                    activeSection === sId
                      ? 'bg-blue-50 text-blue-600 font-medium'
                      : 'text-gray-500 hover:text-gray-700 hover:bg-gray-100'
                  }`}
                >
                  {label}
                </button>
              </li>
            ))}
          </ul>
        </nav>

        {/* Main content */}
        <form onSubmit={handleSave} className="flex-1 min-w-0 space-y-6">

          <Section id="repo" label="代码仓库" desc="GitHub 凭据与仓库配置" sectionRefs={sectionRefs}>
            <Field
              label="Personal Access Token（留空不修改）"
              hint={<>
                <a href="https://github.com/settings/tokens/new" target="_blank" rel="noopener noreferrer" className="text-blue-500 underline">Classic token</a>
                {' '}需 <code className="font-mono text-xs">repo</code> 权限；或使用{' '}
                <a href="https://github.com/settings/tokens?type=beta" target="_blank" rel="noopener noreferrer" className="text-blue-500 underline">Fine-grained token</a>
                {' '}开启 Issues、Pull requests、Contents 读写权限。加密存储。
              </>}
            >
              <input className={inputClass} type="password" value={form.github_pat} onChange={set('github_pat')} placeholder="ghp_...（留空不修改）" />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="修复基准分支" hint="Fix Agent 将基于此分支创建修复 PR，默认 main">
                <input className={inputClass} value={form.fix_base_branch} onChange={set('fix_base_branch')} placeholder="main" />
              </Field>
              <Field
                label="Issue 仓库"
                hint={<>FixLoop 在此仓库创建 Issue，格式 <code className="font-mono text-xs">owner/repo</code></>}
              >
                <input
                  className={inputClass}
                  value={`${form.issue_tracker_owner}/${form.issue_tracker_repo}`}
                  onChange={(e) => {
                    const [owner = '', repo = ''] = e.target.value.split('/');
                    setForm((f) => ({ ...f, issue_tracker_owner: owner, issue_tracker_repo: repo }));
                  }}
                  placeholder="owner/repo"
                />
              </Field>
            </div>
            <DeployKeyCard projectId={id} />
          </Section>

          <Section id="test" label="测试环境" desc="Explore Agent 访问此地址进行自动化 UI 测试" sectionRefs={sectionRefs}>
            <Field label="测试环境地址" hint="需可公开访问。留空则跳过 UI 探索，仅执行代码层修复。">
              <input className={inputClass} value={form.staging_url} onChange={set('staging_url')} placeholder="https://staging.example.com" />
            </Field>
            <Field label="认证类型" hint="basic = HTTP Basic Auth；bearer = Bearer Token；无 = 不需要认证">
              <select className={inputClass} value={form.staging_auth_type} onChange={set('staging_auth_type')}>
                <option value="none">无</option>
                <option value="basic">基础认证（Basic Auth）</option>
                <option value="bearer">Bearer 令牌</option>
              </select>
            </Field>
            {form.staging_auth_type === 'basic' && (
              <div className="grid grid-cols-2 gap-3">
                <Field label="用户名">
                  <input className={inputClass} value={form.staging_auth_user} onChange={set('staging_auth_user')} placeholder="admin" />
                </Field>
                <Field label="密码" hint="留空不修改，加密存储">
                  <input className={inputClass} type="password" value={form.staging_auth_pass} onChange={set('staging_auth_pass')} placeholder="••••••••" />
                </Field>
              </div>
            )}
            {form.staging_auth_type === 'bearer' && (
              <Field label="Bearer Token" hint="留空不修改，加密存储">
                <input className={inputClass} type="password" value={form.staging_auth_token} onChange={set('staging_auth_token')} placeholder="••••••••" />
              </Field>
            )}
          </Section>

          <Section id="deploy" label="部署" desc="Vercel 自动验收 + 截图存储" sectionRefs={sectionRefs}>
            <SubLabel>Vercel</SubLabel>
            <div className="grid grid-cols-2 gap-3">
              <Field label="项目 ID" hint={<>Settings → General，复制 <code className="font-mono text-xs">prj_xxx</code></>}>
                <input className={inputClass} value={form.vercel_project_id} onChange={set('vercel_project_id')} placeholder="prj_xxx" />
              </Field>
              <Field label="部署目标" hint="preview = PR 预览部署；production = 主分支">
                <select className={inputClass} value={form.vercel_staging_target} onChange={set('vercel_staging_target')}>
                  <option value="preview">预览环境</option>
                  <option value="production">生产环境</option>
                </select>
              </Field>
            </div>
            <Field label="访问令牌（留空不修改）" hint={<>Account Settings → Tokens，需 <strong>Full Account</strong> 权限</>}>
              <input className={inputClass} type="password" value={form.vercel_token} onChange={set('vercel_token')} placeholder="••••••••" />
            </Field>

            <SubLabel top>截图存储（S3 兼容）</SubLabel>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Endpoint" hint="例如 obs.cn-north-4.myhuaweicloud.com">
                <input className={inputClass} value={form.s3_endpoint} onChange={set('s3_endpoint')} placeholder="obs.cn-north-4.myhuaweicloud.com" />
              </Field>
              <Field label="存储桶" hint="需开启公开读取权限">
                <input className={inputClass} value={form.s3_bucket} onChange={set('s3_bucket')} placeholder="fixloop-screenshots" />
              </Field>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <Field label="区域（可选）">
                <input className={inputClass} value={form.s3_region} onChange={set('s3_region')} placeholder="cn-north-4" />
              </Field>
              <Field label="Access Key ID">
                <input className={inputClass} value={form.s3_access_key_id} onChange={set('s3_access_key_id')} placeholder="AK..." />
              </Field>
            </div>
            <Field label="Secret Key（留空不修改）" hint="加密存储">
              <input className={inputClass} type="password" value={form.s3_secret_key} onChange={set('s3_secret_key')} placeholder="••••••••" />
            </Field>
          </Section>

          <Section id="ai" label="AI 引擎" desc="所有 Agent 共用的 AI 配置" sectionRefs={sectionRefs}>
            <div className="grid grid-cols-2 gap-3">
              <Field label="引擎">
                <select className={inputClass} value={form.ai_runner} onChange={handleRunnerChange}>
                  <option value="claude">Claude（推荐）</option>
                  <option value="aider">Aider</option>
                  <option value="gemini">Gemini</option>
                </select>
              </Field>
              <Field
                label="模型"
                hint={
                  form.ai_runner === 'claude' ? '留空使用默认 claude-opus-4-6' :
                  form.ai_runner === 'gemini' ? '留空使用默认 gemini-2.5-pro' :
                  '例如 deepseek-chat、gpt-4o'
                }
              >
                <input
                  className={inputClass}
                  value={form.ai_model}
                  onChange={set('ai_model')}
                  placeholder={
                    form.ai_runner === 'claude' ? 'claude-opus-4-6' :
                    form.ai_runner === 'gemini' ? 'gemini-2.5-pro' :
                    'gpt-4o'
                  }
                />
              </Field>
            </div>
            {form.ai_runner === 'claude' && (
              <Field
                label="Anthropic API 密钥（可选）"
                hint={<>留空则使用服务器已登录的 <code className="font-mono text-xs">claude</code> CLI。<a href="https://console.anthropic.com/settings/keys" target="_blank" rel="noopener noreferrer" className="text-blue-500 underline ml-1">获取密钥</a>，加密存储。</>}
              >
                <input className={inputClass} type="password" value={form.ai_api_key} onChange={set('ai_api_key')} placeholder="sk-ant-...（留空不修改）" />
              </Field>
            )}
            {form.ai_runner === 'aider' && (
              <>
                <Field label="API 基础地址" hint="OpenAI 兼容接口，例如 https://api.deepseek.com/v1">
                  <input className={inputClass} value={form.ai_api_base} onChange={set('ai_api_base')} placeholder="https://api.deepseek.com/v1" />
                </Field>
                <Field label="API 密钥（留空不修改）" hint="加密存储">
                  <input className={inputClass} type="password" value={form.ai_api_key} onChange={set('ai_api_key')} placeholder="••••••••" />
                </Field>
              </>
            )}
            {form.ai_runner === 'gemini' && (
              <Field
                label="Gemini API 密钥（可选）"
                hint="留空则使用服务器 GEMINI_API_KEY 环境变量。加密存储。"
              >
                <input className={inputClass} type="password" value={form.ai_api_key} onChange={set('ai_api_key')} placeholder="AIza...（留空不修改）" />
              </Field>
            )}
          </Section>

          <Section id="agents" label="Agents" desc="管理项目的所有 Agent，包括内置 Agent 和自定义 Agent" sectionRefs={sectionRefs}>
            {agentsLoading ? (
              <p className="text-sm text-gray-500">加载中…</p>
            ) : (
              <div className="space-y-3">
                {agents.map((agent) => {
                  const isExpanded = expandedAgent === agent.id;
                  const edit = agentEdits[agent.id] ?? {
                    prompt_override: agent.prompt_override ?? '',
                    rules: agent.rules ?? '',
                    schedule_minutes: agent.schedule_minutes,
                    daily_limit: agent.daily_limit,
                    name: agent.name,
                  };
                  const isSaving = agentSaving[agent.id];
                  const agentError = agentErrors[agent.id];
                  const isBuiltin = agent.agent_type !== 'generic';

                  return (
                    <div key={agent.id} className="border border-gray-200 rounded-lg overflow-hidden">
                      <div className="flex items-center justify-between px-4 py-3 bg-gray-50">
                        <div className="flex items-center gap-3 min-w-0">
                          <span
                            title={AGENT_TYPE_DESC[agent.agent_type]}
                            className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium cursor-default ${
                              agent.agent_type === 'generic' ? 'bg-purple-100 text-purple-700' : 'bg-blue-100 text-blue-700'
                            }`}
                          >
                            {agent.agent_type}
                          </span>
                          <span className="font-medium text-gray-900 truncate">{agent.name}</span>
                          <button
                            type="button"
                            title="复制 alias"
                            onClick={() => navigator.clipboard.writeText(agent.alias)}
                            className="text-xs text-gray-400 bg-gray-100 hover:bg-gray-200 px-1.5 py-0.5 rounded font-mono transition-colors cursor-copy"
                          >{agent.alias}</button>
                          <span className="text-xs text-gray-400">{formatSchedule(agent.schedule_minutes)}</span>
                        </div>
                        <div className="flex items-center gap-2 flex-shrink-0">
                          {!isBuiltin && (
                            <button
                              type="button"
                              onClick={() => deleteAgent(agent.id, agent.name)}
                              className="text-xs text-red-500 hover:text-red-700 px-2 py-1"
                            >
                              删除
                            </button>
                          )}
                          <button
                            type="button"
                            onClick={() => {
                              if (isExpanded) { setExpandedAgent(null); return; }
                              setExpandedAgent(agent.id);
                              // Pre-fill with effective prompt (saved override or system default)
                              setAgentEdits((prev) => {
                                if (prev[agent.id]) return prev;
                                const defaultPrompt =
                                  (agent.agent_type === 'fix' || agent.agent_type === 'plan')
                                    ? defaultPrompts?.[agent.agent_type] ?? ''
                                    : '';
                                return {
                                  ...prev,
                                  [agent.id]: {
                                    prompt_override: agent.prompt_override ?? defaultPrompt,
                                    rules: agent.rules ?? '',
                                    schedule_minutes: agent.schedule_minutes,
                                    daily_limit: agent.daily_limit,
                                    name: agent.name,
                                  },
                                };
                              });
                            }}
                            className="text-xs text-indigo-600 hover:text-indigo-800 px-2 py-1 border border-indigo-200 rounded"
                          >
                            配置
                          </button>
                          <button
                            type="button"
                            onClick={() => toggleEnabled(agent.id)}
                            disabled={agentToggling[agent.id]}
                            className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${
                              agent.enabled ? 'bg-indigo-600' : 'bg-gray-300'
                            } disabled:opacity-50`}
                            aria-label={agent.enabled ? '禁用' : '启用'}
                          >
                            <span className={`inline-block h-3 w-3 transform rounded-full bg-white transition-transform ${
                              agent.enabled ? 'translate-x-5' : 'translate-x-1'
                            }`} />
                          </button>
                        </div>
                      </div>
                      {isExpanded && (
                        <div className="px-4 py-4 space-y-3 border-t border-gray-200">
                          <Field label="名称">
                            <input
                              className={inputClass}
                              value={edit.name}
                              onChange={(e) => setAgentEdits((prev) => ({ ...prev, [agent.id]: { ...edit, name: e.target.value } }))}
                            />
                          </Field>
                          <Field label="运行间隔（分钟）">
                            <input
                              className={inputClass}
                              type="number"
                              min={10}
                              value={edit.schedule_minutes}
                              onChange={(e) => setAgentEdits((prev) => ({ ...prev, [agent.id]: { ...edit, schedule_minutes: Number(e.target.value) } }))}
                            />
                          </Field>
                          {agent.agent_type === 'explore' && (
                            <Field label="每日运行上限（次）">
                              <input
                                className={inputClass}
                                type="number"
                                min={1}
                                value={edit.daily_limit}
                                onChange={(e) => setAgentEdits((prev) => ({ ...prev, [agent.id]: { ...edit, daily_limit: Number(e.target.value) } }))}
                              />
                            </Field>
                          )}
                          {(agent.agent_type === 'fix' || agent.agent_type === 'plan' || agent.agent_type === 'generic') && (
                            <AgentPromptField
                              agentType={agent.agent_type}
                              value={edit.prompt_override}
                              onChange={(v) => setAgentEdits((prev) => ({ ...prev, [agent.id]: { ...edit, prompt_override: v } }))}
                            />
                          )}
                          <AgentRulesField
                            agentType={agent.agent_type}
                            value={edit.rules}
                            onChange={(v) => setAgentEdits((prev) => ({ ...prev, [agent.id]: { ...edit, rules: v } }))}
                          />
                          <div className="flex items-center justify-end gap-3">
                            {agentError && (
                              <span className="text-sm text-red-500 flex items-center gap-1">
                                <svg className="w-4 h-4 flex-shrink-0" viewBox="0 0 20 20" fill="currentColor"><path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.28 7.22a.75.75 0 00-1.06 1.06L8.94 10l-1.72 1.72a.75.75 0 101.06 1.06L10 11.06l1.72 1.72a.75.75 0 101.06-1.06L11.06 10l1.72-1.72a.75.75 0 00-1.06-1.06L10 8.94 8.28 7.22z" clipRule="evenodd"/></svg>
                                {agentError}
                              </span>
                            )}
                            <button
                              type="button"
                              onClick={() => saveAgent(agent.id)}
                              disabled={isSaving}
                              className="px-4 py-1.5 bg-indigo-600 text-white text-sm rounded hover:bg-indigo-700 disabled:opacity-50"
                            >
                              {isSaving ? '保存中…' : '保存'}
                            </button>
                          </div>
                        </div>
                      )}
                    </div>
                  );
                })}

                {/* New Agent Form */}
                {showNewAgentForm ? (
                  <div className="border border-dashed border-gray-300 rounded-lg p-4 space-y-3">
                    <h4 className="text-sm font-medium text-gray-700">新建自定义 Agent</h4>
                    <Field label="名称">
                      <input
                        className={inputClass}
                        value={newAgent.name}
                        onChange={(e) => setNewAgent((n) => ({ ...n, name: e.target.value }))}
                        placeholder="My Custom Agent"
                      />
                    </Field>
                    <Field label="别名（alias）" hint="小写字母/数字/连字符，用于分支名和 PR 标题">
                      <input
                        className={inputClass}
                        value={newAgent.alias}
                        onChange={(e) => setNewAgent((n) => ({ ...n, alias: e.target.value }))}
                        placeholder="my-agent"
                      />
                    </Field>
                    <Field label="运行间隔（分钟）">
                      <input
                        className={inputClass}
                        type="number"
                        min={10}
                        value={newAgent.schedule_minutes}
                        onChange={(e) => setNewAgent((n) => ({ ...n, schedule_minutes: Number(e.target.value) }))}
                      />
                    </Field>
                    <Field label="Prompt" hint="AI 将收到此 Prompt + 仓库目录树">
                      <textarea
                        className={`${inputClass} font-mono text-xs`}
                        rows={6}
                        value={newAgent.prompt_override}
                        onChange={(e) => setNewAgent((n) => ({ ...n, prompt_override: e.target.value }))}
                        placeholder="You are an expert developer. Your task is…"
                      />
                    </Field>
                    <Field label="Rules（可选）">
                      <textarea
                        className={inputClass}
                        rows={2}
                        value={newAgent.rules}
                        onChange={(e) => setNewAgent((n) => ({ ...n, rules: e.target.value }))}
                        placeholder="可选约束规则…"
                      />
                    </Field>
                    <div className="flex gap-2 justify-end">
                      <button
                        type="button"
                        onClick={() => { setShowNewAgentForm(false); setNewAgent({ name: '', alias: '', prompt_override: '', rules: '', schedule_minutes: 60 }); }}
                        className="px-4 py-1.5 text-sm text-gray-600 border border-gray-300 rounded hover:bg-gray-50"
                      >
                        取消
                      </button>
                      <button
                        type="button"
                        disabled={newAgentSaving}
                        onClick={async () => {
                          setNewAgentSaving(true);
                          try {
                            const res = await apiFetch<{ data: ProjectAgent }>(`/api/v1/projects/${id}/agents`, {
                              method: 'POST',
                              body: JSON.stringify({
                                name: newAgent.name,
                                alias: newAgent.alias,
                                prompt_override: newAgent.prompt_override,
                                rules: newAgent.rules || undefined,
                                schedule_minutes: newAgent.schedule_minutes,
                              }),
                            });
                            setAgents((prev) => [...prev, res.data]);
                            setShowNewAgentForm(false);
                            setNewAgent({ name: '', alias: '', prompt_override: '', rules: '', schedule_minutes: 60 });
                          } catch (e) {
                            setError(e instanceof Error ? e.message : '创建失败');
                          } finally {
                            setNewAgentSaving(false);
                          }
                        }}
                        className="px-4 py-1.5 bg-indigo-600 text-white text-sm rounded hover:bg-indigo-700 disabled:opacity-50"
                      >
                        {newAgentSaving ? '创建中…' : '创建'}
                      </button>
                    </div>
                  </div>
                ) : (
                  <button
                    type="button"
                    onClick={() => setShowNewAgentForm(true)}
                    className="w-full py-2 text-sm text-indigo-600 border border-dashed border-indigo-300 rounded-lg hover:bg-indigo-50"
                  >
                    + 新建 Agent
                  </button>
                )}
              </div>
            )}

            {/* Webhook Token */}
            <WebhookTokenSection
              projectId={id}
              tokens={project?.webhook_tokens ?? []}
              onAdd={(masked) => setProject((p) => p ? { ...p, webhook_tokens: [...(p.webhook_tokens ?? []), masked] } : p)}
              onRemove={(id) => setProject((p) => p ? { ...p, webhook_tokens: (p.webhook_tokens ?? []).filter((x) => x.id !== id) } : p)}
              onConfirm={setConfirmDialog}
            />
          </Section>

          <Section id="notify" label="通知" desc="Telegram 推送配置" sectionRefs={sectionRefs}>
            <p className="text-xs text-gray-500 mb-3">选择需要推送的事件类型（取消全部 = 静默）</p>
            <div className="grid grid-cols-2 gap-2">
              {ALL_EVENTS.map(({ key, label }) => (
                <label key={key} className="flex items-center gap-2 text-sm cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.notify_events.includes(key)}
                    onChange={(e) =>
                      setForm((f) => ({
                        ...f,
                        notify_events: e.target.checked
                          ? [...f.notify_events, key]
                          : f.notify_events.filter((k) => k !== key),
                      }))
                    }
                    className="w-4 h-4 rounded border-gray-300 accent-blue-500"
                  />
                  <span>{label}</span>
                </label>
              ))}
            </div>
            <Field
              label="推送目标群组（可选）"
              hint="留空则发送到 Dashboard 绑定的个人账号。将 Bot 加入群组后点「刷新」即可在此选择。"
            >
              <div className="flex gap-2">
                <select
                  className={`flex-1 ${inputClass}`}
                  value={form.tg_chat_id}
                  onChange={set('tg_chat_id')}
                >
                  <option value="">— 留空，发送到个人账号 —</option>
                  {tgChats
                    .filter(c => !c.bound_project_id || String(c.chat_id) === form.tg_chat_id)
                    .map(chat => (
                      <option key={chat.chat_id} value={String(chat.chat_id)}>
                        {chat.title}（{chat.chat_id}）
                      </option>
                    ))}
                  {(() => {
                    const alreadyBound = tgChats.filter(c => !!c.bound_project_id && String(c.chat_id) !== form.tg_chat_id);
                    if (alreadyBound.length === 0) return null;
                    return <>
                      <option disabled>── 已关联其他项目（不可选）──</option>
                      {alreadyBound.map(chat => (
                        <option key={chat.chat_id} value={String(chat.chat_id)} disabled>
                          {chat.title}（已关联：{chat.bound_project_name}）
                        </option>
                      ))}
                    </>;
                  })()}
                  {form.tg_chat_id && !tgChats.find(c => String(c.chat_id) === form.tg_chat_id) && (
                    <option value={form.tg_chat_id}>{form.tg_chat_id}（手动输入）</option>
                  )}
                </select>
                <button
                  type="button"
                  onClick={loadTgChats}
                  disabled={tgChatsLoading}
                  className="px-3 py-2 text-sm rounded-md border border-gray-300 hover:bg-gray-50 disabled:opacity-50 whitespace-nowrap"
                >
                  {tgChatsLoading ? '…' : '刷新'}
                </button>
              </div>
            </Field>
          </Section>

          {/* Save bar */}
          <div className="sticky bottom-0 bg-white border-t border-gray-200 -mx-6 px-6 py-4 flex items-center gap-4">
            <button
              type="submit"
              disabled={saving}
              className="bg-blue-500 hover:bg-blue-600 disabled:opacity-50 text-white px-6 py-2.5 rounded-lg text-sm font-semibold transition-colors"
            >
              {saving ? '保存中...' : '保存配置'}
            </button>
            {saved && <span className="text-green-600 text-sm">已保存 ✓</span>}
            {error && <span className="text-red-500 text-sm">{error}</span>}
          </div>

          <Section id="danger" label="危险操作" desc="" sectionRefs={sectionRefs} danger>
            <div className="flex flex-wrap gap-3">
              {project?.status === 'active' ? (
                <button type="button" onClick={() => handleProjectAction('pause')} className="border border-yellow-300 text-yellow-700 px-4 py-2 rounded-lg text-sm font-medium hover:bg-yellow-50 transition-colors">
                  暂停项目
                </button>
              ) : (
                <button type="button" onClick={() => handleProjectAction('resume')} className="border border-green-300 text-green-700 px-4 py-2 rounded-lg text-sm font-medium hover:bg-green-50 transition-colors">
                  恢复项目
                </button>
              )}
              <button type="button" onClick={handleDelete} className="border border-red-300 text-red-600 px-4 py-2 rounded-lg text-sm font-medium hover:bg-red-50 transition-colors">
                删除项目
              </button>
            </div>
          </Section>

        </form>
      </div>

      {confirmDialog && (
        <div className="fixed inset-0 bg-black/50 z-50 flex items-center justify-center px-4" onClick={() => setConfirmDialog(null)}>
          <div className="bg-white rounded-2xl shadow-2xl w-full max-w-sm overflow-hidden" onClick={(e) => e.stopPropagation()}>
            <div className="px-6 pt-6 pb-5">
              <div className="flex items-start gap-4">
                <div className={`flex-shrink-0 w-10 h-10 rounded-full flex items-center justify-center ${confirmDialog.danger ? 'bg-red-50' : 'bg-yellow-50'}`}>
                  <svg className={`w-5 h-5 ${confirmDialog.danger ? 'text-red-500' : 'text-yellow-500'}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                  </svg>
                </div>
                <div className="flex-1 min-w-0">
                  <h3 className="text-base font-semibold text-gray-900">{confirmDialog.title}</h3>
                  <p className="mt-1 text-sm text-gray-500 leading-relaxed">{confirmDialog.message}</p>
                </div>
              </div>
            </div>
            <div className="px-6 pb-5 flex gap-3">
              <button onClick={() => setConfirmDialog(null)} className="flex-1 border border-gray-200 text-gray-700 py-2.5 rounded-xl text-sm font-medium hover:bg-gray-50 active:bg-gray-100 transition-colors">
                取消
              </button>
              <button
                onClick={() => { setConfirmDialog(null); confirmDialog.onConfirm(); }}
                className={`flex-1 py-2.5 rounded-xl text-sm font-semibold text-white transition-colors ${confirmDialog.danger ? 'bg-red-500 hover:bg-red-600 active:bg-red-700' : 'bg-blue-500 hover:bg-blue-600 active:bg-blue-700'}`}
              >
                {confirmDialog.confirmText ?? '确认'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}


type SectionProps = {
  id: string;
  label: string;
  desc: string;
  children: React.ReactNode;
  sectionRefs: React.MutableRefObject<Record<string, HTMLElement | null>>;
  danger?: boolean;
};

function Section({ id, label, desc, children, sectionRefs, danger }: SectionProps) {
  return (
    <section
      id={id}
      ref={(el) => { sectionRefs.current[id] = el; }}
      className={`bg-white rounded-xl border p-6 scroll-mt-20 ${danger ? 'border-red-200' : 'border-gray-200'}`}
    >
      <div className="mb-5">
        <h2 className={`text-base font-semibold ${danger ? 'text-red-600' : 'text-gray-800'}`}>{label}</h2>
        {desc && <p className="text-xs text-gray-400 mt-0.5">{desc}</p>}
      </div>
      <div className="space-y-4">{children}</div>
    </section>
  );
}


function SubLabel({ children, top }: { children: React.ReactNode; top?: boolean }) {
  return (
    <p className={`text-xs font-medium text-gray-400 uppercase tracking-wide${top ? ' pt-2' : ''}`}>
      {children}
    </p>
  );
}

type ConfirmDialogProps = { title: string; message: string; confirmText?: string; danger?: boolean; onConfirm: () => void };


const PROMPT_PLACEHOLDERS: Record<string, { name: string; desc: string }[]> = {
  fix: [
    { name: '{{.IssueTitle}}', desc: '工单标题' },
    { name: '{{.IssueBody}}', desc: '工单正文（Markdown）' },
    { name: '{{.DirTree}}', desc: '仓库目录树（3层深度）' },
    { name: '{{.PreviousFailures}}', desc: '历次失败尝试的摘要（若有）' },
  ],
  plan: [
    { name: '{{.Owner}}', desc: 'GitHub 仓库 Owner' },
    { name: '{{.Repo}}', desc: 'GitHub 仓库名' },
    { name: '{{.StagingURL}}', desc: '预发布环境 URL' },
    { name: '{{.PendingCount}}', desc: '待处理 Backlog 条数' },
    { name: '{{.RecentIssues}}', desc: '最近 Issue 列表摘要' },
    { name: '{{.Count}}', desc: '本次建议生成条数（固定 5）' },
  ],
  generic: [],
};

const PROMPT_TEMPLATES: Record<string, { label: string; value: string }[]> = {
  fix: [
    {
      label: '专注前端修复',
      value: `你是一位专业前端工程师，只修改前端相关文件（.tsx/.ts/.css）。

## 修复规则
1. **只改前端** — 不动后端代码，不改 API 接口，不改 CI/CD 流水线
2. **最小改动** — 不重构、不改命名、不加无用注释
3. **匹配现有风格** — 使用项目已有的组件和样式系统

## 工单信息
标题：{{.IssueTitle}}
内容：
{{.IssueBody}}

## 仓库结构
{{.DirTree}}
{{if .PreviousFailures}}
## 历次失败尝试
{{.PreviousFailures}}
{{end}}`,
    },
    {
      label: '专注后端修复',
      value: `你是一位专业后端工程师，只修改后端逻辑文件。

## 修复规则
1. **只改后端** — 不动前端文件，不改 SQL migration，不改 CI/CD 流水线
2. **最小改动** — 不加错误处理包装、不改函数签名
3. **匹配现有风格** — 遵循项目的错误处理和日志风格

## 工单信息
标题：{{.IssueTitle}}
内容：
{{.IssueBody}}

## 仓库结构
{{.DirTree}}
{{if .PreviousFailures}}
## 历次失败尝试
{{.PreviousFailures}}
{{end}}`,
    },
  ],
  plan: [
    {
      label: '测试场景规划',
      value: `你是项目测试规划 Agent，为 {{.Owner}}/{{.Repo}} 生成 {{.Count}} 条新测试场景，补充到 Backlog。

## 项目状态
- Staging 地址：{{.StagingURL}}
- 待测场景：{{.PendingCount}} 条
- 最近 Issue：
{{.RecentIssues}}

## 生成规则
1. 每条场景有明确操作步骤和二值化验收标准（PASS/FAIL）
2. 优先级：P1=核心流程，P2=重要功能，P3=边界异常，P4=体验优化
3. 不重复已有场景（标题关键词重叠 > 60% 则跳过）

## 输出 {{.Count}} 条，格式：
- **标题**：[简短动词短语]
- **步骤**：[操作 + 验证标准]
- **优先级**：P1/P2/P3`,
    },
    {
      label: '功能规划',
      value: `你是项目技术负责人，为 {{.Owner}}/{{.Repo}} 生成 {{.Count}} 条新功能或改进建议。

## 项目状态
- 预发布地址：{{.StagingURL}}
- 待处理 Backlog：{{.PendingCount}} 条
- 最近 Issue：
{{.RecentIssues}}

## 输出要求
每条建议格式：
- **标题**：[简洁动词短语]
- **描述**：[2-3句，说明具体改进点和预期收益]
- **优先级**：P1/P2/P3

只输出 {{.Count}} 条，不要重复已有 Issue。`,
    },
  ],
  generic: [
    {
      label: '代码审查',
      value: `你是一位经验丰富的代码审查员。

## 任务
扫描仓库中最近修改的代码，发现潜在问题并给出改进建议。

## 关注重点
1. 安全漏洞（SQL 注入、XSS、未验证输入）
2. 性能问题（N+1 查询、无效循环、内存泄漏）
3. 逻辑错误（边界条件、空指针）

## 输出格式
每个问题：
- **文件**：路径:行号
- **问题**：具体描述
- **建议**：如何修复`,
    },
    {
      label: '依赖更新检查',
      value: `检查仓库中所有依赖文件（package.json、go.mod 等），列出：
1. 有已知安全漏洞的依赖（CVE）
2. 落后主版本 2+ 的过时依赖
3. 给出升级建议和注意事项`,
    },
  ],
};

const RULES_TEMPLATES: Record<string, { label: string; value: string }[]> = {
  explore: [
    {
      label: '标准优先级规则',
      value: `# 规则说明：每行 P<n>: 关键词1, 关键词2, ...
# 匹配范围：场景标题 + 错误类型(crash/timeout/assertion/console_error) + 错误信息
# 按顺序匹配，第一条命中的规则生效

P1: crash, err_connection_refused, net::err, 核心功能, 无法访问, 首页
P2: assertion, console_error, 登录, 注册, 支付, 提交, 数据
P3: timeout, 加载慢, 超时
P4: 样式, 文案, 布局`,
    },
    {
      label: 'SaaS 场景规则',
      value: `P1: crash, 无法登录, 无法注册, 支付失败, 数据丢失, 首页崩溃
P2: assertion, console_error, 核心功能, 表单, 用户信息, 权限
P3: timeout, 加载慢, 列表, 搜索
P4: 样式, 文案, 图标, 体验`,
    },
  ],
  fix: [
    {
      label: '优先级控制',
      value: `MAX_PRIORITY: 2
MAX_ATTEMPTS: 3`,
    },
    {
      label: '安全约束',
      value: `- 禁止引入新的外部依赖
- 禁止修改 CI/CD 流水线文件（.github/workflows/）
- 禁止使用 git push --force
- 修改数据库相关代码时必须保证向后兼容`,
    },
    {
      label: '代码规范约束',
      value: `- 注释与错误提示文案使用中文
- 不引入新依赖，优先使用已有组件和工具函数
- 禁止添加无用注释（代码即文档）
- 只修改与 Issue 相关的文件，不做额外重构`,
    },
  ],
  plan: [
    {
      label: '场景约束',
      value: `- 每条场景聚焦单一用户操作（步骤不超过 5 步）
- 验收标准必须二值化（PASS/FAIL），不含糊
- 优先覆盖核心用户路径，再覆盖边界和异常
- 每次生成 10-20 条，聚焦覆盖空白区域`,
    },
    {
      label: '功能规划约束',
      value: `- 每条建议必须可在 1-3 天内完成
- 优先修复现有 Bug，再提新功能
- 不建议涉及数据库 Schema 变更的功能（风险高）
- 建议要可测试，需有明确的验收标准`,
    },
  ],
  master: [
    {
      label: '合并验收约束',
      value: `- 必须等待 code review 完成后再决定合并
- Review 通过 → squash merge
- Review 请求修改 → 回滚 issue 为 open，等待 fix-agent 处理
- staging_url 未配置时跳过 UI 验收，仅验证 commit SHA
- fix_attempts >= 3 → 改为 needs-human，停止自动处理`,
    },
  ],
  generic: [
    {
      label: '安全约束',
      value: `- 只读操作，不修改任何文件
- 不调用外部 API
- 输出结果不超过 2000 字`,
    },
  ],
};

function AgentPromptField({ agentType, value, onChange }: {
  agentType: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const [showRef, setShowRef] = useState(false);
  const placeholders = PROMPT_PLACEHOLDERS[agentType] ?? [];
  const templates = PROMPT_TEMPLATES[agentType] ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-1">
        <label className="text-sm font-medium text-gray-700">
          自定义 Prompt
          {agentType !== 'generic' && (
            <span className="ml-1 text-xs font-normal text-gray-400">（留空使用系统默认）</span>
          )}
        </label>
        <div className="flex items-center gap-2">
          {placeholders.length > 0 && (
            <button
              type="button"
              onClick={() => setShowRef((v) => !v)}
              className="text-xs text-indigo-500 hover:text-indigo-700 flex items-center gap-1"
            >
              <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" /></svg>
              占位符参考
            </button>
          )}
          {templates.length > 0 && (
            <select
              className="text-xs border border-gray-200 rounded px-2 py-1 bg-white text-gray-600 focus:outline-none focus:ring-1 focus:ring-indigo-300"
              defaultValue=""
              onChange={(e) => { if (e.target.value) { onChange(e.target.value); e.target.value = ''; } }}
            >
              <option value="">插入模板…</option>
              {templates.map((t) => (
                <option key={t.label} value={t.value}>{t.label}</option>
              ))}
            </select>
          )}
        </div>
      </div>
      {showRef && placeholders.length > 0 && (
        <div className="mb-2 p-3 bg-indigo-50 border border-indigo-100 rounded-lg">
          <p className="text-xs font-medium text-indigo-700 mb-1.5">可用占位符（Go template 语法）</p>
          <div className="grid grid-cols-2 gap-x-4 gap-y-1">
            {placeholders.map((p) => (
              <div key={p.name} className="flex items-baseline gap-1.5">
                <code className="text-xs font-mono text-indigo-600 flex-shrink-0">{p.name}</code>
                <span className="text-xs text-gray-500">{p.desc}</span>
              </div>
            ))}
          </div>
        </div>
      )}
      <MdEditor value={value} onChange={onChange} rows={12} />
    </div>
  );
}

function AgentRulesField({ agentType, value, onChange }: {
  agentType: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const [showHelp, setShowHelp] = useState(false);
  const templates = RULES_TEMPLATES[agentType] ?? [];

  const isExplore = agentType === 'explore';

  return (
    <div>
      <div className="flex items-center justify-between mb-1">
        <label className="text-sm font-medium text-gray-700">
          规则约束
          <span className="ml-1 text-xs font-normal text-gray-400">
            {isExplore ? '（优先级分类规则）' : '（追加到 Prompt 末尾的约束，可选）'}
          </span>
        </label>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setShowHelp((v) => !v)}
            className="text-xs text-gray-400 hover:text-gray-600 flex items-center gap-1"
          >
            <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M9.879 7.519c1.171-1.025 3.071-1.025 4.242 0 1.172 1.025 1.172 2.687 0 3.712-.203.179-.43.326-.67.442-.745.361-1.45.999-1.45 1.827v.75M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9 5.25h.008v.008H12v-.008z" /></svg>
            使用说明
          </button>
          {templates.length > 0 && (
            <select
              className="text-xs border border-gray-200 rounded px-2 py-1 bg-white text-gray-600 focus:outline-none focus:ring-1 focus:ring-indigo-300"
              defaultValue=""
              onChange={(e) => { if (e.target.value) { onChange(e.target.value); e.target.value = ''; } }}
            >
              <option value="">插入模板…</option>
              {templates.map((t) => (
                <option key={t.label} value={t.value}>{t.label}</option>
              ))}
            </select>
          )}
        </div>
      </div>
      {showHelp && (
        <div className="mb-2 p-3 bg-gray-50 border border-gray-200 rounded-lg text-xs text-gray-600 space-y-1.5">
          {isExplore ? (
            <>
              <p className="font-medium text-gray-700">Explore 规则约束 — 工单优先级分类</p>
              <p>每行一条规则，格式：<code className="font-mono bg-white px-1 border border-gray-200 rounded">P&lt;数字&gt;: 关键词1, 关键词2, ...</code></p>
              <p>匹配范围（大小写不敏感）：<strong>场景标题</strong> + <strong>错误类型</strong> + <strong>错误信息</strong>，按顺序匹配，第一条命中生效。</p>
              <p className="text-gray-500">错误类型固定为：<code className="font-mono bg-white px-1 border border-gray-200 rounded">crash</code> / <code className="font-mono bg-white px-1 border border-gray-200 rounded">timeout</code> / <code className="font-mono bg-white px-1 border border-gray-200 rounded">assertion</code> / <code className="font-mono bg-white px-1 border border-gray-200 rounded">console_error</code></p>
              <pre className="bg-white border border-gray-200 rounded p-2 font-mono text-xs leading-relaxed">{`P1: crash, 无法登录, 支付失败\nP2: assertion, console_error, 核心功能\nP3: timeout, 加载慢`}</pre>
            </>
          ) : (
            <>
              <p className="font-medium text-gray-700">规则约束 — 追加约束 + 控制指令</p>
              <p>纯文本，追加到 Prompt 末尾（<code className="font-mono bg-white px-1 border border-gray-200 rounded">## Additional Rules</code> 段）。</p>
              <p>可写任意约束，也支持以下 <strong>Fix Agent 专属指令</strong>（不会出现在 Prompt 中）：</p>
              <pre className="bg-white border border-gray-200 rounded p-2 font-mono text-xs leading-relaxed">{`MAX_PRIORITY: 2   # 只修复 P1/P2，P3/P4 跳过\nMAX_ATTEMPTS: 3   # N 次失败后标记 needs-human`}</pre>
            </>
          )}
        </div>
      )}
      <MdEditor value={value} onChange={onChange} rows={isExplore ? 5 : 6} />
    </div>
  );
}


function WebhookTokenSection({
  projectId, tokens, onAdd, onRemove, onConfirm,
}: {
  projectId: string;
  tokens: { id: string; masked: string }[];
  onAdd: (t: { id: string; masked: string }) => void;
  onRemove: (id: string) => void;
  onConfirm: (d: ConfirmDialogProps) => void;
}) {
  const [adding, setAdding] = useState(false);
  const [newToken, setNewToken] = useState<string | null>(null); // shown once only
  const [copied, setCopied] = useState(false);
  const [tokenError, setTokenError] = useState('');

  const copy = (text: string) => {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };

  const handleAdd = async () => {
    setAdding(true);
    try {
      const res = await apiFetch<{ data: { new_token: string; webhook_tokens: { id: string; masked: string }[] } }>(
        `/api/v1/projects/${projectId}/webhook-tokens`, { method: 'POST' }
      );
      const { new_token, webhook_tokens } = res.data;
      const added = webhook_tokens[webhook_tokens.length - 1];
      onAdd(added);
      setNewToken(new_token);
      setCopied(false);
    } catch (e) {
      setTokenError(e instanceof Error ? e.message : '操作失败');
    } finally {
      setAdding(false);
    }
  };

  const handleRemove = (tokenId: string) => {
    onConfirm({
      title: '删除 Token',
      message: '删除后使用该 Token 的外部系统将无法触发 Agent，确认继续？',
      confirmText: '删除',
      danger: true,
      onConfirm: async () => {
        try {
          await apiFetch(`/api/v1/projects/${projectId}/webhook-tokens/${tokenId}`, { method: 'DELETE' });
          onRemove(tokenId);
        } catch (e) {
          setTokenError(e instanceof Error ? e.message : '操作失败');
        }
      },
    });
  };

  const curlExample = (token: string) =>
    `curl -X POST https://dapp.predict.kim/webhook/projects/${projectId}/trigger \\\n  -H "X-Webhook-Token: ${token}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"alias":"fix"}'`;

  return (
    <div className="mt-6 pt-5 border-t border-gray-100 space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h3 className="text-sm font-medium text-gray-700">Webhook Tokens</h3>
          <p className="text-xs text-gray-400 mt-0.5">供外部系统（CI、脚本、GitHub Actions）通过 alias 触发 Agent</p>
        </div>
        <button
          type="button"
          onClick={handleAdd}
          disabled={adding}
          className="flex-shrink-0 flex items-center gap-1.5 text-xs text-indigo-600 hover:text-indigo-800 border border-indigo-200 rounded-lg px-3 py-1.5 disabled:opacity-50 transition-colors"
        >
          {adding ? '生成中…' : (
            <>
              <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
              </svg>
              生成新 Token
            </>
          )}
        </button>
      </div>

      {tokenError && (
        <p className="text-xs text-red-500">{tokenError}</p>
      )}

      {/* One-time reveal panel — shown immediately after creation */}
      {newToken && (
        <div className="rounded-xl border-2 border-amber-300 bg-amber-50 overflow-hidden">
          <div className="flex items-center gap-2 px-4 py-2.5 bg-amber-100 border-b border-amber-200">
            <svg className="w-4 h-4 text-amber-600 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
            </svg>
            <span className="text-xs font-semibold text-amber-800">请立即复制 Token — 关闭后将永久隐藏，无法再次查看</span>
          </div>
          <div className="px-4 py-3 space-y-3">
            <div className="flex items-center gap-2">
              <code className="flex-1 text-sm font-mono text-gray-800 bg-white border border-gray-200 rounded px-3 py-2 break-all select-all">{newToken}</code>
              <button
                type="button"
                onClick={() => copy(newToken)}
                className={`flex-shrink-0 text-xs font-medium px-3 py-2 rounded border transition-all ${copied ? 'bg-green-50 text-green-700 border-green-300' : 'bg-white text-gray-700 border-gray-300 hover:border-gray-500'}`}
              >
                {copied ? '✓ 已复制' : '复制'}
              </button>
            </div>
            <div className="bg-gray-900 rounded-lg px-3 py-2.5 relative">
              <pre className="text-xs text-green-300 font-mono whitespace-pre-wrap break-all leading-relaxed pr-10">{curlExample(newToken)}</pre>
            </div>
            <p className="text-xs text-gray-500">将 <code className="font-mono">fix</code> 替换为任意 agent alias 即可触发对应 Agent。</p>
            <div className="flex justify-end pt-1">
              <button
                type="button"
                onClick={() => setNewToken(null)}
                className="text-xs px-4 py-1.5 bg-amber-500 hover:bg-amber-600 text-white rounded-lg transition-colors font-medium"
              >
                已保存，关闭
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Existing tokens — masked only, no copy */}
      {tokens.length === 0 ? (
        !newToken && <p className="text-xs text-gray-400">暂无 Token，点击「生成新 Token」创建。</p>
      ) : (
        <div className="space-y-2">
          {tokens.map((token) => (
            <div key={token.id} className="flex items-center gap-2 px-3 py-2.5 bg-gray-50 rounded-lg border border-gray-200">
              <code className="flex-1 text-xs font-mono text-gray-500">{token.masked}</code>
              <button
                type="button"
                onClick={() => handleRemove(token.id)}
                className="flex-shrink-0 text-xs text-red-400 hover:text-red-600 border border-red-100 bg-white rounded px-2.5 py-1 transition-colors"
              >
                删除
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function DeployKeyCard({ projectId }: { projectId: string }) {
  const [info, setInfo] = useState<{ public_key: string; registered: boolean; deploy_key_id: number | null } | null>(null);
  const [loading, setLoading] = useState(true);
  const [registering, setRegistering] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    apiFetch<{ data: { public_key: string; registered: boolean; deploy_key_id: number | null } }>(`/api/v1/projects/${projectId}/deploy-key`)
      .then((r) => setInfo(r.data))
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)))
      .finally(() => setLoading(false));
  }, [projectId]);

  const handleRegister = async () => {
    setRegistering(true);
    setError('');
    try {
      const r = await apiFetch<{ data: { public_key: string; deploy_key_id: number } }>(`/api/v1/projects/${projectId}/deploy-key/register`, { method: 'POST' });
      setInfo({ public_key: r.data.public_key, registered: true, deploy_key_id: r.data.deploy_key_id });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setRegistering(false);
    }
  };

  const handleConfirm = async () => {
    setConfirming(true);
    setError('');
    try {
      await apiFetch(`/api/v1/projects/${projectId}/deploy-key/confirm`, { method: 'POST' });
      setInfo((prev) => prev ? { ...prev, registered: true } : prev);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setConfirming(false);
    }
  };

  const copy = (text: string) => {
    navigator.clipboard.writeText(text).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); });
  };

  return (
    <div className="mt-5 pt-5 border-t border-gray-100">
      <div className="flex items-center justify-between mb-3">
        <div>
          <h3 className="text-sm font-medium text-gray-700">Deploy Key</h3>
          <p className="text-xs text-gray-400 mt-0.5">Fix Agent 通过此 SSH 公钥 clone / push 仓库</p>
        </div>
        {!loading && info && (
          <span className={`inline-flex items-center gap-1.5 text-xs font-medium px-2.5 py-1 rounded-full ${info.registered ? 'text-green-700 bg-green-50 border border-green-200' : 'text-red-600 bg-red-50 border border-red-200'}`}>
            <span className={`w-1.5 h-1.5 rounded-full ${info.registered ? 'bg-green-500' : 'bg-red-500'}`} />
            {info.registered ? '已注册' : '未注册'}
          </span>
        )}
      </div>

      {loading && <p className="text-xs text-gray-400">加载中…</p>}
      {error && <p className="text-xs text-red-500">{error}</p>}

      {info && (
        <div className="space-y-3">
          <div className="flex items-center gap-2">
            <code className="flex-1 px-3 py-2 bg-gray-50 border border-gray-200 rounded text-xs font-mono text-gray-600 truncate">{info.public_key}</code>
            <button type="button" onClick={() => copy(info.public_key)} className="flex-shrink-0 text-xs text-indigo-600 hover:text-indigo-800 border border-indigo-200 rounded px-3 py-2">
              {copied ? '已复制' : '复制'}
            </button>
          </div>
          {!info.registered && (
            <div className="rounded-lg bg-amber-50 border border-amber-200 px-4 py-3 text-sm text-amber-800 space-y-2">
              <p className="font-medium">Deploy Key 未注册，Fix Agent 无法 clone 仓库</p>
              <p className="text-xs">点击「注册到 GitHub」自动添加（需要 PAT 有 Administration 权限），或手动将上方公钥添加到 GitHub → Settings → Deploy keys（需勾选「Allow write access」），添加后点击「已手动添加」。</p>
            </div>
          )}
          <div className="flex items-center gap-2 flex-wrap">
            <button
              type="button"
              onClick={handleRegister}
              disabled={registering || confirming}
              className="flex items-center gap-2 text-xs text-white bg-indigo-500 hover:bg-indigo-600 disabled:opacity-50 rounded-lg px-4 py-2 transition-colors"
            >
              {registering ? '注册中…' : info.registered ? '重新注册' : '注册到 GitHub'}
            </button>
            {!info.registered && (
              <button
                type="button"
                onClick={handleConfirm}
                disabled={confirming || registering}
                className="flex items-center gap-2 text-xs text-gray-700 bg-white hover:bg-gray-50 disabled:opacity-50 border border-gray-300 rounded-lg px-4 py-2 transition-colors"
              >
                {confirming ? '确认中…' : '已手动添加'}
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function MdEditor({ value, onChange, rows = 8 }: { value: string; onChange: (v: string) => void; rows?: number }) {
  const minH = `${rows * 1.5}rem`;
  return (
    <div className="rounded-lg border border-gray-200 overflow-hidden flex" style={{ minHeight: minH }}>
      <textarea
        className="w-1/2 px-3 py-2.5 text-sm font-mono resize-none focus:outline-none focus:ring-2 focus:ring-inset focus:ring-indigo-400 border-r border-gray-200"
        style={{ minHeight: minH }}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="支持 Markdown 格式…"
        spellCheck={false}
      />
      <div
        className="w-1/2 px-4 py-3 prose prose-sm max-w-none overflow-y-auto text-gray-700 bg-gray-50"
        style={{ minHeight: minH }}
      >
        {value ? (
          <ReactMarkdown>{value}</ReactMarkdown>
        ) : (
          <p className="text-gray-400 italic text-xs">预览…</p>
        )}
      </div>
    </div>
  );
}

type PromptFieldProps = {
  label: string;
  hint: React.ReactNode;
  value: string;
  onChange: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
  onBlur: () => void;
  validation: ValidationState | undefined;
  defaultContent?: string;
  expanded: boolean;
  onToggleExpand: () => void;
};

function PromptField({ label, hint, value, onChange, onBlur, validation, defaultContent, expanded, onToggleExpand }: PromptFieldProps) {
  return (
    <div className="space-y-1.5">
      <Field label={label} hint={hint}>
        <textarea
          className={inputClass + ' font-mono text-xs h-32 resize-y'}
          value={value}
          onChange={onChange}
          onBlur={onBlur}
          placeholder="留空使用内置 Prompt"
          spellCheck={false}
        />
      </Field>

      {/* Validation status */}
      {validation === 'loading' && (
        <p className="text-xs text-gray-400">验证中...</p>
      )}
      {validation != null && validation !== 'loading' && (
        <p className={`text-xs ${validation.valid ? 'text-green-600' : 'text-red-500'}`}>
          {validation.valid ? '✓ 模版语法正确' : `✗ ${validation.error}`}
        </p>
      )}

      {/* Default template toggle */}
      {defaultContent && (
        <div>
          <button
            type="button"
            onClick={onToggleExpand}
            className="text-xs text-blue-500 hover:text-blue-700 flex items-center gap-1"
          >
            <span className={`inline-block transition-transform ${expanded ? 'rotate-90' : ''}`}>▶</span>
            {expanded ? '收起内置模版' : '查看内置模版'}
          </button>
          {expanded && (
            <pre className="mt-2 p-3 bg-gray-50 border border-gray-200 rounded-lg text-xs font-mono text-gray-600 whitespace-pre-wrap overflow-x-auto max-h-64 overflow-y-auto">
              {defaultContent}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

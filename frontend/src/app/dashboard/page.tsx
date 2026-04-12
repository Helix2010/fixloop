'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import AuthGuard from '@/components/AuthGuard';
import { Spinner, Field, inputClass } from '@/components/ui';
import { apiFetch, ApiError } from '@/lib/api';
import type {
  User,
  Project,
  Notification,
  PagedResponse,
} from '@/lib/types';

export default function DashboardPage() {
  return (
    <AuthGuard>
      {(user) => <DashboardContent user={user} />}
    </AuthGuard>
  );
}

function DashboardContent({ user }: { user: User }) {
  const [projects, setProjects] = useState<Project[]>([]);
  const [unreadCount, setUnreadCount] = useState(0);
  const [showCreate, setShowCreate] = useState(false);
  const [loading, setLoading] = useState(true);
  const [tgBound, setTgBound] = useState(!!user.tg_chat_id);
  const [showTgMenu, setShowTgMenu] = useState(false);
  const [tgLoading, setTgLoading] = useState(false);
  const [tgUrl, setTgUrl] = useState('');
  const [tgError, setTgError] = useState('');
  const tgMenuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!showTgMenu) return;
    const handler = (e: MouseEvent) => {
      if (tgMenuRef.current && !tgMenuRef.current.contains(e.target as Node)) {
        setShowTgMenu(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [showTgMenu]);

  const handleTGBind = async () => {
    setTgLoading(true);
    setTgUrl('');
    setTgError('');
    try {
      const res = await apiFetch<{ data: { tg_url: string } }>('/api/v1/me/tg-bind', { method: 'POST' });
      setTgUrl(res.data.tg_url);
      window.open(res.data.tg_url, '_blank');
      setTgBound(true);
    } catch (err) {
      setTgError(err instanceof ApiError ? err.message : '请求失败，请稍后重试');
    } finally {
      setTgLoading(false);
    }
  };

  const fetchData = useCallback(async () => {
    try {
      const [projectsRes, notifRes] = await Promise.all([
        apiFetch<PagedResponse<Project>>('/api/v1/projects'),
        apiFetch<PagedResponse<Notification>>('/api/v1/notifications?per_page=50'),
      ]);
      setProjects(projectsRes.data);
      setUnreadCount(notifRes.data.filter((n) => !n.read_at).length);
    } catch {
      // apiFetch handles 401; ignore other errors on poll
    }
  }, []);

  useEffect(() => {
    fetchData().finally(() => setLoading(false));
    const id = setInterval(fetchData, 30_000);
    return () => clearInterval(id);
  }, [fetchData]);

  const statusIcon = (status: string) => {
    if (status === 'active') return '🟢';
    if (status === 'paused') return '⏸';
    return '🔴';
  };

  const handleToggleStatus = async (e: React.MouseEvent, p: Project) => {
    e.preventDefault();
    e.stopPropagation();
    const action = p.status === 'active' ? 'pause' : 'resume';
    try {
      await apiFetch(`/api/v1/projects/${p.id}/${action}`, { method: 'POST' });
      setProjects((prev) =>
        prev.map((x) => x.id === p.id ? { ...x, status: action === 'pause' ? 'paused' : 'active' } : x)
      );
    } catch {
      // ignore
    }
  };

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-5xl mx-auto px-6 py-4 flex items-center justify-between">
          <Link href="/dashboard" className="text-xl font-bold">FixLoop</Link>
          <div className="flex items-center gap-4">
            <button className="relative text-gray-500 hover:text-gray-700" title="通知">
              🔔
              {unreadCount > 0 && (
                <span className="absolute -top-1 -right-1 bg-red-500 text-white text-xs rounded-full w-4 h-4 flex items-center justify-center font-bold">
                  {unreadCount > 9 ? '9+' : unreadCount}
                </span>
              )}
            </button>

            {/* TG 绑定入口 */}
            <div className="relative" ref={tgMenuRef}>
              <button
                onClick={() => setShowTgMenu((v) => !v)}
                title={tgBound ? 'Telegram 已绑定' : '绑定 Telegram 通知'}
                className="text-lg leading-none"
              >
                {tgBound ? '📱' : '🔕'}
              </button>
              {showTgMenu && (
                <div className="absolute right-0 top-8 w-72 bg-white border border-gray-200 rounded-xl shadow-lg p-4 z-50">
                  <p className="text-sm font-semibold text-gray-800 mb-1">Telegram 通知</p>
                  <p className="text-xs text-gray-500 mb-3">
                    {tgBound ? '已绑定，所有项目事件将推送到你的 Telegram。' : '绑定后，修复完成、PR 合并等事件将推送到你的 Telegram。'}
                  </p>
                  <button
                    onClick={handleTGBind}
                    disabled={tgLoading}
                    className="w-full bg-blue-500 hover:bg-blue-600 disabled:opacity-50 text-white py-2 rounded-lg text-sm font-medium transition-colors"
                  >
                    {tgLoading ? '生成中...' : tgBound ? '重新绑定' : '获取绑定链接'}
                  </button>
                  {tgError && (
                    <p className="mt-2 text-xs text-red-500">{tgError}</p>
                  )}
                  {tgUrl && (
                    <p className="mt-2 text-xs text-gray-500 break-all">
                      <a href={tgUrl} target="_blank" rel="noopener noreferrer" className="text-blue-500 underline">{tgUrl}</a>
                    </p>
                  )}
                </div>
              )}
            </div>

            <Link href="/admin" title="系统设置" className="text-gray-400 hover:text-gray-600 text-lg leading-none">⚙️</Link>
            <span className="text-sm text-gray-600">{user.github_login}</span>
          </div>
        </div>
      </header>

      <main className="max-w-5xl mx-auto px-6 py-8">
        <div className="flex items-center justify-between mb-6">
          <h2 className="text-2xl font-bold">我的项目</h2>
          <button
            onClick={() => setShowCreate(true)}
            className="bg-blue-500 hover:bg-blue-600 text-white px-4 py-2 rounded-lg text-sm font-semibold transition-colors"
          >
            + 新建项目
          </button>
        </div>

        {loading ? (
          <Spinner />
        ) : projects.length === 0 ? (
          <div className="text-center py-20 text-gray-500">
            <p className="text-lg mb-2">还没有项目</p>
            <p className="text-sm mb-6">接入一个 GitHub 仓库，让 AI 自动发现并修复问题</p>
            <button
              onClick={() => setShowCreate(true)}
              className="bg-blue-500 text-white px-6 py-3 rounded-lg font-semibold hover:bg-blue-600 transition-colors"
            >
              创建第一个项目
            </button>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {projects.map((p) => (
              <div key={p.id} className="relative bg-white rounded-xl border border-gray-200 hover:shadow-md transition-shadow">
                <Link href={`/projects/${p.id}`} className="block p-5">
                  <div className="flex items-center justify-between mb-2">
                    <span className="font-semibold">{p.name}</span>
                    <span className="w-6" />
                  </div>
                  <p className="text-sm text-gray-500">
                    {p.github.owner}/{p.github.repo}
                  </p>
                  {p.test?.staging_url && (
                    <p className="text-xs text-blue-500 mt-1 truncate">{p.test.staging_url}</p>
                  )}
                  <div className="flex gap-3 mt-3">
                    <Link
                      href={`/projects/${p.id}/settings`}
                      onClick={(e) => e.stopPropagation()}
                      className="text-xs text-gray-400 hover:text-gray-600"
                    >
                      设置
                    </Link>
                  </div>
                </Link>
                {p.status !== 'error' && (
                  <button
                    onClick={(e) => handleToggleStatus(e, p)}
                    title={p.status === 'active' ? '点击暂停' : '点击恢复'}
                    className={`absolute top-4 right-4 text-xs font-medium px-2.5 py-1 rounded-full border transition-colors ${
                      p.status === 'active'
                        ? 'bg-green-50 border-green-200 text-green-700 hover:bg-red-50 hover:border-red-200 hover:text-red-600'
                        : 'bg-yellow-50 border-yellow-200 text-yellow-700 hover:bg-green-50 hover:border-green-200 hover:text-green-700'
                    }`}
                  >
                    {p.status === 'active' ? '运行中' : '已暂停'}
                  </button>
                )}
                {p.status === 'error' && (
                  <span className="absolute top-4 right-4 text-xs font-medium px-2.5 py-1 rounded-full bg-red-50 border border-red-200 text-red-600">
                    异常
                  </span>
                )}
              </div>
            ))}
          </div>
        )}
      </main>

      <CreateProjectDrawer
        open={showCreate}
        onClose={() => setShowCreate(false)}
        onCreated={() => { setShowCreate(false); fetchData(); }}
      />
    </div>
  );
}

// ----------------------------------------------------------------
// 新建项目侧边抽屉
// ----------------------------------------------------------------

interface DrawerProps {
  open: boolean;
  onClose: () => void;
  onCreated: () => void;
}

function CreateProjectDrawer({ open, onClose, onCreated }: DrawerProps) {
  const [form, setForm] = useState({
    name: '',
    github_owner: '',
    github_repo: '',
    github_pat: '',
    fix_base_branch: 'main',
    staging_url: '',
    ai_runner: 'claude',
    ai_model: 'claude-opus-4-6',
    ai_auth: 'cli' as 'cli' | 'apikey',
    ai_api_base: '',
    ai_api_key: '',
  });
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    const orig = document.body.style.overflow;
    document.body.style.overflow = open ? 'hidden' : orig;
    return () => { document.body.style.overflow = orig; };
  }, [open]);

  // 关闭时重置表单
  const handleClose = () => {
    setError('');
    onClose();
  };

  const set =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((f) => ({ ...f, [k]: e.target.value }));

  // 切换引擎时自动填充默认模型
  const handleRunnerChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    const runner = e.target.value;
    const defaultModel: Record<string, string> = {
      claude: 'claude-opus-4-6',
      aider: '',
    };
    setForm((f) => ({ ...f, ai_runner: runner, ai_model: defaultModel[runner] ?? '', ai_auth: runner === 'aider' ? 'apikey' : 'cli', ai_api_base: '', ai_api_key: '' }));
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    try {
      await apiFetch('/api/v1/projects', {
        method: 'POST',
        body: JSON.stringify({
          name: form.name,
          github: {
            owner: form.github_owner,
            repo: form.github_repo,
            pat: form.github_pat,
            fix_base_branch: form.fix_base_branch || 'main',
          },
          test: { staging_url: form.staging_url },
          ai_runner: form.ai_runner,
          ai_model: form.ai_model,
          ...(form.ai_runner === 'aider' && {
            ai_api_base: form.ai_api_base,
            ai_api_key: form.ai_api_key,
          }),
          ...(form.ai_runner === 'claude' && form.ai_auth === 'apikey' && {
            ai_api_key: form.ai_api_key,
          }),
        }),
      });
      onCreated();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  if (!open) return null;

  return (
    <>
      {/* 遮罩层 */}
      <div
        className="fixed inset-0 bg-black/40 z-40"
        onClick={handleClose}
      />

      {/* 抽屉主体 */}
      <div className="fixed inset-y-0 right-0 w-full max-w-[480px] bg-white z-50 flex flex-col shadow-2xl">

        {/* 顶部标题栏 */}
        <div className="flex items-start justify-between px-6 py-5 border-b border-gray-200 flex-shrink-0">
          <div>
            <h3 className="text-lg font-bold text-gray-900">新建项目</h3>
            <p className="text-sm text-gray-500 mt-0.5">接入 GitHub 仓库，开启 AI 自动化闭环</p>
          </div>
          <button
            onClick={handleClose}
            className="text-gray-400 hover:text-gray-600 text-xl mt-0.5 ml-4 flex-shrink-0"
          >
            ✕
          </button>
        </div>

        {/* 表单主体（可滚动） */}
        <form onSubmit={handleSubmit} className="flex-1 overflow-y-auto">
          <div className="px-6 py-6 space-y-8">

            {/* 一、基本信息 */}
            <section>
              <SectionHeader
                title="基本信息"
                desc="项目在 FixLoop 中的标识"
              />
              <div className="space-y-4">
                <Field
                  label="项目名称"
                  required
                  hint="FixLoop 控制台内的显示名称，建议与仓库名保持一致"
                >
                  <input
                    className={inputClass}
                    value={form.name}
                    onChange={set('name')}
                    required
                    placeholder="my-app"
                    autoFocus
                  />
                </Field>
              </div>
            </section>

            {/* 二、GitHub 仓库 */}
            <section>
              <SectionHeader
                title="GitHub 仓库"
                desc="FixLoop 将在此仓库上提交修复 PR、创建 Issue"
              />
              <div className="space-y-4">
                <div className="grid grid-cols-2 gap-3">
                  <Field
                    label="所有者"
                    required
                    hint="GitHub 用户名或组织名"
                  >
                    <input
                      className={inputClass}
                      value={form.github_owner}
                      onChange={set('github_owner')}
                      required
                      placeholder="myorg"
                    />
                  </Field>
                  <Field
                    label="仓库名"
                    required
                    hint="不含所有者前缀"
                  >
                    <input
                      className={inputClass}
                      value={form.github_repo}
                      onChange={set('github_repo')}
                      required
                      placeholder="my-app"
                    />
                  </Field>
                </div>

                <Field
                  label="精细访问令牌（Fine-grained PAT）"
                  required
                  hint={
                    <>
                      需要以下权限：<strong>Issues 读写</strong>、<strong>Pull requests 读写</strong>、<strong>Contents 读写</strong>。
                      前往 GitHub →&nbsp;
                      <a
                        href="https://github.com/settings/tokens?type=beta"
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-blue-500 underline"
                      >
                        Settings → Fine-grained tokens
                      </a>
                      &nbsp;生成，有效期建议 1 年。
                    </>
                  }
                >
                  <input
                    className={inputClass}
                    type="password"
                    value={form.github_pat}
                    onChange={set('github_pat')}
                    required
                    placeholder="github_pat_..."
                  />
                </Field>

                <Field
                  label="修复基础分支"
                  hint="AI 生成的修复 PR 将从此分支切出，通常为 main 或 dev"
                >
                  <input
                    className={inputClass}
                    value={form.fix_base_branch}
                    onChange={set('fix_base_branch')}
                    placeholder="main"
                  />
                </Field>
              </div>
            </section>

            {/* 三、测试环境 */}
            <section>
              <SectionHeader
                title="测试环境"
                desc="Explore Agent 将访问此地址进行自动化 UI 测试"
              />
              <div className="space-y-4">
                <Field
                  label="Staging 地址"
                  hint="需可公开访问（或内网可达）。留空则跳过 UI 探索，仅执行代码层修复。"
                >
                  <input
                    className={inputClass}
                    value={form.staging_url}
                    onChange={set('staging_url')}
                    placeholder="https://staging.example.com"
                  />
                </Field>
              </div>
            </section>

            {/* 四、AI 引擎 */}
            <section>
              <SectionHeader
                title="AI 引擎"
                desc="用于分析问题、生成修复代码的 AI 引擎"
              />
              <div className="space-y-4">
                <div className="grid grid-cols-2 gap-3">
                  <Field label="引擎">
                    <select
                      className={inputClass}
                      value={form.ai_runner}
                      onChange={handleRunnerChange}
                    >
                      <option value="claude">Claude（推荐）</option>
                      <option value="aider">Aider</option>
                    </select>
                  </Field>
                  <Field
                    label="模型"
                    hint={
                      form.ai_runner === 'claude'
                        ? '留空则使用默认模型 claude-opus-4-6'
                        : '填写 OpenAI 兼容的模型名'
                    }
                  >
                    <input
                      className={inputClass}
                      value={form.ai_model}
                      onChange={set('ai_model')}
                      placeholder={form.ai_runner === 'claude' ? 'claude-opus-4-6' : 'gpt-4o'}
                    />
                  </Field>
                </div>

                {form.ai_runner === 'claude' && (
                  <div className="space-y-3">
                    <Field label="认证方式" hint="选择 Claude 的调用方式">
                      <div className="flex rounded-lg border border-gray-300 overflow-hidden text-sm">
                        <button
                          type="button"
                          onClick={() => setForm((f) => ({ ...f, ai_auth: 'cli', ai_api_key: '' }))}
                          className={`flex-1 py-2 px-3 text-center transition-colors ${form.ai_auth === 'cli' ? 'bg-blue-500 text-white font-medium' : 'text-gray-600 hover:bg-gray-50'}`}
                        >
                          服务器已登录 CLI
                        </button>
                        <button
                          type="button"
                          onClick={() => setForm((f) => ({ ...f, ai_auth: 'apikey' }))}
                          className={`flex-1 py-2 px-3 text-center border-l border-gray-300 transition-colors ${form.ai_auth === 'apikey' ? 'bg-blue-500 text-white font-medium' : 'text-gray-600 hover:bg-gray-50'}`}
                        >
                          自带 API 密钥
                        </button>
                      </div>
                    </Field>

                    {form.ai_auth === 'cli' ? (
                      <div className="rounded-lg bg-blue-50 border border-blue-100 px-4 py-3 text-sm text-blue-700">
                        使用服务器上已登录的 <code className="font-mono">claude</code> CLI，无需填写 API Key。
                      </div>
                    ) : (
                      <Field
                        label="Anthropic API 密钥"
                        required
                        hint={<>前往 <a href="https://console.anthropic.com/settings/keys" target="_blank" rel="noopener noreferrer" className="text-blue-500 underline">console.anthropic.com</a> 获取，加密存储</>}
                      >
                        <input
                          className={inputClass}
                          type="password"
                          value={form.ai_api_key}
                          onChange={set('ai_api_key')}
                          required
                          placeholder="sk-ant-..."
                        />
                      </Field>
                    )}
                  </div>
                )}

                {form.ai_runner === 'aider' && (
                  <>
                    <Field
                      label="API 基础地址"
                      hint="OpenAI 兼容接口地址，例如 DeepSeek、Qwen、Kimi 等"
                    >
                      <input
                        className={inputClass}
                        value={form.ai_api_base}
                        onChange={set('ai_api_base')}
                        placeholder="https://api.deepseek.com/v1"
                      />
                    </Field>
                    <Field
                      label="API 密钥"
                      required
                      hint="对应服务商的 API Key，加密存储"
                    >
                      <input
                        className={inputClass}
                        type="password"
                        value={form.ai_api_key}
                        onChange={set('ai_api_key')}
                        required
                        placeholder="sk-..."
                      />
                    </Field>
                  </>
                )}
              </div>
            </section>

          </div>
        </form>

        {/* 底部操作栏 */}
        <div className="flex-shrink-0 border-t border-gray-200 px-6 py-4 bg-white">
          {error && <p className="text-red-500 text-sm mb-3">{error}</p>}
          <div className="flex gap-3">
            <button
              type="button"
              onClick={handleClose}
              className="flex-1 border border-gray-300 text-gray-700 py-2.5 rounded-lg text-sm font-medium hover:bg-gray-50 transition-colors"
            >
              取消
            </button>
            <button
              type="submit"
              form="create-project-form"
              disabled={loading}
              onClick={handleSubmit}
              className="flex-1 bg-blue-500 hover:bg-blue-600 disabled:opacity-50 text-white py-2.5 rounded-lg text-sm font-semibold transition-colors"
            >
              {loading ? '创建中...' : '创建项目'}
            </button>
          </div>
        </div>

      </div>
    </>
  );
}

function SectionHeader({ title, desc }: { title: string; desc: string }) {
  return (
    <div className="mb-4 pb-2 border-b border-gray-100">
      <h4 className="text-sm font-semibold text-gray-900">{title}</h4>
      <p className="text-xs text-gray-400 mt-0.5">{desc}</p>
    </div>
  );
}

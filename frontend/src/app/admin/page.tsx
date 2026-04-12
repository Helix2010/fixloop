'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import AuthGuard from '@/components/AuthGuard';
import { Field, inputClass } from '@/components/ui';
import { apiFetch, ApiError } from '@/lib/api';
import { fmtBytes } from '@/lib/utils';
import type { User } from '@/lib/types';

export default function AdminPage() {
  return <AuthGuard>{(user) => <AdminSettings user={user} />}</AuthGuard>;
}

// bot_token format: digits:35+ alphanumeric chars
const TOKEN_RE = /^\d+:[A-Za-z0-9_-]{35,}$/;

function validateForm(form: { bot_token: string; bot_username: string }): string {
  if (form.bot_token && !TOKEN_RE.test(form.bot_token)) {
    return 'Token 格式不正确，应为 123456789:AAH... 格式';
  }
  if (form.bot_username && form.bot_username.startsWith('@')) {
    return '用户名不需要 @ 前缀';
  }
  return '';
}

function AdminSettings({ user: _user }: { user: User }) {
  const [configured, setConfigured] = useState(false);
  const [form, setForm] = useState({ bot_token: '', bot_username: '' });
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');
  const [verifying, setVerifying] = useState(false);
  const [verifyResult, setVerifyResult] = useState<{ ok: boolean; name?: string; username?: string; msg?: string } | null>(null);

  useEffect(() => {
    apiFetch<{ data: { configured: boolean; bot_username: string } }>('/api/v1/admin/tg-config')
      .then((r) => {
        setConfigured(r.data.configured);
        setForm((f) => ({ ...f, bot_username: r.data.bot_username }));
      })
      .catch(() => setError('加载配置失败，请刷新重试'));
  }, []);

  const handleVerify = async () => {
    const fmtErr = validateForm(form);
    if (fmtErr) { setError(fmtErr); return; }
    if (!form.bot_token) { setError('请先填写 Bot Token'); return; }
    setVerifying(true);
    setVerifyResult(null);
    setError('');
    try {
      const r = await apiFetch<{ data: { bot_name: string; bot_username: string } }>(
        '/api/v1/admin/tg-config/verify',
        { method: 'POST', body: JSON.stringify({ bot_token: form.bot_token }) },
      );
      setVerifyResult({ ok: true, name: r.data.bot_name, username: r.data.bot_username });
      setForm((f) => ({ ...f, bot_username: f.bot_username || r.data.bot_username }));
    } catch (err) {
      setVerifyResult({ ok: false, msg: err instanceof ApiError ? err.message : String(err) });
    } finally {
      setVerifying(false);
    }
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    const fmtErr = validateForm(form);
    if (fmtErr) { setError(fmtErr); return; }
    if (!form.bot_token && !form.bot_username) return;
    setSaving(true);
    setError('');
    setSaved(false);
    try {
      await apiFetch('/api/v1/admin/tg-config', {
        method: 'PATCH',
        body: JSON.stringify({
          ...(form.bot_token && { bot_token: form.bot_token }),
          ...(form.bot_username && { bot_username: form.bot_username }),
        }),
      });
      setSaved(true);
      setConfigured(true);
      setForm((f) => ({ ...f, bot_token: '' }));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-2xl mx-auto px-6 py-4 flex items-center gap-3">
          <Link href="/dashboard" className="text-gray-400 hover:text-gray-600 text-sm">← 返回</Link>
          <span className="text-gray-300">/</span>
          <span className="font-semibold">系统设置</span>
        </div>
      </header>

      <main className="max-w-2xl mx-auto px-6 py-8 space-y-6">
        {/* Telegram Bot 配置 */}
        <section className="bg-white rounded-xl border border-gray-200 p-6">
          <div className="flex items-center justify-between mb-5">
            <div>
              <h2 className="text-lg font-semibold">Telegram Bot</h2>
              <p className="text-sm text-gray-500 mt-0.5">配置后所有用户可绑定 Telegram 接收通知</p>
            </div>
            {configured ? (
              <span className="inline-flex items-center gap-1.5 text-xs font-medium text-green-700 bg-green-50 border border-green-200 px-2.5 py-1 rounded-full">
                <span className="w-1.5 h-1.5 rounded-full bg-green-500" />
                已配置{form.bot_username ? `：@${form.bot_username}` : ''}
              </span>
            ) : (
              <span className="inline-flex items-center gap-1.5 text-xs font-medium text-gray-500 bg-gray-100 px-2.5 py-1 rounded-full">
                <span className="w-1.5 h-1.5 rounded-full bg-gray-400" />
                未配置
              </span>
            )}
          </div>

          <form onSubmit={handleSave} className="space-y-4">
            <Field
              label="Bot Token"
              hint="格式：123456789:AAH... 留空不修改现有 Token，加密存储"
            >
              <div className="flex gap-2">
                <input
                  className={inputClass + ' flex-1'}
                  type="password"
                  value={form.bot_token}
                  onChange={(e) => { setForm((f) => ({ ...f, bot_token: e.target.value })); setVerifyResult(null); }}
                  placeholder="••••••••（留空不修改）"
                />
                <button
                  type="button"
                  onClick={handleVerify}
                  disabled={verifying || !form.bot_token}
                  className="flex-shrink-0 border border-gray-300 text-gray-700 px-3 py-2 rounded-lg text-sm hover:bg-gray-50 disabled:opacity-40 transition-colors"
                >
                  {verifying ? '检测中…' : '验证'}
                </button>
              </div>
            </Field>

            {verifyResult && (
              <div className={`rounded-lg px-4 py-3 text-sm flex items-start gap-2 ${verifyResult.ok ? 'bg-green-50 border border-green-200 text-green-800' : 'bg-red-50 border border-red-200 text-red-700'}`}>
                <span>{verifyResult.ok ? '✅' : '❌'}</span>
                <span>
                  {verifyResult.ok
                    ? <>Token 有效。Bot 名称：<strong>{verifyResult.name}</strong>，用户名：<strong>@{verifyResult.username}</strong></>
                    : verifyResult.msg}
                </span>
              </div>
            )}

            <Field
              label="Bot 用户名"
              hint="不含 @ 符号，例如 fixloop_notify_bot；验证 Token 后自动填入"
            >
              <input
                className={inputClass}
                value={form.bot_username}
                onChange={(e) => setForm((f) => ({ ...f, bot_username: e.target.value }))}
                placeholder="fixloop_notify_bot"
              />
            </Field>

            {error && <p className="text-red-500 text-sm">{error}</p>}
            {saved && (
              <div className="rounded-lg bg-amber-50 border border-amber-200 px-4 py-3 text-sm text-amber-800">
                ✅ 已保存。<strong>需重启后端服务后生效：</strong>
                <code className="font-mono text-xs ml-1 bg-amber-100 px-1.5 py-0.5 rounded">
                  kill $(lsof -ti:8080) &amp;&amp; nohup /tmp/fixloop-server &gt; /tmp/fixloop-backend.log 2&gt;&amp;1 &amp;
                </code>
              </div>
            )}

            <button
              type="submit"
              disabled={saving || (!form.bot_token && !form.bot_username)}
              className="bg-blue-500 hover:bg-blue-600 disabled:opacity-40 text-white px-6 py-2.5 rounded-lg text-sm font-semibold transition-colors"
            >
              {saving ? '保存中...' : '保存配置'}
            </button>
          </form>
        </section>

        {/* 工作目录 */}
        <WorkspaceSection />

        {/* 创建 Bot 指引 */}
        <section className="bg-white rounded-xl border border-gray-200 p-6">
          <h2 className="text-base font-semibold mb-4">如何创建 Telegram Bot</h2>
          <ol className="space-y-4">
            <Step n={1} title="打开 BotFather">
              在 Telegram 中搜索{' '}
              <a
                href="https://t.me/BotFather"
                target="_blank"
                rel="noopener noreferrer"
                className="text-blue-500 underline font-medium"
              >
                @BotFather
              </a>{' '}
              并点击 <Kbd>START</Kbd>
            </Step>

            <Step n={2} title="创建新 Bot">
              发送命令：
              <CopyCmd>/newbot</CopyCmd>
            </Step>

            <Step n={3} title="输入展示名称">
              BotFather 会问：<Chat>Alright, a new bot. How are we going to call it?</Chat>
              输入你的 Bot 展示名，例如：<Chat dir="out">FixLoop Notify</Chat>
            </Step>

            <Step n={4} title="输入用户名">
              再问：<Chat>Good. Now let's choose a username for your bot.</Chat>
              输入用户名（必须以 <code className="font-mono text-xs bg-gray-100 px-1 rounded">bot</code> 结尾）：
              <Chat dir="out">fixloop_notify_bot</Chat>
            </Step>

            <Step n={5} title="复制 Token">
              BotFather 返回成功消息，其中包含：
              <Chat>
                Use this token to access the HTTP API:
                <br />
                <span className="font-mono text-green-700">123456789:AAHxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx</span>
              </Chat>
              复制该 Token，填入上方表单。
            </Step>

            <Step n={6} title="（可选）发送测试消息">
              在 Telegram 找到你的 Bot，点击 <Kbd>START</Kbd>，确认它能回复即可。
            </Step>
          </ol>
        </section>
      </main>
    </div>
  );
}

// ---- Workspace Section ----

interface WorkspaceInfo {
  path: string;
  exists: boolean;
  readable: boolean;
  writable: boolean;
  disk_total: number;
  disk_free: number;
  disk_used: number;
}



function WorkspaceSection() {
  const [info, setInfo] = useState<WorkspaceInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [initing, setIniting] = useState(false);
  const [error, setError] = useState('');

  const load = async () => {
    try {
      const r = await apiFetch<{ data: WorkspaceInfo }>('/api/v1/admin/workspace');
      setInfo(r.data);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, []);

  const handleInit = async () => {
    setIniting(true);
    setError('');
    try {
      const r = await apiFetch<{ data: WorkspaceInfo }>('/api/v1/admin/workspace/init', { method: 'POST' });
      setInfo(r.data);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setIniting(false);
    }
  };

  const diskUsedPct = info && info.disk_total > 0
    ? Math.round(((info.disk_total - info.disk_free) / info.disk_total) * 100)
    : 0;
  const dirUsedPct = info && info.disk_total > 0
    ? Math.min(100, Math.round((info.disk_used / info.disk_total) * 100))
    : 0;

  return (
    <section className="bg-white rounded-xl border border-gray-200 p-6">
      <div className="flex items-start justify-between mb-5">
        <div>
          <h2 className="text-lg font-semibold">工作目录</h2>
          <p className="text-sm text-gray-500 mt-0.5">Agent 克隆仓库、执行任务的本地存储根目录</p>
        </div>
        {info && (
          <StatusBadge ok={info.exists && info.readable && info.writable} />
        )}
      </div>

      {loading && <p className="text-sm text-gray-400">加载中…</p>}
      {error && <p className="text-sm text-red-500">{error}</p>}

      {info && (
        <div className="space-y-4">
          {/* Path */}
          <div className="flex items-center gap-3 p-3 bg-gray-50 rounded-lg">
            <svg className="w-4 h-4 text-gray-400 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
            </svg>
            <code className="text-sm font-mono text-gray-700 flex-1 truncate">{info.path}</code>
            <span className="text-xs text-gray-400 flex-shrink-0">config.yaml</span>
          </div>

          {/* Status row */}
          <div className="grid grid-cols-3 gap-3">
            <StatusItem label="目录存在" ok={info.exists} />
            <StatusItem label="可读" ok={info.readable} />
            <StatusItem label="可写" ok={info.writable} />
          </div>

          {/* Disk usage — only if dir exists */}
          {info.exists && info.disk_total > 0 && (
            <div className="space-y-2">
              <div className="flex justify-between text-xs text-gray-500">
                <span>磁盘使用（整体）</span>
                <span>{fmtBytes(info.disk_total - info.disk_free)} / {fmtBytes(info.disk_total)} ({diskUsedPct}%)</span>
              </div>
              <div className="h-2 bg-gray-100 rounded-full overflow-hidden">
                <div
                  className={`h-full rounded-full transition-all ${diskUsedPct > 85 ? 'bg-red-400' : diskUsedPct > 65 ? 'bg-amber-400' : 'bg-blue-400'}`}
                  style={{ width: `${diskUsedPct}%` }}
                />
              </div>
              <div className="flex justify-between text-xs text-gray-500">
                <span>工作目录占用</span>
                <span>{fmtBytes(info.disk_used)} ({dirUsedPct > 0 ? `${dirUsedPct}%` : '< 1%'})</span>
              </div>
            </div>
          )}

          {/* Init button */}
          {(!info.exists || !info.writable) && (
            <button
              type="button"
              onClick={handleInit}
              disabled={initing}
              className="flex items-center gap-2 bg-blue-500 hover:bg-blue-600 disabled:opacity-40 text-white px-4 py-2.5 rounded-lg text-sm font-semibold transition-colors"
            >
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
              </svg>
              {initing ? '初始化中…' : '初始化目录'}
            </button>
          )}
          {info.exists && info.writable && (
            <p className="text-xs text-gray-400">
              路径在 <code className="font-mono">config.yaml</code> 的 <code className="font-mono">workspace.dir</code> 中配置，修改后需重启服务。
            </p>
          )}
        </div>
      )}
    </section>
  );
}

function StatusBadge({ ok }: { ok: boolean }) {
  return (
    <span className={`inline-flex items-center gap-1.5 text-xs font-medium px-2.5 py-1 rounded-full ${ok ? 'text-green-700 bg-green-50 border border-green-200' : 'text-red-600 bg-red-50 border border-red-200'}`}>
      <span className={`w-1.5 h-1.5 rounded-full ${ok ? 'bg-green-500' : 'bg-red-500'}`} />
      {ok ? '正常' : '异常'}
    </span>
  );
}

function StatusItem({ label, ok }: { label: string; ok: boolean }) {
  return (
    <div className={`flex items-center gap-2 px-3 py-2.5 rounded-lg border text-sm ${ok ? 'bg-green-50 border-green-200 text-green-800' : 'bg-red-50 border-red-200 text-red-700'}`}>
      <svg className="w-4 h-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
        {ok
          ? <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
          : <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
        }
      </svg>
      {label}
    </div>
  );
}

// ---- Telegram guide steps ----

function Step({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <li className="flex gap-3">
      <span className="flex-shrink-0 w-6 h-6 rounded-full bg-blue-500 text-white text-xs font-bold flex items-center justify-center mt-0.5">
        {n}
      </span>
      <div className="text-sm text-gray-700 leading-relaxed">
        <span className="font-medium text-gray-900">{title}　</span>
        {children}
      </div>
    </li>
  );
}

function Kbd({ children }: { children: React.ReactNode }) {
  return (
    <kbd className="inline-block bg-gray-100 border border-gray-300 rounded px-1.5 py-0.5 text-xs font-mono text-gray-700">
      {children}
    </kbd>
  );
}

function CopyCmd({ children }: { children: React.ReactNode }) {
  return (
    <code className="block mt-1.5 bg-gray-900 text-green-400 font-mono text-sm px-4 py-2 rounded-lg">
      {children}
    </code>
  );
}

function Chat({ children, dir = 'in' }: { children: React.ReactNode; dir?: 'in' | 'out' }) {
  return (
    <div
      className={`mt-1.5 inline-block max-w-full text-sm px-3 py-2 rounded-xl leading-snug ${
        dir === 'in'
          ? 'bg-gray-100 text-gray-700 rounded-tl-sm'
          : 'bg-blue-500 text-white rounded-tr-sm ml-6'
      }`}
    >
      {children}
    </div>
  );
}

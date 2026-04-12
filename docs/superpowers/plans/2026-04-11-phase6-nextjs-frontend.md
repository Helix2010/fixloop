# FixLoop Phase 6: Next.js Frontend

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the complete Next.js frontend — landing page (SSG), dashboard with 30s polling, project detail tabs, project settings with TG bind, and run log with 10s polling.

**Architecture:** Next.js 15 App Router with TypeScript + Tailwind CSS. Auth is cookie-based JWT (httpOnly, set by backend). All CSR pages start with an `AuthGuard` that calls `GET /api/v1/me` — on 401 it redirects to `/login`. In dev, `next.config.ts` rewrites `/api/*` to the Go backend at `localhost:8080`.

**Tech Stack:** Next.js 15, TypeScript 5, Tailwind CSS 3, React 19

---

## File Structure

```
frontend/
  package.json
  next.config.ts
  tsconfig.json
  tailwind.config.ts
  postcss.config.js
  src/
    app/
      globals.css
      layout.tsx                    # root layout, sets <html lang="zh">
      page.tsx                      # landing page (SSG)
      login/
        page.tsx                    # login redirect page (SSG)
      dashboard/
        page.tsx                    # dashboard (CSR + 30s poll)
      projects/
        [id]/
          page.tsx                  # project detail: issues/prs/backlog/runs tabs
          settings/
            page.tsx                # project settings + TG bind
          runs/
            [runId]/
              page.tsx              # run log (CSR + 10s poll)
    components/
      AuthGuard.tsx                 # calls /me, redirects 401 → /login
      Pagination.tsx                # prev/next page controls
    lib/
      api.ts                        # fetch wrapper, 401 handling
      types.ts                      # TypeScript types matching API response shapes
internal/api/
  handlers/
    auth.go                         # add TGBind handler
  router.go                         # add POST /api/v1/me/tg-bind route
internal/config/
  config.go                         # add TGBotUsername field
config.yaml                         # add tg.bot_username
config.yaml.example                 # add tg.bot_username
```

---

## Task 1: Scaffold + API client + types

**Files:**
- Create: `frontend/` (Next.js project)
- Create: `frontend/next.config.ts`
- Create: `frontend/src/lib/api.ts`
- Create: `frontend/src/lib/types.ts`
- Create: `frontend/src/app/globals.css`
- Create: `frontend/src/app/layout.tsx`

- [ ] **Step 1: Scaffold Next.js inside frontend/**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend
npx create-next-app@latest . \
  --typescript \
  --tailwind \
  --app \
  --src-dir \
  --import-alias "@/*" \
  --no-git \
  --yes
```

Expected: project files created, `npm run dev` starts without error.

- [ ] **Step 2: Verify scaffold compiles**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend && npm run build
```

Expected: Build successful. (Will fail on the scaffolded demo page — that's OK, we'll replace it.)

- [ ] **Step 3: Replace next.config.ts**

```typescript
// frontend/next.config.ts
import type { NextConfig } from 'next'

const nextConfig: NextConfig = {
  // In dev, proxy /api and /health to the Go backend.
  // In production, Nginx handles the proxy — no rewrites needed.
  async rewrites() {
    if (process.env.NODE_ENV !== 'development') return [];
    return [
      {
        source: '/api/:path*',
        destination: 'http://localhost:8080/api/:path*',
      },
      {
        source: '/health',
        destination: 'http://localhost:8080/health',
      },
    ];
  },
};

export default nextConfig;
```

- [ ] **Step 4: Create src/lib/types.ts**

```typescript
// frontend/src/lib/types.ts

export interface User {
  id: number;
  github_login: string;
}

export interface Project {
  id: number;
  name: string;
  status: 'active' | 'paused' | 'error';
  github: {
    owner: string;
    repo: string;
    fix_base_branch: string;
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
  ai_runner?: string;
  ai_model?: string;
  fix_disabled: boolean;
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
  status: string;
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
```

- [ ] **Step 5: Create src/lib/api.ts**

```typescript
// frontend/src/lib/api.ts
'use client';

export class ApiError extends Error {
  constructor(
    public readonly code: string,
    message: string,
    public readonly status: number,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

/**
 * Wrapper around fetch that:
 * - Always sends cookies (credentials: 'include')
 * - On 401: redirects to /login (client-side only)
 * - On non-2xx: throws ApiError with code+message from body
 */
export async function apiFetch<T>(
  path: string,
  options?: RequestInit,
): Promise<T> {
  const res = await fetch(path, {
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...options?.headers,
    },
    ...options,
  });

  if (res.status === 401) {
    if (typeof window !== 'undefined') {
      window.location.href = `/login?redirect=${encodeURIComponent(window.location.pathname)}`;
    }
    throw new ApiError('UNAUTHORIZED', 'Unauthorized', 401);
  }

  let body: unknown;
  try {
    body = await res.json();
  } catch {
    throw new ApiError('PARSE_ERROR', 'Response is not JSON', res.status);
  }

  if (!res.ok) {
    const err = (body as { error?: { code?: string; message?: string } }).error;
    throw new ApiError(
      err?.code ?? 'ERROR',
      err?.message ?? `HTTP ${res.status}`,
      res.status,
    );
  }

  return body as T;
}
```

- [ ] **Step 6: Replace src/app/globals.css with minimal version**

```css
/* frontend/src/app/globals.css */
@tailwind base;
@tailwind components;
@tailwind utilities;
```

- [ ] **Step 7: Replace src/app/layout.tsx**

```tsx
// frontend/src/app/layout.tsx
import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = {
  title: 'FixLoop — AI 驱动的 CI/CD 自动化',
  description: 'AI 自动发现 bug、提交修复 PR、部署验证，形成完整的 CI/CD 闭环。',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh">
      <body className="bg-gray-50 text-gray-900 antialiased">
        {children}
      </body>
    </html>
  );
}
```

- [ ] **Step 8: Verify TypeScript types compile**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend && npx tsc --noEmit
```

Expected: No errors.

- [ ] **Step 9: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add frontend/
git commit -m "feat: scaffold Next.js frontend with API client and TypeScript types"
```

---

## Task 2: Landing page + login page

**Files:**
- Create: `frontend/src/app/page.tsx`
- Create: `frontend/src/app/login/page.tsx`

These are both SSG (no `'use client'`). The landing page is the product intro with a GitHub login button. The login page is shown when an unauthenticated user tries to access a protected page.

- [ ] **Step 1: Create src/app/page.tsx (landing page)**

```tsx
// frontend/src/app/page.tsx
// SSG — no 'use client' needed
import Link from 'next/link';

export default function LandingPage() {
  return (
    <main className="min-h-screen bg-gradient-to-br from-gray-900 to-gray-800 text-white">
      {/* Nav */}
      <nav className="flex items-center justify-between px-8 py-5 max-w-6xl mx-auto">
        <span className="text-2xl font-bold tracking-tight">FixLoop</span>
        <a
          href="/api/v1/auth/github"
          className="bg-white text-gray-900 px-5 py-2 rounded-lg text-sm font-semibold hover:bg-gray-100 transition-colors"
        >
          Login with GitHub
        </a>
      </nav>

      {/* Hero */}
      <section className="flex flex-col items-center justify-center px-8 py-32 text-center max-w-4xl mx-auto">
        <h1 className="text-5xl font-bold leading-tight mb-6">
          AI 驱动的<br />CI/CD 自动化闭环
        </h1>
        <p className="text-xl text-gray-300 mb-10 max-w-2xl">
          FixLoop 自动发现 bug、提交修复 PR、部署验证 —— 从发现问题到线上修复，全程无需人工干预。
        </p>
        <a
          href="/api/v1/auth/github"
          className="bg-blue-500 hover:bg-blue-600 text-white px-10 py-4 rounded-xl text-lg font-semibold transition-colors inline-flex items-center gap-3"
        >
          <svg className="w-6 h-6" fill="currentColor" viewBox="0 0 24 24">
            <path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/>
          </svg>
          GitHub 登录，5 分钟上手
        </a>

        {/* Feature grid */}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-8 mt-24 text-left w-full">
          {[
            { icon: '🔍', title: 'AI 探索', desc: 'Playwright 驱动 UI 测试，自动发现 bug 并创建 GitHub Issue' },
            { icon: '🔧', title: 'AI 修复', desc: 'Claude/Gemini 分析问题，生成修复 PR，自动 code review' },
            { icon: '✅', title: '自动验收', desc: 'Vercel 部署后自动运行验收测试，通过则合并关闭 Issue' },
          ].map((f) => (
            <div key={f.title} className="bg-gray-800 rounded-xl p-6 border border-gray-700">
              <div className="text-3xl mb-3">{f.icon}</div>
              <h3 className="text-lg font-semibold mb-2">{f.title}</h3>
              <p className="text-gray-400 text-sm">{f.desc}</p>
            </div>
          ))}
        </div>
      </section>
    </main>
  );
}
```

- [ ] **Step 2: Create src/app/login/page.tsx**

```tsx
// frontend/src/app/login/page.tsx
// SSG — shown when AuthGuard redirects unauthenticated users
export default function LoginPage() {
  return (
    <main className="min-h-screen bg-gray-50 flex items-center justify-center">
      <div className="bg-white rounded-2xl shadow-sm border border-gray-200 p-10 max-w-sm w-full text-center">
        <h1 className="text-2xl font-bold mb-2">登录 FixLoop</h1>
        <p className="text-gray-500 text-sm mb-8">使用 GitHub 账号登录，5 分钟完成接入</p>
        <a
          href="/api/v1/auth/github"
          className="w-full bg-gray-900 hover:bg-gray-700 text-white py-3 px-6 rounded-lg font-semibold text-sm inline-flex items-center justify-center gap-2 transition-colors"
        >
          <svg className="w-5 h-5" fill="currentColor" viewBox="0 0 24 24">
            <path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/>
          </svg>
          使用 GitHub 登录
        </a>
      </div>
    </main>
  );
}
```

- [ ] **Step 3: Verify build**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend && npm run build 2>&1 | tail -20
```

Expected: Build succeeds. `/` and `/login` are static (SSG) pages.

- [ ] **Step 4: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add frontend/src/app/page.tsx frontend/src/app/login/
git commit -m "feat: add landing page (SSG) and login page"
```

---

## Task 3: AuthGuard + Dashboard

**Files:**
- Create: `frontend/src/components/AuthGuard.tsx`
- Create: `frontend/src/app/dashboard/page.tsx`

The dashboard fetches the project list every 30s and shows an unread notification badge.

- [ ] **Step 1: Create src/components/AuthGuard.tsx**

```tsx
// frontend/src/components/AuthGuard.tsx
'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { apiFetch } from '@/lib/api';
import type { User, SingleResponse } from '@/lib/types';

interface Props {
  children: (user: User) => React.ReactNode;
}

/**
 * Calls GET /api/v1/me on mount.
 * - On 401: redirects to /login (apiFetch handles this automatically).
 * - While loading: shows a spinner.
 * - On success: renders children(user).
 */
export default function AuthGuard({ children }: Props) {
  const [user, setUser] = useState<User | null>(null);
  const [error, setError] = useState<string | null>(null);
  const router = useRouter();

  useEffect(() => {
    apiFetch<SingleResponse<User>>('/api/v1/me')
      .then((res) => setUser(res.data))
      .catch((err) => {
        // apiFetch redirects on 401; other errors show a message.
        if (err?.status !== 401) setError(err.message ?? 'Unknown error');
      });
  }, []);

  if (error) {
    return (
      <div className="min-h-screen flex items-center justify-center text-red-500">
        {error}
      </div>
    );
  }

  if (!user) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="w-8 h-8 border-4 border-blue-500 border-t-transparent rounded-full animate-spin" />
      </div>
    );
  }

  return <>{children(user)}</>;
}
```

- [ ] **Step 2: Create src/app/dashboard/page.tsx**

```tsx
// frontend/src/app/dashboard/page.tsx
'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import AuthGuard from '@/components/AuthGuard';
import { apiFetch, ApiError } from '@/lib/api';
import type {
  User,
  Project,
  Notification,
  PagedResponse,
  SingleResponse,
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
    } finally {
      setLoading(false);
    }
  }, []);

  // Initial load + 30s polling
  useEffect(() => {
    fetchData();
    const id = setInterval(fetchData, 30_000);
    return () => clearInterval(id);
  }, [fetchData]);

  const statusIcon = (status: string) => {
    if (status === 'active') return '🟢';
    if (status === 'paused') return '⏸';
    return '🔴';
  };

  return (
    <div className="min-h-screen bg-gray-50">
      {/* Header */}
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-5xl mx-auto px-6 py-4 flex items-center justify-between">
          <Link href="/dashboard" className="text-xl font-bold">FixLoop</Link>
          <div className="flex items-center gap-4">
            {/* Notification badge */}
            <button
              className="relative text-gray-500 hover:text-gray-700"
              title="通知"
            >
              🔔
              {unreadCount > 0 && (
                <span className="absolute -top-1 -right-1 bg-red-500 text-white text-xs rounded-full w-4 h-4 flex items-center justify-center font-bold">
                  {unreadCount > 9 ? '9+' : unreadCount}
                </span>
              )}
            </button>
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
          <div className="flex justify-center py-20">
            <div className="w-8 h-8 border-4 border-blue-500 border-t-transparent rounded-full animate-spin" />
          </div>
        ) : projects.length === 0 ? (
          <div className="text-center py-20 text-gray-500">
            <p className="text-lg mb-4">还没有项目</p>
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
              <Link
                key={p.id}
                href={`/projects/${p.id}`}
                className="bg-white rounded-xl border border-gray-200 p-5 hover:shadow-md transition-shadow"
              >
                <div className="flex items-center justify-between mb-2">
                  <span className="font-semibold">{p.name}</span>
                  <span title={p.status}>{statusIcon(p.status)}</span>
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
            ))}
          </div>
        )}
      </main>

      {showCreate && (
        <CreateProjectModal
          onClose={() => setShowCreate(false)}
          onCreated={() => { setShowCreate(false); fetchData(); }}
        />
      )}
    </div>
  );
}

// ---- Create project modal ----

interface CreateProjectModalProps {
  onClose: () => void;
  onCreated: () => void;
}

function CreateProjectModal({ onClose, onCreated }: CreateProjectModalProps) {
  const [form, setForm] = useState({
    name: '',
    github_owner: '',
    github_repo: '',
    github_pat: '',
    fix_base_branch: 'main',
    staging_url: '',
    ai_runner: 'claude',
    ai_model: 'claude-opus-4-6',
    ai_api_key: '',
  });
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setForm((f) => ({ ...f, [k]: e.target.value }));

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
          ai_api_key: form.ai_api_key,
        }),
      });
      onCreated();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
      <div className="bg-white rounded-2xl max-w-lg w-full p-6 max-h-screen overflow-y-auto">
        <div className="flex items-center justify-between mb-5">
          <h3 className="text-xl font-bold">新建项目</h3>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-2xl">&times;</button>
        </div>
        <form onSubmit={handleSubmit} className="space-y-4">
          <Field label="项目名称" required>
            <input className={inputClass} value={form.name} onChange={set('name')} required placeholder="my-app" />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="GitHub Owner" required>
              <input className={inputClass} value={form.github_owner} onChange={set('github_owner')} required placeholder="myorg" />
            </Field>
            <Field label="GitHub Repo" required>
              <input className={inputClass} value={form.github_repo} onChange={set('github_repo')} required placeholder="my-app" />
            </Field>
          </div>
          <Field label="GitHub Fine-grained PAT" required>
            <input className={inputClass} type="password" value={form.github_pat} onChange={set('github_pat')} required placeholder="github_pat_..." />
          </Field>
          <Field label="Fix Base Branch">
            <input className={inputClass} value={form.fix_base_branch} onChange={set('fix_base_branch')} placeholder="main" />
          </Field>
          <Field label="Staging URL">
            <input className={inputClass} value={form.staging_url} onChange={set('staging_url')} placeholder="https://staging.example.com" />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="AI Runner">
              <select className={inputClass} value={form.ai_runner} onChange={set('ai_runner')}>
                <option value="claude">Claude</option>
                <option value="gemini">Gemini</option>
                <option value="aider">Aider</option>
              </select>
            </Field>
            <Field label="AI Model">
              <input className={inputClass} value={form.ai_model} onChange={set('ai_model')} placeholder="claude-opus-4-6" />
            </Field>
          </div>
          <Field label="AI API Key">
            <input className={inputClass} type="password" value={form.ai_api_key} onChange={set('ai_api_key')} placeholder="sk-ant-..." />
          </Field>
          {error && <p className="text-red-500 text-sm">{error}</p>}
          <button
            type="submit"
            disabled={loading}
            className="w-full bg-blue-500 hover:bg-blue-600 disabled:opacity-50 text-white py-3 rounded-lg font-semibold text-sm transition-colors"
          >
            {loading ? '创建中...' : '创建项目'}
          </button>
        </form>
      </div>
    </div>
  );
}

const inputClass = 'w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500';

function Field({ label, required, children }: { label: string; required?: boolean; children: React.ReactNode }) {
  return (
    <div>
      <label className="block text-sm font-medium text-gray-700 mb-1">
        {label}{required && <span className="text-red-500 ml-0.5">*</span>}
      </label>
      {children}
    </div>
  );
}
```

- [ ] **Step 3: Verify TypeScript**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend && npx tsc --noEmit 2>&1
```

Expected: No errors.

- [ ] **Step 4: Verify build**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend && npm run build 2>&1 | tail -20
```

Expected: Build succeeds.

- [ ] **Step 5: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add frontend/src/components/ frontend/src/app/dashboard/
git commit -m "feat: add AuthGuard component and dashboard with 30s polling"
```

---

## Task 4: Project detail page (issues / PRs / backlog / runs tabs)

**Files:**
- Create: `frontend/src/components/Pagination.tsx`
- Create: `frontend/src/app/projects/[id]/page.tsx`

The detail page uses `?tab=issues|prs|backlog|runs` (default: `issues`) to switch between four data tabs. Each tab fetches its data with pagination.

- [ ] **Step 1: Create src/components/Pagination.tsx**

```tsx
// frontend/src/components/Pagination.tsx
interface Props {
  page: number;
  total: number;
  perPage: number;
  onChange: (page: number) => void;
}

export default function Pagination({ page, total, perPage, onChange }: Props) {
  const totalPages = Math.ceil(total / perPage);
  if (totalPages <= 1) return null;

  return (
    <div className="flex items-center justify-between mt-4 text-sm text-gray-600">
      <span>共 {total} 条</span>
      <div className="flex gap-2">
        <button
          onClick={() => onChange(page - 1)}
          disabled={page <= 1}
          className="px-3 py-1 border border-gray-300 rounded disabled:opacity-40 hover:bg-gray-50"
        >
          上一页
        </button>
        <span className="px-3 py-1">
          {page} / {totalPages}
        </span>
        <button
          onClick={() => onChange(page + 1)}
          disabled={page >= totalPages}
          className="px-3 py-1 border border-gray-300 rounded disabled:opacity-40 hover:bg-gray-50"
        >
          下一页
        </button>
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Create src/app/projects/[id]/page.tsx**

```tsx
// frontend/src/app/projects/[id]/page.tsx
'use client';

import { useCallback, useEffect, useState } from 'react';
import { useParams, useSearchParams, useRouter } from 'next/navigation';
import Link from 'next/link';
import AuthGuard from '@/components/AuthGuard';
import Pagination from '@/components/Pagination';
import { apiFetch, ApiError } from '@/lib/api';
import type {
  User,
  Project,
  Issue,
  PR,
  BacklogItem,
  AgentRun,
  PagedResponse,
  SingleResponse,
} from '@/lib/types';

export default function ProjectDetailPage() {
  return <AuthGuard>{(user) => <ProjectDetail user={user} />}</AuthGuard>;
}

type Tab = 'issues' | 'prs' | 'backlog' | 'runs';

function ProjectDetail({ user: _user }: { user: User }) {
  const { id } = useParams<{ id: string }>();
  const searchParams = useSearchParams();
  const router = useRouter();
  const tab = (searchParams.get('tab') as Tab) || 'issues';

  const [project, setProject] = useState<Project | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    apiFetch<SingleResponse<Project>>(`/api/v1/projects/${id}`)
      .then((r) => setProject(r.data))
      .catch((err) => setError(err instanceof ApiError ? err.message : String(err)))
      .finally(() => setLoading(false));
  }, [id]);

  const setTab = (t: Tab) => {
    router.replace(`/projects/${id}?tab=${t}`, { scroll: false });
  };

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="w-8 h-8 border-4 border-blue-500 border-t-transparent rounded-full animate-spin" />
      </div>
    );
  }
  if (error || !project) {
    return (
      <div className="min-h-screen flex items-center justify-center text-red-500">
        {error || '项目不存在'}
      </div>
    );
  }

  const tabs: { key: Tab; label: string }[] = [
    { key: 'issues', label: 'Issues' },
    { key: 'prs', label: 'Pull Requests' },
    { key: 'backlog', label: 'Backlog' },
    { key: 'runs', label: '运行日志' },
  ];

  return (
    <div className="min-h-screen bg-gray-50">
      {/* Header */}
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-5xl mx-auto px-6 py-4 flex items-center gap-3">
          <Link href="/dashboard" className="text-gray-400 hover:text-gray-600 text-sm">← Dashboard</Link>
          <span className="text-gray-300">/</span>
          <span className="font-semibold">{project.name}</span>
          <span className="ml-auto">
            <Link
              href={`/projects/${id}/settings`}
              className="text-sm text-gray-500 hover:text-gray-700 border border-gray-200 rounded px-3 py-1"
            >
              设置
            </Link>
          </span>
        </div>
      </header>

      <div className="max-w-5xl mx-auto px-6 py-6">
        {/* Project summary */}
        <div className="bg-white border border-gray-200 rounded-xl p-4 mb-6 flex items-center gap-4 text-sm text-gray-600">
          <span className="font-medium text-gray-900">{project.github.owner}/{project.github.repo}</span>
          {project.test?.staging_url && <span className="text-blue-500">{project.test.staging_url}</span>}
          <span className={`ml-auto px-2 py-0.5 rounded-full text-xs font-semibold ${
            project.status === 'active' ? 'bg-green-100 text-green-700' :
            project.status === 'paused' ? 'bg-yellow-100 text-yellow-700' :
            'bg-red-100 text-red-700'
          }`}>{project.status}</span>
        </div>

        {/* Tabs */}
        <div className="flex gap-1 mb-5 border-b border-gray-200">
          {tabs.map((t) => (
            <button
              key={t.key}
              onClick={() => setTab(t.key)}
              className={`px-4 py-2.5 text-sm font-medium -mb-px border-b-2 transition-colors ${
                tab === t.key
                  ? 'border-blue-500 text-blue-600'
                  : 'border-transparent text-gray-500 hover:text-gray-700'
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>

        {/* Tab content */}
        {tab === 'issues' && <IssuesTab projectId={id} />}
        {tab === 'prs' && <PRsTab projectId={id} />}
        {tab === 'backlog' && <BacklogTab projectId={id} />}
        {tab === 'runs' && <RunsTab projectId={id} />}
      </div>
    </div>
  );
}

// ---- Issues tab ----

function IssuesTab({ projectId }: { projectId: string }) {
  const [issues, setIssues] = useState<Issue[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async (p: number) => {
    setLoading(true);
    try {
      const res = await apiFetch<PagedResponse<Issue>>(
        `/api/v1/projects/${projectId}/issues?page=${p}&per_page=20`,
      );
      setIssues(res.data);
      setTotal(res.pagination.total);
      setPage(p);
    } finally {
      setLoading(false);
    }
  }, [projectId]);

  useEffect(() => { load(1); }, [load]);

  const statusColor = (s: string) => {
    if (s === 'open') return 'bg-green-100 text-green-700';
    if (s === 'fixing') return 'bg-blue-100 text-blue-700';
    if (s === 'needs-human') return 'bg-red-100 text-red-700';
    return 'bg-gray-100 text-gray-600';
  };

  if (loading) return <Spinner />;
  if (issues.length === 0) return <Empty text="暂无 Issues" />;

  return (
    <div>
      <div className="bg-white rounded-xl border border-gray-200 divide-y divide-gray-100">
        {issues.map((i) => (
          <div key={i.id} className="px-5 py-4 flex items-start justify-between gap-4">
            <div className="flex-1 min-w-0">
              <p className="font-medium text-sm truncate">{i.title}</p>
              <p className="text-xs text-gray-400 mt-0.5">
                #{i.github_number} · 尝试 {i.fix_attempts} 次 · {fmtDate(i.created_at)}
              </p>
            </div>
            <span className={`text-xs px-2 py-0.5 rounded-full font-medium whitespace-nowrap ${statusColor(i.status)}`}>
              {i.status}
            </span>
          </div>
        ))}
      </div>
      <Pagination page={page} total={total} perPage={20} onChange={load} />
    </div>
  );
}

// ---- PRs tab ----

function PRsTab({ projectId }: { projectId: string }) {
  const [prs, setPRs] = useState<PR[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async (p: number) => {
    setLoading(true);
    try {
      const res = await apiFetch<PagedResponse<PR>>(
        `/api/v1/projects/${projectId}/prs?page=${p}&per_page=20`,
      );
      setPRs(res.data);
      setTotal(res.pagination.total);
      setPage(p);
    } finally {
      setLoading(false);
    }
  }, [projectId]);

  useEffect(() => { load(1); }, [load]);

  if (loading) return <Spinner />;
  if (prs.length === 0) return <Empty text="暂无 Pull Requests" />;

  return (
    <div>
      <div className="bg-white rounded-xl border border-gray-200 divide-y divide-gray-100">
        {prs.map((pr) => (
          <div key={pr.id} className="px-5 py-4 flex items-center justify-between gap-4">
            <div>
              <p className="font-medium text-sm">PR #{pr.github_number}</p>
              <p className="text-xs text-gray-400 mt-0.5">{pr.branch} · {fmtDate(pr.created_at)}</p>
            </div>
            <span className={`text-xs px-2 py-0.5 rounded-full font-medium ${
              pr.status === 'merged' ? 'bg-purple-100 text-purple-700' :
              pr.status === 'open' ? 'bg-green-100 text-green-700' :
              'bg-gray-100 text-gray-600'
            }`}>
              {pr.status}
            </span>
          </div>
        ))}
      </div>
      <Pagination page={page} total={total} perPage={20} onChange={load} />
    </div>
  );
}

// ---- Backlog tab ----

function BacklogTab({ projectId }: { projectId: string }) {
  const [items, setItems] = useState<BacklogItem[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [statusFilter, setStatusFilter] = useState<'pending' | 'ignored'>('pending');
  const [loading, setLoading] = useState(true);

  const load = useCallback(async (p: number, sf = statusFilter) => {
    setLoading(true);
    try {
      const res = await apiFetch<PagedResponse<BacklogItem>>(
        `/api/v1/projects/${projectId}/backlog?status=${sf}&page=${p}&per_page=20`,
      );
      setItems(res.data);
      setTotal(res.pagination.total);
      setPage(p);
    } finally {
      setLoading(false);
    }
  }, [projectId, statusFilter]);

  useEffect(() => { load(1, statusFilter); }, [projectId, statusFilter]); // eslint-disable-line

  const toggleIgnore = async (item: BacklogItem) => {
    const newStatus = item.status === 'ignored' ? 'pending' : 'ignored';
    try {
      await apiFetch(`/api/v1/projects/${projectId}/backlog/${item.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ status: newStatus }),
      });
      load(page);
    } catch {
      // ignore
    }
  };

  return (
    <div>
      <div className="flex gap-2 mb-4">
        {(['pending', 'ignored'] as const).map((s) => (
          <button
            key={s}
            onClick={() => { setStatusFilter(s); }}
            className={`px-3 py-1 rounded-full text-xs font-medium border transition-colors ${
              statusFilter === s
                ? 'bg-gray-900 text-white border-gray-900'
                : 'border-gray-300 text-gray-600 hover:bg-gray-50'
            }`}
          >
            {s === 'pending' ? '待测试' : '已忽略'}
          </button>
        ))}
      </div>
      {loading ? <Spinner /> : items.length === 0 ? <Empty text="暂无场景" /> : (
        <div>
          <div className="bg-white rounded-xl border border-gray-200 divide-y divide-gray-100">
            {items.map((item) => (
              <div key={item.id} className="px-5 py-4 flex items-center justify-between gap-4">
                <div className="flex-1 min-w-0">
                  <p className="font-medium text-sm truncate">{item.title}</p>
                  <p className="text-xs text-gray-400 mt-0.5">
                    {item.scenario_type} · 优先级 {item.priority} · {item.source}
                  </p>
                </div>
                <button
                  onClick={() => toggleIgnore(item)}
                  className="text-xs text-gray-400 hover:text-gray-700 border border-gray-200 px-2 py-1 rounded"
                >
                  {item.status === 'ignored' ? '恢复' : '忽略'}
                </button>
              </div>
            ))}
          </div>
          <Pagination page={page} total={total} perPage={20} onChange={(p) => load(p)} />
        </div>
      )}
    </div>
  );
}

// ---- Runs tab ----

function RunsTab({ projectId }: { projectId: string }) {
  const [runs, setRuns] = useState<AgentRun[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [triggering, setTriggering] = useState(false);

  const load = useCallback(async (p: number) => {
    setLoading(true);
    try {
      const res = await apiFetch<PagedResponse<AgentRun>>(
        `/api/v1/projects/${projectId}/runs?page=${p}&per_page=20`,
      );
      setRuns(res.data);
      setTotal(res.pagination.total);
      setPage(p);
    } finally {
      setLoading(false);
    }
  }, [projectId]);

  useEffect(() => { load(1); }, [load]);

  const triggerRun = async (agentType: string) => {
    setTriggering(true);
    try {
      await apiFetch(`/api/v1/projects/${projectId}/runs`, {
        method: 'POST',
        body: JSON.stringify({ agent_type: agentType }),
      });
      setTimeout(() => load(1), 1000);
    } catch {
      // ignore
    } finally {
      setTriggering(false);
    }
  };

  if (loading) return <Spinner />;

  return (
    <div>
      <div className="flex gap-2 mb-4">
        {['explore', 'fix', 'master', 'plan'].map((t) => (
          <button
            key={t}
            onClick={() => triggerRun(t)}
            disabled={triggering}
            className="text-xs px-3 py-1.5 bg-gray-100 hover:bg-gray-200 rounded-lg font-medium disabled:opacity-50 transition-colors"
          >
            ▶ {t}
          </button>
        ))}
      </div>
      {runs.length === 0 ? <Empty text="暂无运行记录" /> : (
        <div>
          <div className="bg-white rounded-xl border border-gray-200 divide-y divide-gray-100">
            {runs.map((r) => (
              <Link
                key={r.id}
                href={`/projects/${projectId}/runs/${r.id}`}
                className="px-5 py-4 flex items-center justify-between gap-4 hover:bg-gray-50 transition-colors block"
              >
                <div>
                  <p className="font-medium text-sm">{r.agent_type}-agent</p>
                  <p className="text-xs text-gray-400 mt-0.5">{fmtDate(r.started_at)}</p>
                </div>
                <span className={`text-xs px-2 py-0.5 rounded-full font-medium ${
                  r.status === 'success' ? 'bg-green-100 text-green-700' :
                  r.status === 'running' ? 'bg-blue-100 text-blue-700' :
                  r.status === 'failed' ? 'bg-red-100 text-red-700' :
                  'bg-gray-100 text-gray-600'
                }`}>
                  {r.status}
                </span>
              </Link>
            ))}
          </div>
          <Pagination page={page} total={total} perPage={20} onChange={load} />
        </div>
      )}
    </div>
  );
}

// ---- helpers ----

function Spinner() {
  return (
    <div className="flex justify-center py-12">
      <div className="w-6 h-6 border-4 border-blue-500 border-t-transparent rounded-full animate-spin" />
    </div>
  );
}

function Empty({ text }: { text: string }) {
  return <p className="text-center py-12 text-gray-400">{text}</p>;
}

function fmtDate(iso: string) {
  return new Date(iso).toLocaleString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}
```

- [ ] **Step 3: Verify TypeScript**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend && npx tsc --noEmit 2>&1
```

Expected: No errors.

- [ ] **Step 4: Verify build**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend && npm run build 2>&1 | tail -20
```

Expected: Build succeeds.

- [ ] **Step 5: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add frontend/src/components/Pagination.tsx frontend/src/app/projects/
git commit -m "feat: add project detail page with issues/prs/backlog/runs tabs"
```

---

## Task 5: Project settings page + backend TG bind endpoint

**Files:**
- Modify: `internal/config/config.go` — add `TGBotUsername string`
- Modify: `config.yaml` — add `tg.bot_username`
- Modify: `config.yaml.example` — add `tg.bot_username`
- Modify: `internal/api/handlers/auth.go` — add `TGBind` handler
- Modify: `internal/api/router.go` — add `POST /api/v1/me/tg-bind` route
- Create: `frontend/src/app/projects/[id]/settings/page.tsx`

### Backend changes first

- [ ] **Step 1: Add TGBotUsername to config.go**

Read `internal/config/config.go`. Add `TGBotUsername string` to the `Config` struct and `BotUsername string \`yaml:"bot_username"\`` to the `TG` struct, then set it in Load().

In `Config` struct, after `TGBotToken string` add:
```go
TGBotUsername string
```

In `yamlConfig.TG` struct, after `BotToken string \`yaml:"bot_token"\`` add:
```go
BotUsername string `yaml:"bot_username"`
```

In the `return &Config{...}` block, after `TGBotToken: y.TG.BotToken,` add:
```go
TGBotUsername: y.TG.BotUsername,
```

- [ ] **Step 2: Update config.yaml and config.yaml.example**

In `config.yaml`, change the `tg:` section from:
```yaml
tg:
  bot_token: ""
```
to:
```yaml
tg:
  bot_token: ""
  bot_username: ""
```

Apply the same change to `config.yaml.example`.

- [ ] **Step 3: Add TGBind handler to auth.go**

Read `internal/api/handlers/auth.go`. At the end of the file, before the last `}` or after `DeleteMe`, add:

```go
// TGBind generates a one-time Telegram bind token. POST /api/v1/me/tg-bind
// Returns the token and the t.me deep link for opening the bot.
func (h *AuthHandler) TGBind(c *gin.Context) {
	if h.Cfg.TGBotUsername == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"code":    "TG_NOT_CONFIGURED",
			"message": "Telegram Bot 未配置，请联系管理员",
		}})
		return
	}
	userID := c.MustGet("user_id").(int64)

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "RAND_ERROR", "message": "生成 token 失败",
		}})
		return
	}
	token := fmt.Sprintf("%x", b)
	key := "tg_bind_" + token

	_, err := h.DB.ExecContext(c.Request.Context(),
		`INSERT INTO system_config (key_name, value) VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = NOW()`,
		key, fmt.Sprintf("%d", userID),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "DB_ERROR", "message": "生成绑定 token 失败",
		}})
		return
	}

	tgURL := fmt.Sprintf("https://t.me/%s?start=%s", h.Cfg.TGBotUsername, token)
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"token":  token,
		"tg_url": tgURL,
	}})
}
```

Note: `rand`, `fmt`, `http` are already imported. Verify the import list and add any missing imports.

- [ ] **Step 4: Register route in router.go**

Read `internal/api/router.go`. In the `authed` group (after `authed.DELETE("/me", authH.DeleteMe)`), add:

```go
authed.POST("/me/tg-bind", authH.TGBind)
```

- [ ] **Step 5: Verify Go build**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./...
```

Expected: No output (clean build).

- [ ] **Step 6: Create frontend/src/app/projects/[id]/settings/page.tsx**

```tsx
// frontend/src/app/projects/[id]/settings/page.tsx
'use client';

import { useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import Link from 'next/link';
import AuthGuard from '@/components/AuthGuard';
import { apiFetch, ApiError } from '@/lib/api';
import type { User, Project, SingleResponse } from '@/lib/types';

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
  const [tgLoading, setTgLoading] = useState(false);
  const [tgUrl, setTgUrl] = useState('');

  // Form state (all optional override fields)
  const [form, setForm] = useState({
    staging_url: '',
    staging_auth_type: 'none',
    vercel_project_id: '',
    vercel_staging_target: 'preview',
    ai_runner: 'claude',
    ai_model: '',
    ai_api_base: '',
    ai_api_key: '',
    fix_disabled: false,
  });

  useEffect(() => {
    apiFetch<SingleResponse<Project>>(`/api/v1/projects/${id}`)
      .then((r) => {
        const p = r.data;
        setProject(p);
        setForm({
          staging_url: p.test?.staging_url ?? '',
          staging_auth_type: p.test?.staging_auth_type ?? 'none',
          vercel_project_id: p.vercel?.project_id ?? '',
          vercel_staging_target: p.vercel?.staging_target ?? 'preview',
          ai_runner: p.ai_runner ?? 'claude',
          ai_model: p.ai_model ?? '',
          ai_api_base: '',
          ai_api_key: '',   // never pre-filled for security
          fix_disabled: p.fix_disabled,
        });
      })
      .finally(() => setLoading(false));
  }, [id]);

  const set = (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((f) => ({ ...f, [k]: e.target.type === 'checkbox' ? (e.target as HTMLInputElement).checked : e.target.value }));

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError('');
    setSaved(false);
    try {
      const patch: Record<string, unknown> = {
        test: {
          staging_url: form.staging_url,
          staging_auth_type: form.staging_auth_type,
        },
        vercel: {
          project_id: form.vercel_project_id,
          staging_target: form.vercel_staging_target,
        },
        ai_runner: form.ai_runner,
        fix_disabled: form.fix_disabled,
      };
      if (form.ai_model) patch.ai_model = form.ai_model;
      if (form.ai_api_base) patch.ai_api_base = form.ai_api_base;
      if (form.ai_api_key) patch.ai_api_key = form.ai_api_key;

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

  const handlePause = async () => {
    try {
      await apiFetch(`/api/v1/projects/${id}/pause`, { method: 'POST' });
      router.push('/dashboard');
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  };

  const handleResume = async () => {
    try {
      await apiFetch(`/api/v1/projects/${id}/resume`, { method: 'POST' });
      router.push('/dashboard');
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  };

  const handleDelete = async () => {
    if (!confirm(`确定要删除项目 "${project?.name}" 吗？此操作不可撤销。`)) return;
    try {
      await apiFetch(`/api/v1/projects/${id}`, { method: 'DELETE' });
      router.push('/dashboard');
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  };

  const handleTGBind = async () => {
    setTgLoading(true);
    setTgUrl('');
    try {
      const res = await apiFetch<{ data: { token: string; tg_url: string } }>('/api/v1/me/tg-bind', {
        method: 'POST',
      });
      setTgUrl(res.data.tg_url);
      window.open(res.data.tg_url, '_blank');
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setTgLoading(false);
    }
  };

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="w-8 h-8 border-4 border-blue-500 border-t-transparent rounded-full animate-spin" />
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-2xl mx-auto px-6 py-4 flex items-center gap-3">
          <Link href={`/projects/${id}`} className="text-gray-400 hover:text-gray-600 text-sm">← 项目详情</Link>
          <span className="text-gray-300">/</span>
          <span className="font-semibold">设置</span>
        </div>
      </header>

      <main className="max-w-2xl mx-auto px-6 py-8 space-y-8">
        {/* Config form */}
        <section className="bg-white rounded-xl border border-gray-200 p-6">
          <h2 className="text-lg font-semibold mb-5">项目配置</h2>
          <form onSubmit={handleSave} className="space-y-5">
            <SectionTitle>测试环境</SectionTitle>
            <Field label="Staging URL">
              <input className={inputClass} value={form.staging_url} onChange={set('staging_url')} placeholder="https://staging.example.com" />
            </Field>
            <Field label="Auth Type">
              <select className={inputClass} value={form.staging_auth_type} onChange={set('staging_auth_type')}>
                <option value="none">None</option>
                <option value="basic">Basic Auth</option>
                <option value="bearer">Bearer Token</option>
              </select>
            </Field>

            <SectionTitle>Vercel</SectionTitle>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Project ID">
                <input className={inputClass} value={form.vercel_project_id} onChange={set('vercel_project_id')} placeholder="prj_xxx" />
              </Field>
              <Field label="Staging Target">
                <select className={inputClass} value={form.vercel_staging_target} onChange={set('vercel_staging_target')}>
                  <option value="preview">Preview</option>
                  <option value="production">Production</option>
                </select>
              </Field>
            </div>

            <SectionTitle>AI 配置</SectionTitle>
            <div className="grid grid-cols-2 gap-3">
              <Field label="AI Runner">
                <select className={inputClass} value={form.ai_runner} onChange={set('ai_runner')}>
                  <option value="claude">Claude</option>
                  <option value="gemini">Gemini</option>
                  <option value="aider">Aider</option>
                </select>
              </Field>
              <Field label="AI Model">
                <input className={inputClass} value={form.ai_model} onChange={set('ai_model')} placeholder="claude-opus-4-6" />
              </Field>
            </div>
            <Field label="API Base URL (aider)">
              <input className={inputClass} value={form.ai_api_base} onChange={set('ai_api_base')} placeholder="https://api.openai.com/v1" />
            </Field>
            <Field label="AI API Key（留空不修改）">
              <input className={inputClass} type="password" value={form.ai_api_key} onChange={set('ai_api_key')} placeholder="••••••••" />
            </Field>

            <SectionTitle>其他</SectionTitle>
            <label className="flex items-center gap-3 text-sm">
              <input
                type="checkbox"
                checked={form.fix_disabled}
                onChange={set('fix_disabled')}
                className="w-4 h-4 rounded border-gray-300"
              />
              <span>暂停 Fix Agent（不自动提交修复 PR）</span>
            </label>

            {error && <p className="text-red-500 text-sm">{error}</p>}
            {saved && <p className="text-green-600 text-sm">已保存</p>}

            <button
              type="submit"
              disabled={saving}
              className="bg-blue-500 hover:bg-blue-600 disabled:opacity-50 text-white px-6 py-2.5 rounded-lg text-sm font-semibold transition-colors"
            >
              {saving ? '保存中...' : '保存配置'}
            </button>
          </form>
        </section>

        {/* TG bind */}
        <section className="bg-white rounded-xl border border-gray-200 p-6">
          <h2 className="text-lg font-semibold mb-2">绑定 Telegram</h2>
          <p className="text-sm text-gray-500 mb-4">绑定后，所有项目通知将推送到你的 Telegram 账号。</p>
          <button
            onClick={handleTGBind}
            disabled={tgLoading}
            className="bg-blue-400 hover:bg-blue-500 disabled:opacity-50 text-white px-5 py-2.5 rounded-lg text-sm font-semibold transition-colors"
          >
            {tgLoading ? '生成中...' : '获取绑定链接'}
          </button>
          {tgUrl && (
            <p className="mt-3 text-sm text-gray-600">
              链接已复制：
              <a href={tgUrl} target="_blank" rel="noopener noreferrer" className="text-blue-500 underline ml-1 break-all">
                {tgUrl}
              </a>
            </p>
          )}
        </section>

        {/* Danger zone */}
        <section className="bg-white rounded-xl border border-red-200 p-6">
          <h2 className="text-lg font-semibold text-red-600 mb-4">危险操作</h2>
          <div className="flex flex-wrap gap-3">
            {project?.status === 'active' ? (
              <button
                onClick={handlePause}
                className="border border-yellow-300 text-yellow-700 px-4 py-2 rounded-lg text-sm font-medium hover:bg-yellow-50 transition-colors"
              >
                暂停项目
              </button>
            ) : (
              <button
                onClick={handleResume}
                className="border border-green-300 text-green-700 px-4 py-2 rounded-lg text-sm font-medium hover:bg-green-50 transition-colors"
              >
                恢复项目
              </button>
            )}
            <button
              onClick={handleDelete}
              className="border border-red-300 text-red-600 px-4 py-2 rounded-lg text-sm font-medium hover:bg-red-50 transition-colors"
            >
              删除项目
            </button>
          </div>
        </section>
      </main>
    </div>
  );
}

// ---- helpers ----
const inputClass = 'w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500';

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="block text-sm font-medium text-gray-700 mb-1">{label}</label>
      {children}
    </div>
  );
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return <h3 className="text-sm font-semibold text-gray-500 uppercase tracking-wide pt-2">{children}</h3>;
}
```

- [ ] **Step 7: Verify TypeScript**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend && npx tsc --noEmit 2>&1
```

Expected: No errors.

- [ ] **Step 8: Verify Go build**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./...
```

Expected: No output (clean build).

- [ ] **Step 9: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add \
  internal/config/config.go \
  internal/api/handlers/auth.go \
  internal/api/router.go \
  config.yaml \
  config.yaml.example \
  frontend/src/app/projects/
git commit -m "feat: add project settings page + TG bind endpoint (POST /api/v1/me/tg-bind)"
```

---

## Task 6: Run log page

**Files:**
- Create: `frontend/src/app/projects/[id]/runs/[runId]/page.tsx`

The run log page fetches `GET /api/v1/projects/:id/runs/:runId` and polls every 10s while `status === 'running'`.

- [ ] **Step 1: Create the run log page**

```tsx
// frontend/src/app/projects/[id]/runs/[runId]/page.tsx
'use client';

import { useEffect, useRef, useState } from 'react';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import AuthGuard from '@/components/AuthGuard';
import { apiFetch } from '@/lib/api';
import type { User, AgentRunDetail, SingleResponse } from '@/lib/types';

export default function RunLogPage() {
  return <AuthGuard>{(user) => <RunLog user={user} />}</AuthGuard>;
}

function RunLog({ user: _user }: { user: User }) {
  const { id, runId } = useParams<{ id: string; runId: string }>();
  const [run, setRun] = useState<AgentRunDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const outputRef = useRef<HTMLPreElement>(null);

  const fetchRun = async () => {
    try {
      const res = await apiFetch<SingleResponse<AgentRunDetail>>(
        `/api/v1/projects/${id}/runs/${runId}`,
      );
      setRun(res.data);
    } finally {
      setLoading(false);
    }
  };

  // Initial load
  useEffect(() => {
    fetchRun();
  }, [id, runId]); // eslint-disable-line

  // 10s polling while running
  useEffect(() => {
    if (!run || run.status !== 'running') return;
    const timer = setInterval(fetchRun, 10_000);
    return () => clearInterval(timer);
  }, [run?.status]); // eslint-disable-line

  // Auto-scroll output to bottom
  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight;
    }
  }, [run?.output]);

  const statusColor = (s: string) => {
    if (s === 'success') return 'bg-green-100 text-green-700';
    if (s === 'running') return 'bg-blue-100 text-blue-700 animate-pulse';
    if (s === 'failed') return 'bg-red-100 text-red-700';
    return 'bg-gray-100 text-gray-600';
  };

  const fmtDate = (iso?: string) =>
    iso ? new Date(iso).toLocaleString('zh-CN') : '—';

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="w-8 h-8 border-4 border-blue-500 border-t-transparent rounded-full animate-spin" />
      </div>
    );
  }

  if (!run) {
    return (
      <div className="min-h-screen flex items-center justify-center text-red-500">
        运行记录不存在
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-gray-900 text-gray-100 flex flex-col">
      {/* Header */}
      <header className="bg-gray-800 border-b border-gray-700 px-6 py-4 flex items-center gap-4">
        <Link
          href={`/projects/${id}?tab=runs`}
          className="text-gray-400 hover:text-gray-200 text-sm"
        >
          ← 运行列表
        </Link>
        <span className="text-gray-600">/</span>
        <span className="font-mono text-sm">{run.agent_type}-agent · run #{run.id}</span>
        <span className={`ml-auto text-xs px-2 py-0.5 rounded-full font-medium ${statusColor(run.status)}`}>
          {run.status}
        </span>
      </header>

      {/* Meta */}
      <div className="bg-gray-800 border-b border-gray-700 px-6 py-3 text-xs text-gray-400 flex gap-6">
        <span>开始: {fmtDate(run.started_at)}</span>
        {run.finished_at && <span>结束: {fmtDate(run.finished_at)}</span>}
        <span>Config v{run.config_version}</span>
        {run.status === 'running' && (
          <span className="text-blue-400 animate-pulse">⟳ 10s 自动刷新</span>
        )}
      </div>

      {/* Output */}
      <pre
        ref={outputRef}
        className="flex-1 p-6 text-sm font-mono text-green-300 overflow-auto whitespace-pre-wrap break-words"
        style={{ maxHeight: 'calc(100vh - 120px)' }}
      >
        {run.output ?? '（暂无输出）'}
      </pre>
    </div>
  );
}
```

- [ ] **Step 2: Verify TypeScript**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend && npx tsc --noEmit 2>&1
```

Expected: No errors.

- [ ] **Step 3: Full build check**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend && npm run build 2>&1 | tail -30
```

Expected: Build succeeds. Pages listed:
- `/` — SSG (static)
- `/login` — SSG (static)
- `/dashboard` — dynamic (CSR)
- `/projects/[id]` — dynamic (CSR)
- `/projects/[id]/settings` — dynamic (CSR)
- `/projects/[id]/runs/[runId]` — dynamic (CSR)

- [ ] **Step 4: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add frontend/src/app/projects/
git commit -m "feat: add run log page (CSR + 10s polling while running)"
```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Task |
|---|---|
| 落地页 (SSG) — product intro + GitHub login | Task 2 ✅ |
| Dashboard (CSR + 30s polling) — project list + notification badge | Task 3 ✅ |
| 项目详情 (CSR) — Issue / PR / Backlog lists paginated | Task 4 ✅ |
| 项目设置 (CSR) — config edit, AI Runner, TG bind | Task 5 ✅ |
| 运行日志 (CSR + 10s polling) — agent_run output | Task 6 ✅ |
| 截图代理 — Go API (already implemented in Phase 5) | N/A ✅ |

**Auth flow check:**
- AuthGuard calls `GET /api/v1/me` → 401 redirects to `/login` ✅
- GitHub OAuth button links to `/api/v1/auth/github` ✅
- Backend callback redirects to `/dashboard` ✅

**API consistency check:**
- All types in `types.ts` match the Go response structs ✅
- `PagedResponse<T>` matches `{ data: T[], pagination: { page, per_page, total } }` ✅
- `SingleResponse<T>` matches `{ data: T }` ✅

**Pagination:** All four list tabs use `per_page=20` matching the spec ✅

**TG bind:** Added `TGBotUsername` config field + `POST /api/v1/me/tg-bind` backend endpoint + frontend button ✅

**No placeholders found** — all code blocks are complete and executable.

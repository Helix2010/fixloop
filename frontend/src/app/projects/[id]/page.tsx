'use client';

import { Suspense, useCallback, useEffect, useState } from 'react';
import { useParams, useSearchParams, useRouter } from 'next/navigation';
import Link from 'next/link';
import AuthGuard from '@/components/AuthGuard';
import Pagination from '@/components/Pagination';
import { Spinner, PageSpinner, Empty } from '@/components/ui';
import { apiFetch, ApiError } from '@/lib/api';
import { fmtDate, fmtTimeAgo, fmtDuration, runStatusColor, runStatusLabel, issueStatusLabel, issueStatusColor, prStatusLabel, prStatusColor, projectStatusLabel } from '@/lib/utils';
import type {
  User,
  Project,
  Issue,
  PR,
  BacklogItem,
  AgentRun,
  ProjectAgent,
  PagedResponse,
  SingleResponse,
} from '@/lib/types';

export default function ProjectDetailPage() {
  return (
    <Suspense>
      <AuthGuard>{(user) => <ProjectDetail user={user} />}</AuthGuard>
    </Suspense>
  );
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

  if (loading) return <PageSpinner />;
  if (error || !project) {
    return (
      <div className="min-h-screen flex items-center justify-center text-red-500">
        {error || '项目不存在'}
      </div>
    );
  }

  const tabs: { key: Tab; label: string }[] = [
    { key: 'issues', label: '工单' },
    { key: 'prs', label: '拉取请求' },
    { key: 'backlog', label: '测试背板' },
    { key: 'runs', label: '运行日志' },
  ];

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-5xl mx-auto px-6 py-4 flex items-center gap-3">
          <Link href="/dashboard" className="text-gray-400 hover:text-gray-600 text-sm">← 控制台</Link>
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
        <div className="bg-white border border-gray-200 rounded-xl p-4 mb-6 flex items-center gap-4 text-sm text-gray-600">
          <span className="font-medium text-gray-900">{project.github.owner}/{project.github.repo}</span>
          {project.test?.staging_url && <span className="text-blue-500">{project.test.staging_url}</span>}
          <span className={`ml-auto px-2 py-0.5 rounded-full text-xs font-semibold ${
            project.status === 'active' ? 'bg-green-100 text-green-700' :
            project.status === 'paused' ? 'bg-yellow-100 text-yellow-700' :
            'bg-red-100 text-red-700'
          }`}>{projectStatusLabel(project.status)}</span>
        </div>

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

        {tab === 'issues' && <IssuesTab projectId={id} project={project} />}
        {tab === 'prs' && <PRsTab projectId={id} project={project} />}
        {tab === 'backlog' && <BacklogTab projectId={id} />}
        {tab === 'runs' && <RunsTab projectId={id} />}
      </div>
    </div>
  );
}

function IssuesTab({ projectId, project }: { projectId: string; project: Project }) {
  const [issues, setIssues] = useState<Issue[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);

  const { owner, repo } = project.issue_tracker;

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

  if (loading) return <Spinner />;
  if (issues.length === 0) return <Empty text="暂无工单" />;

  return (
    <div>
      <div className="bg-white rounded-xl border border-gray-200 divide-y divide-gray-100">
        {issues.map((i) => {
          const ghUrl = `https://github.com/${owner}/${repo}/issues/${i.github_number}`;
          return (
            <div key={i.id} className={`px-5 py-4 flex items-start justify-between gap-4 ${i.status === 'closed' ? 'opacity-60' : ''}`}>
              <div className="flex-1 min-w-0">
                <a
                  href={ghUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={`font-medium text-sm truncate block hover:underline ${i.status === 'closed' ? 'line-through text-gray-400' : 'text-gray-900'}`}
                >
                  {i.title}
                </a>
                <p className="text-xs text-gray-400 mt-0.5">
                  <a href={ghUrl} target="_blank" rel="noopener noreferrer" className="hover:underline">
                    #{i.github_number}
                  </a>
                  {' '}· 尝试 {i.fix_attempts} 次 · {i.status === 'closed' ? `关闭于 ${fmtDate(i.closed_at)}` : fmtDate(i.created_at)}
                </p>
              </div>
              <span className={`text-xs px-2 py-0.5 rounded-full font-medium whitespace-nowrap ${issueStatusColor[i.status] ?? 'bg-gray-100 text-gray-600'}`}>
                {issueStatusLabel(i.status)}
              </span>
            </div>
          );
        })}
      </div>
      <Pagination page={page} total={total} perPage={20} onChange={load} />
    </div>
  );
}

function PRsTab({ projectId, project }: { projectId: string; project: Project }) {
  const [prs, setPRs] = useState<PR[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);

  const { owner, repo } = project.issue_tracker;

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
  if (prs.length === 0) return <Empty text="暂无拉取请求" />;

  return (
    <div>
      <div className="bg-white rounded-xl border border-gray-200 divide-y divide-gray-100">
        {prs.map((pr) => {
          const ghUrl = `https://github.com/${owner}/${repo}/pull/${pr.github_number}`;
          return (
            <div key={pr.id} className="px-4 py-3 flex items-start gap-3 hover:bg-gray-50/60 transition-colors">
              {/* Status icon */}
              <div className="flex-shrink-0 mt-0.5">
                {pr.status === 'merged' ? (
                  <svg className="w-5 h-5 text-purple-500" viewBox="0 0 16 16" fill="currentColor">
                    <path d="M5.45 5.154A4.25 4.25 0 0 0 9.25 7.5h1.378a2.251 2.251 0 1 1 0 1.5H9.25A5.734 5.734 0 0 1 5 7.123v3.505a2.25 2.25 0 1 1-1.5 0V5.372a2.25 2.25 0 1 1 1.95-.218ZM4.25 13.5a.75.75 0 1 0 0-1.5.75.75 0 0 0 0 1.5Zm8.5-4.5a.75.75 0 1 0 0-1.5.75.75 0 0 0 0 1.5ZM5 3.25a.75.75 0 1 0 0 .005V3.25Z" />
                  </svg>
                ) : pr.status === 'open' ? (
                  <svg className="w-5 h-5 text-green-500" viewBox="0 0 16 16" fill="currentColor">
                    <path d="M1.5 3.25a2.25 2.25 0 1 1 3 2.122v5.256a2.251 2.251 0 1 1-1.5 0V5.372A2.25 2.25 0 0 1 1.5 3.25Zm5.677-.177L9.573.677A.25.25 0 0 1 10 .854V2.5h1A2.5 2.5 0 0 1 13.5 5v5.628a2.251 2.251 0 1 1-1.5 0V5a1 1 0 0 0-1-1h-1v1.646a.25.25 0 0 1-.427.177L7.177 3.427a.25.25 0 0 1 0-.354ZM3.75 2.5a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Zm0 9.5a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Zm8.25.75a.75.75 0 1 0 1.5 0 .75.75 0 0 0-1.5 0Z" />
                  </svg>
                ) : (
                  <svg className="w-5 h-5 text-gray-400" viewBox="0 0 16 16" fill="currentColor">
                    <path d="M3.25 1A2.25 2.25 0 0 1 4 5.372v5.256a2.25 2.25 0 1 1-1.5 0V5.372A2.251 2.251 0 0 1 3.25 1Zm9.5 14a2.25 2.25 0 1 1 0-4.5 2.25 2.25 0 0 1 0 4.5ZM3.25 2.5a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Zm0 9.5a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Zm9.5 0a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Z" />
                  </svg>
                )}
              </div>

              {/* Content */}
              <div className="flex-1 min-w-0">
                <a
                  href={ghUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-sm font-medium text-gray-900 leading-snug hover:underline block truncate"
                >
                  {pr.title || pr.branch}
                </a>
                <p className="text-xs text-gray-500 mt-0.5">
                  <a href={ghUrl} target="_blank" rel="noopener noreferrer" className="hover:underline">
                    #{pr.github_number}
                  </a>
                  {pr.status === 'merged' && pr.merged_by
                    ? <> 由 <span className="font-medium">{pr.merged_by}</span> 于 {fmtTimeAgo(pr.merged_at)} 合并</>
                    : pr.status === 'open'
                    ? <> {fmtTimeAgo(pr.created_at)} 开启 · {pr.branch}</>
                    : <> {prStatusLabel(pr.status)} · {fmtDate(pr.created_at)}</>
                  }
                </p>
              </div>

              {/* Status badge */}
              <span className={`flex-shrink-0 text-xs font-medium px-2 py-0.5 rounded-full ${prStatusColor[pr.status] ?? 'bg-gray-100 text-gray-600'}`}>
                {prStatusLabel(pr.status)}
              </span>
            </div>
          );
        })}
      </div>
      <Pagination page={page} total={total} perPage={20} onChange={load} />
    </div>
  );
}

function BacklogTab({ projectId }: { projectId: string }) {
  const [items, setItems] = useState<BacklogItem[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [statusFilter, setStatusFilter] = useState<'pending' | 'ignored'>('pending');
  const [loading, setLoading] = useState(true);

  const load = useCallback(async (p: number) => {
    setLoading(true);
    try {
      const res = await apiFetch<PagedResponse<BacklogItem>>(
        `/api/v1/projects/${projectId}/backlog?status=${statusFilter}&page=${p}&per_page=20`,
      );
      setItems(res.data);
      setTotal(res.pagination.total);
      setPage(p);
    } finally {
      setLoading(false);
    }
  }, [projectId, statusFilter]);

  useEffect(() => { load(1); }, [load]);

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
            onClick={() => setStatusFilter(s)}
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
          <Pagination page={page} total={total} perPage={20} onChange={load} />
        </div>
      )}
    </div>
  );
}

const AGENT_TYPES = ['explore', 'fix', 'master', 'plan'] as const;


const AGENT_DESCRIPTIONS: Record<string, { label: string; desc: string; icon: string }> = {
  explore: { label: 'Explore', desc: '扫描代码库，发现潜在问题并创建工单', icon: '🔍' },
  fix:     { label: 'Fix',     desc: '取优先级最高的工单，自动修复并提交 PR', icon: '🔧' },
  master:  { label: 'Master',  desc: '审核待合并 PR，通过后自动合并关闭工单', icon: '✅' },
  plan:    { label: 'Plan',    desc: '分析项目现状，规划下一步功能与改进方向', icon: '📋' },
};
const RUN_STATUSES = ['running', 'success', 'failed', 'skipped', 'abandoned'] as const;

const AGENT_COLORS: Record<string, string> = {
  explore: 'bg-violet-100 text-violet-700',
  fix:     'bg-blue-100 text-blue-700',
  master:  'bg-amber-100 text-amber-700',
  plan:    'bg-teal-100 text-teal-700',
  generic: 'bg-pink-100 text-pink-700',
};


function RunStatusIcon({ status }: { status: string }) {
  if (status === 'running')
    return <span className="w-2 h-2 rounded-full bg-blue-400 animate-pulse inline-block" />;
  if (status === 'success')
    return <svg className="w-4 h-4 text-green-500" viewBox="0 0 16 16" fill="currentColor"><path d="M13.78 4.22a.75.75 0 0 1 0 1.06l-7.25 7.25a.75.75 0 0 1-1.06 0L2.22 9.28a.75.75 0 0 1 1.06-1.06L6 10.94l6.72-6.72a.75.75 0 0 1 1.06 0Z"/></svg>;
  if (status === 'failed')
    return <svg className="w-4 h-4 text-red-500" viewBox="0 0 16 16" fill="currentColor"><path d="M3.72 3.72a.75.75 0 0 1 1.06 0L8 6.94l3.22-3.22a.75.75 0 1 1 1.06 1.06L9.06 8l3.22 3.22a.75.75 0 1 1-1.06 1.06L8 9.06l-3.22 3.22a.75.75 0 0 1-1.06-1.06L6.94 8 3.72 4.78a.75.75 0 0 1 0-1.06Z"/></svg>;
  return <span className="w-2 h-2 rounded-full bg-gray-300 inline-block" />;
}

function RunsTab({ projectId }: { projectId: string }) {
  const [runs, setRuns] = useState<AgentRun[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [triggering, setTriggering] = useState<Record<string, 'running' | 'done'>>({});
  const [agentFilter, setAgentFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const [latestByAgent, setLatestByAgent] = useState<Record<string, AgentRun>>({});
  const [agentsByType, setAgentsByType] = useState<Record<string, ProjectAgent>>({});

  useEffect(() => {
    apiFetch<{ data: ProjectAgent[] }>(`/api/v1/projects/${projectId}/agents`)
      .then((r) => {
        const byType: Record<string, ProjectAgent> = {};
        for (const a of r.data) byType[a.agent_type] = a;
        setAgentsByType(byType);
      })
      .catch(() => {});
  }, [projectId]);

  const load = useCallback(async (p: number, agent: string, status: string) => {
    setLoading(true);
    try {
      const params = new URLSearchParams({ page: String(p), per_page: '20' });
      if (agent) params.set('agent_type', agent);
      if (status) params.set('status', status);
      const res = await apiFetch<PagedResponse<AgentRun>>(
        `/api/v1/projects/${projectId}/runs?${params}`,
      );
      setRuns(res.data);
      setTotal(res.pagination.total);
      setPage(p);
      // Sync latest-by-agent from the unfiltered first page if no filters active
      if (!agent && !status && p === 1) {
        const byAgent: Record<string, AgentRun> = {};
        for (const run of res.data) {
          if (!byAgent[run.agent_type]) byAgent[run.agent_type] = run;
        }
        setLatestByAgent(byAgent);
      }
    } finally {
      setLoading(false);
    }
  }, [projectId]);

  useEffect(() => { load(1, agentFilter, statusFilter); }, [load, agentFilter, statusFilter]);

  const setAgent = (v: string) => setAgentFilter((prev) => prev === v ? '' : v);
  const setStatus = (v: string) => setStatusFilter(v);

  const triggerRun = async (alias: string) => {
    if (triggering[alias]) return;
    setTriggering((prev) => ({ ...prev, [alias]: 'running' }));
    try {
      await apiFetch(`/api/v1/projects/${projectId}/runs`, {
        method: 'POST',
        body: JSON.stringify({ alias }),
      });
      setTriggering((prev) => ({ ...prev, [alias]: 'done' }));
      setTimeout(() => {
        setTriggering((prev) => { const n = { ...prev }; delete n[alias]; return n; });
        load(1, agentFilter, statusFilter);
      }, 2000);
    } catch {
      setTriggering((prev) => { const n = { ...prev }; delete n[alias]; return n; });
    }
  };

  return (
    <div className="space-y-4">
      {/* Manual trigger cards */}
      <div className="bg-white border border-gray-200 rounded-xl overflow-hidden">
        <div className="px-4 py-3 border-b border-gray-100 flex items-center gap-2">
          <span className="text-xs font-semibold text-gray-500 uppercase tracking-wide">手动触发</span>
          <span className="text-xs text-gray-400">立即运行一次，不影响自动调度</span>
        </div>
        <div className="grid grid-cols-2 sm:grid-cols-4 divide-x divide-y sm:divide-y-0 divide-gray-100">
          {AGENT_TYPES.map((t) => {
            const info = AGENT_DESCRIPTIONS[t];
            const state = triggering[t];
            const latest = latestByAgent[t];
            const agent = agentsByType[t];
            const isEnabled = agent?.enabled ?? true;
            return (
              <div key={t} className="px-4 py-3 flex flex-col gap-2">
                <div className="flex items-center justify-between gap-1.5">
                  <div className="flex items-center gap-1.5">
                    <span className="text-base leading-none">{info.icon}</span>
                    <span className={`text-xs font-semibold px-1.5 py-0.5 rounded capitalize ${AGENT_COLORS[t] ?? 'bg-gray-100 text-gray-600'}`}>{info.label}</span>
                  </div>
                  <span className={`text-xs px-1.5 py-0.5 rounded-full font-medium ${isEnabled ? 'bg-green-50 text-green-600' : 'bg-gray-100 text-gray-400'}`}>
                    {isEnabled ? '运行中' : '已停用'}
                  </span>
                </div>
                <p className="text-xs text-gray-500 leading-relaxed">{info.desc}</p>

                {/* Latest run status */}
                {latest ? (
                  <div className={`flex items-start gap-1.5 text-xs rounded-md px-2 py-1.5 ${
                    latest.status === 'running'  ? 'bg-blue-50 text-blue-700' :
                    latest.status === 'success'  ? 'bg-green-50 text-green-700' :
                    latest.status === 'failed'   ? 'bg-red-50 text-red-600' :
                    latest.status === 'skipped'  ? 'bg-amber-50 text-amber-700' :
                                                   'bg-gray-50 text-gray-500'
                  }`}>
                    <span className="flex-shrink-0 mt-0.5">
                      {latest.status === 'running' ? (
                        <span className="w-2 h-2 rounded-full bg-blue-400 animate-pulse inline-block" />
                      ) : latest.status === 'success' ? (
                        <svg className="w-3 h-3" viewBox="0 0 16 16" fill="currentColor"><path d="M13.78 4.22a.75.75 0 0 1 0 1.06l-7.25 7.25a.75.75 0 0 1-1.06 0L2.22 9.28a.75.75 0 0 1 1.06-1.06L6 10.94l6.72-6.72a.75.75 0 0 1 1.06 0Z"/></svg>
                      ) : latest.status === 'failed' ? (
                        <svg className="w-3 h-3" viewBox="0 0 16 16" fill="currentColor"><path d="M3.72 3.72a.75.75 0 0 1 1.06 0L8 6.94l3.22-3.22a.75.75 0 1 1 1.06 1.06L9.06 8l3.22 3.22a.75.75 0 1 1-1.06 1.06L8 9.06l-3.22 3.22a.75.75 0 0 1-1.06-1.06L6.94 8 3.72 4.78a.75.75 0 0 1 0-1.06Z"/></svg>
                      ) : (
                        <span className="w-2 h-2 rounded-full bg-current inline-block opacity-60" />
                      )}
                    </span>
                    <span className="min-w-0">
                      <span className="font-medium">{runStatusLabel(latest.status)}</span>
                      <span className="opacity-70"> · {fmtTimeAgo(latest.started_at)}</span>
                      {latest.summary && (
                        <span className="block opacity-70 truncate" title={latest.summary}>{latest.summary}</span>
                      )}
                    </span>
                  </div>
                ) : (
                  <div className="text-xs text-gray-300 px-2 py-1.5">暂无运行记录</div>
                )}

                <button
                  onClick={() => triggerRun(t)}
                  disabled={!!state}
                  className={`mt-auto text-xs px-3 py-1.5 rounded-lg font-medium border transition-all self-start ${
                    state === 'done'
                      ? 'bg-green-50 text-green-700 border-green-300 cursor-default'
                      : state === 'running'
                      ? 'bg-gray-800 text-white border-gray-800 cursor-wait'
                      : 'bg-white text-gray-600 border-gray-200 hover:border-gray-800 hover:text-gray-800 hover:bg-gray-50'
                  }`}
                >
                  {state === 'done' ? (
                    <span className="flex items-center gap-1.5">✓ 已触发</span>
                  ) : state === 'running' ? (
                    <span className="flex items-center gap-1.5">
                      <svg className="w-3 h-3 animate-spin" viewBox="0 0 24 24" fill="none"><circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4"/><path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"/></svg>
                      触发中
                    </span>
                  ) : '▶ 立即运行'}
                </button>
              </div>
            );
          })}
        </div>
      </div>

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-2">
        {/* Agent type filter pills */}
        <div className="flex items-center gap-1 bg-white border border-gray-200 rounded-lg p-1">
          <button
            onClick={() => setAgent('')}
            className={`text-xs px-2.5 py-1 rounded-md font-medium transition-colors ${agentFilter === '' ? 'bg-gray-800 text-white' : 'text-gray-500 hover:text-gray-800'}`}
          >全部</button>
          {AGENT_TYPES.map((t) => (
            <button
              key={t}
              onClick={() => setAgent(agentFilter === t ? '' : t)}
              className={`text-xs px-2.5 py-1 rounded-md font-medium transition-colors capitalize ${agentFilter === t ? AGENT_COLORS[t] + ' ring-1 ring-inset ring-current/20' : 'text-gray-500 hover:text-gray-800'}`}
            >{t}</button>
          ))}
        </div>

        {/* Status filter */}
        <select
          value={statusFilter}
          onChange={(e) => setStatus(e.target.value)}
          className="text-xs border border-gray-200 rounded-lg px-2.5 py-1.5 bg-white text-gray-600 focus:outline-none focus:ring-1 focus:ring-gray-300"
        >
          <option value="">所有状态</option>
          {RUN_STATUSES.map((s) => (
            <option key={s} value={s}>{runStatusLabel(s)}</option>
          ))}
        </select>
      </div>

      {/* List */}
      {loading ? <Spinner /> : runs.length === 0 ? <Empty text="暂无运行记录" /> : (
        <div>
          <div className="bg-white rounded-xl border border-gray-200 divide-y divide-gray-100">
            {runs.map((r) => (
              <Link
                key={r.id}
                href={`/projects/${projectId}/runs/${r.id}`}
                className="px-4 py-3 flex items-center gap-3 hover:bg-gray-50/70 transition-colors"
              >
                {/* Status icon */}
                <div className="flex-shrink-0 w-5 flex items-center justify-center">
                  <RunStatusIcon status={r.status} />
                </div>

                {/* Agent type badge */}
                <span className={`flex-shrink-0 text-xs font-semibold px-2 py-0.5 rounded-md capitalize ${AGENT_COLORS[r.agent_type] ?? 'bg-gray-100 text-gray-600'}`}>
                  {r.agent_type}
                </span>

                {/* Time + summary */}
                <div className="flex-1 min-w-0">
                  <div className="flex items-baseline gap-2 min-w-0">
                    <span className="text-sm text-gray-700 whitespace-nowrap">{fmtTimeAgo(r.started_at)}</span>
                    <span className="text-xs text-gray-400 whitespace-nowrap">{fmtDate(r.started_at)}</span>
                    {r.summary && (
                      <span className="text-xs text-gray-500 truncate min-w-0">· {r.summary}</span>
                    )}
                  </div>
                </div>

                {/* Duration */}
                {r.finished_at && (
                  <span className="flex-shrink-0 text-xs text-gray-400 tabular-nums">
                    {fmtDuration(r.started_at, r.finished_at)}
                  </span>
                )}
                {r.status === 'running' && (
                  <span className="flex-shrink-0 text-xs text-blue-500 tabular-nums animate-pulse">
                    {fmtDuration(r.started_at)}
                  </span>
                )}

                {/* Status label */}
                <span className={`flex-shrink-0 text-xs px-2 py-0.5 rounded-full font-medium ${runStatusColor(r.status, r.status === 'running')}`}>
                  {runStatusLabel(r.status)}
                </span>
              </Link>
            ))}
          </div>
          <Pagination page={page} total={total} perPage={20} onChange={(p) => load(p, agentFilter, statusFilter)} />
        </div>
      )}
    </div>
  );
}

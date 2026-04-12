'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import AuthGuard from '@/components/AuthGuard';
import { PageSpinner } from '@/components/ui';
import { apiFetch } from '@/lib/api';
import { fmtDate, runStatusColor, runStatusLabel } from '@/lib/utils';
import type { User, AgentRunDetail, SingleResponse } from '@/lib/types';

export default function RunLogPage() {
  return <AuthGuard>{(user) => <RunLog user={user} />}</AuthGuard>;
}

function RunLog({ user: _user }: { user: User }) {
  const { id, runId } = useParams<{ id: string; runId: string }>();
  const [run, setRun] = useState<AgentRunDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const outputRef = useRef<HTMLPreElement>(null);

  const fetchRun = useCallback(async () => {
    try {
      const res = await apiFetch<SingleResponse<AgentRunDetail>>(
        `/api/v1/projects/${id}/runs/${runId}`,
      );
      setRun(res.data);
    } catch {
      // apiFetch handles 401; ignore transient poll errors
    }
  }, [id, runId]);

  useEffect(() => {
    fetchRun().finally(() => setLoading(false));
  }, [fetchRun]);

  useEffect(() => {
    if (!run || run.status !== 'running') return;
    const timer = setInterval(fetchRun, 10_000);
    return () => clearInterval(timer);
  }, [fetchRun, run?.status]);

  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight;
    }
  }, [run?.output]);

  if (loading) return <PageSpinner />;

  if (!run) {
    return (
      <div className="min-h-screen flex items-center justify-center text-red-500">
        运行记录不存在
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-gray-900 text-gray-100 flex flex-col">
      <header className="bg-gray-800 border-b border-gray-700 px-6 py-4 flex items-center gap-4">
        <Link
          href={`/projects/${id}?tab=runs`}
          className="text-gray-400 hover:text-gray-200 text-sm"
        >
          ← 运行列表
        </Link>
        <span className="text-gray-600">/</span>
        <span className="font-mono text-sm">{run.agent_type}-agent · run #{run.id}</span>
        <span className={`ml-auto text-xs px-2 py-0.5 rounded-full font-medium ${runStatusColor(run.status, true)}`}>
          {runStatusLabel(run.status)}
        </span>
      </header>

      <div className="bg-gray-800 border-b border-gray-700 px-6 py-3 text-xs text-gray-400 flex gap-6">
        <span>开始: {fmtDate(run.started_at)}</span>
        {run.finished_at && <span>结束: {fmtDate(run.finished_at)}</span>}
        <span>配置版本 v{run.config_version}</span>
        {run.status === 'running' && (
          <span className="text-blue-400 animate-pulse">⟳ 10s 自动刷新</span>
        )}
      </div>

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

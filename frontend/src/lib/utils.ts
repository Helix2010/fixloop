export function fmtTimeAgo(iso?: string): string {
  if (!iso) return '—';
  const diff = Date.now() - new Date(iso).getTime();
  const m = Math.floor(diff / 60000);
  if (m < 1) return '刚刚';
  if (m < 60) return `${m} 分钟前`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} 小时前`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d} 天前`;
  return fmtDate(iso);
}

export function fmtDate(iso?: string): string {
  if (!iso) return '—';
  return new Date(iso).toLocaleString('zh-CN', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function runStatusColor(status: string, pulse = false): string {
  if (status === 'success') return 'bg-green-100 text-green-700';
  if (status === 'running') return `bg-blue-100 text-blue-700${pulse ? ' animate-pulse' : ''}`;
  if (status === 'failed') return 'bg-red-100 text-red-700';
  return 'bg-gray-100 text-gray-600';
}

const issueStatusLabels: Record<string, string> = {
  open: '待处理', fixing: '修复中', 'needs-human': '需人工', closed: '已关闭',
};
export const issueStatusLabel = (s: string) => issueStatusLabels[s] ?? s;

const prStatusLabels: Record<string, string> = {
  open: '开放', merged: '已合并', closed: '已关闭',
};
export const prStatusLabel = (s: string) => prStatusLabels[s] ?? s;

const runStatusLabels: Record<string, string> = {
  running: '运行中', success: '成功', failed: '失败', skipped: '跳过', abandoned: '已放弃',
};
export const runStatusLabel = (s: string) => runStatusLabels[s] ?? s;

export function fmtBytes(bytes: number): string {
  if (bytes >= 1e9) return (bytes / 1e9).toFixed(1) + ' GB';
  if (bytes >= 1e6) return (bytes / 1e6).toFixed(1) + ' MB';
  if (bytes >= 1e3) return (bytes / 1e3).toFixed(1) + ' KB';
  return bytes + ' B';
}

export function fmtDuration(start?: string, end?: string): string {
  if (!start) return '';
  const ms = (end ? new Date(end) : new Date()).getTime() - new Date(start).getTime();
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${s % 60}s`;
}

export function formatSchedule(minutes: number): string {
  if (minutes >= 1440 && minutes % 1440 === 0) return `每 ${minutes / 1440} 天`;
  if (minutes >= 60 && minutes % 60 === 0) return `每 ${minutes / 60} 小时`;
  if (minutes >= 60) return `每 ${(minutes / 60).toFixed(1)} 小时`;
  return `每 ${minutes} 分钟`;
}

const projectStatusLabels: Record<string, string> = {
  active: '运行中', paused: '已暂停', error: '异常',
};
export const projectStatusLabel = (s: string) => projectStatusLabels[s] ?? s;

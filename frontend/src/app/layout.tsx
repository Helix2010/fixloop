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

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

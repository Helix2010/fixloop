# FixLoop 项目架构

## 概述

FixLoop 是一个 AI 驱动的自动修复平台。用户接入 GitHub 仓库后，系统会自动发现 Bug、生成修复 PR、跑验收测试，全流程无人工干预。

---

## 部署架构

```
外部请求
    │
    ▼
Nginx (80/443)
    ├── /api/*  → Go 后端 (localhost:8080)
    └── /*      → Next.js 前端 (localhost:3100)

Go 后端
    ├── HTTP API (Gin)
    ├── 调度器 (gocron)
    │     ├── Explore Agent  每10分钟
    │     ├── Fix Agent      每30分钟
    │     ├── Master Agent   每10分钟
    │     └── Plan Agent     每周一凌晨
    └── Telegram Bot (可选)

外部依赖
    ├── MySQL
    ├── GitHub API
    ├── Vercel API（可选）
    └── Cloudflare R2（截图存储，可选）
```

---

## 目录结构

```
fixloop/
├── cmd/server/main.go          # 程序入口
├── config.yaml                 # 运行配置（不入 git）
├── config.yaml.example         # 配置模板
├── go.mod / go.sum
│
├── backend/
│   └── migrations/             # SQL 迁移文件（golang-migrate）
│       ├── 000001_initial_schema.up.sql
│       └── 000002_add_column_comments.up.sql
│
├── frontend/                   # Next.js 14 前端
│   └── src/
│       ├── app/                # App Router 页面
│       ├── components/         # 公共组件
│       └── lib/                # API 客户端、类型定义、工具函数
│
├── internal/                   # Go 后端核心逻辑（外部不可导入）
│   ├── agentrun/               # Agent 运行记录（启动、完成、崩溃恢复）
│   ├── api/                    # HTTP 层
│   │   ├── handlers/           # 各业务路由处理器
│   │   ├── middleware/         # 认证、限流、权限校验
│   │   ├── response/           # 统一响应格式封装
│   │   └── router.go           # 路由注册
│   ├── agents/                 # 四类 AI Agent 实现
│   │   ├── explore/            # Explore Agent：UI 探索 → 发现 Bug → 创建 Issue
│   │   ├── fix/                # Fix Agent：认领 Issue → 调用 AI → 推送修复 PR
│   │   ├── master/             # Master Agent：PR review → 合并 → 验收测试
│   │   └── plan/               # Plan Agent：每周生成测试场景补充 Backlog
│   ├── config/                 # 配置加载（config.yaml → Config struct）
│   ├── crypto/                 # AES-256-GCM 加解密、SSH 密钥工具
│   ├── db/                     # 数据库连接 + 迁移执行
│   ├── github/                 # GitHub REST API 客户端
│   ├── gitops/                 # 本地 git 操作（clone、branch、commit、push）
│   ├── notify/                 # 通知写入（DB → Telegram 推送）
│   ├── playwright/             # Playwright 封装：执行验收测试、截图
│   ├── runner/                 # AI Runner 接口 + 实现（Claude CLI / Aider）
│   ├── scheduler/              # gocron 调度器封装，管理各项目 4 类定时任务
│   ├── ssrf/                   # SSRF 防护（阻止访问内网 IP）
│   ├── storage/                # Cloudflare R2 截图上传（S3 兼容）
│   ├── tgbot/                  # Telegram Bot：收 Issue 报告、推送通知
│   └── vercel/                 # Vercel API 客户端（等待部署、查询状态）
│
└── docs/
    ├── architecture.md         # 本文件
    └── specs/                  # 功能设计文档
```

---

## 核心流程

### 1. Explore Agent（每 10 分钟）

```
克隆/更新仓库
    → Playwright 打开 Staging URL，随机跑 UI 交互
    → 截图上传 R2
    → AI 分析截图，判断是否存在 Bug
    → 有 Bug → 在 GitHub Issue Tracker 开 Issue + 写入 issues 表
```

### 2. Fix Agent（每 30 分钟）

```
从 issues 表取一条 open 状态的 Issue（乐观锁防并发）
    → 解密 SSH Key / PAT
    → EnsureRepo：clone 或 git fetch 仓库
    → 切到 fix/issue-{N} 分支
    → 构建 Prompt（Issue 标题 + 目录树 + 历史失败记录）
    → 调用 runner.Run()（claude CLI 或 aider）
    → AI 直接修改工作区文件
    → 有变更 → git commit + push
    → 创建 PR（首次）或追加 commit（重试）
    → 请求 Copilot Review
    → 失败 ≥ 3 次 → 标记 needs-human，Telegram 通知
```

### 3. Master Agent（每 10 分钟，比 Explore 错开 5 分钟）

```
找最旧的 open PR
    → 读取 PR Reviews
    → APPROVED 或 Copilot COMMENTED → 可合并
    → Squash Merge
    → 等待 Vercel 部署（可选）
    → Playwright 验收测试（3 轮 seed check）
        ├── 通过 → 关闭 Issue，删除分支，Telegram 通知
        └── 失败 → 重新打开 Issue，失败 ≥ 5 次 → needs-human
```

### 4. Plan Agent（每周一凌晨，带项目 ID jitter）

```
Backlog pending < 10 条时触发
    → 读取最近 Issues 作为上下文
    → AI 生成 5 条新测试场景（JSON 数组）
    → 去重写入 backlog 表
```

---

## 数据模型（核心表）

| 表 | 用途 |
|---|---|
| `users` | 用户账号，存 Telegram chat_id |
| `projects` | 项目配置（JSON 加密存储），含 GitHub、AI、Vercel 配置 |
| `issues` | 发现的 Bug，状态机：open → fixing → closed / needs-human |
| `prs` | AI 创建的修复 PR，状态：open → merged / closed |
| `backlog` | 待执行的测试场景，由 Plan Agent 生成 |
| `agent_runs` | 每次 Agent 运行记录（类型、状态、配置版本） |
| `agent_run_outputs` | Agent 运行日志（ANSI 脱色后存储） |
| `notifications` | 通知队列，tgbot 异步消费推送 Telegram |
| `sessions` | 用户登录 session（server-side） |

---

## AI Runner 抽象

```go
// internal/runner/runner.go
type Runner interface {
    Run(ctx context.Context, repoPath, prompt string) (string, error)
}
```

两种实现：

| 实现 | 说明 |
|---|---|
| `ClaudeCLIRunner` | 调用 `claude --print --dangerously-skip-permissions`，stdin 传入 Prompt，直接在 repoPath 修改文件 |
| `AiderRunner` | 调用 `aider`，适配 OpenAI 兼容接口（如 DeepSeek），支持自定义 api_base |

由 `runner.New(aiRunner, model, apiBase, apiKey)` 工厂函数按项目配置选择。

---

## 安全设计

- **敏感信息加密**：所有密钥（SSH、GitHub PAT、AI API Key、Vercel Token）均以 AES-256-GCM 加密后 hex 编码存入 `projects.config`，解密密钥来自服务器环境变量。
- **SSRF 防护**：`internal/ssrf` 在配置保存时和 Agent 运行时两次校验 staging_url，拒绝指向内网 IP 的请求。
- **API 认证**：所有 `/api/v1/*` 接口经 `middleware/auth` 校验 session，跨项目资源访问经 `middleware/ownership` 二次校验所有权。
- **单实例锁**：进程启动时用文件 flock 防止重复运行。

---

## 本地开发

```bash
# 后端
go build -o /tmp/fixloop-server ./cmd/server/
/tmp/fixloop-server

# 前端
cd frontend
npm install
npm run dev     # 开发模式 http://localhost:3100
```

修改代码后必须重新构建并重启服务，详见 [CLAUDE.md](../CLAUDE.md)。

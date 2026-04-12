# FixLoop 产品设计文档

> 版本：v1.0
> 日期：2026-04-11
> 状态：已定稿（经 20 轮深度 Review）

---

## 一、产品定位

**FixLoop** 是一个 AI 驱动的 CI/CD 自动化 SaaS 平台。

用户接入 GitHub 仓库后，系统自动运行 AI Agent 发现 bug、提交修复 PR、部署验收，形成完整闭环。用户通过 Telegram Bot 管理和监控所有项目。

**一句话：接入你的 GitHub，选你的 AI，自动修 bug。**

---

## 二、目标用户

- 小团队（1-5 人），没有专职 QA
- 同时维护多个项目
- 想把 AI 接入现有 CI/CD 流程
- 技术背景，能配置 GitHub Token / Vercel

---

## 三、核心差异点

| 差异点 | 说明 |
|--------|------|
| 聚焦 CI/CD 闭环 | 专做"测试发现→自动修复→上线验收"链路 |
| 5 分钟接入 | GitHub OAuth 登录，填 repo + staging URL 即可启动 |
| TG 管控 | 移动端友好，随时查看状态、触发 agent、接收告警 |
| 多 AI 支持 | 支持 Claude、Gemini、aider（含国内 AI） |

---

## 四、商业模式

- **冷启动期**：免费不限量，积累种子用户
- **增长期**：按项目数/月收费（待验证后设计）
- **规模化**：托管 AI Key 作为付费功能，用户也可 BYOK

MVP 期间 Claude CLI 服务器统一登录（共享账号），未来支持 BYOK 实现 rate limit 隔离。

---

## 五、核心用户旅程

```
访问落地页
  → "Login with GitHub"（GitHub OAuth + state CSRF 验证）
  → 首次登录 → 引导创建第一个项目
      填写：GitHub repo / Staging URL / AI 配置 / GitHub Fine-grained PAT
  → 系统初始化：
      生成 SSH Ed25519 密钥对（私钥 AES 加密存 DB）
      调用 GitHub API 注册 Deploy Key（read_only: false）
      种入 3 条 seed 测试场景到 backlog
      注册 gocron 调度 job
  → 绑定 Telegram（强推）
      生成一次性 token（10 分钟有效）
      跳转 tg://resolve?domain=fixloopbot&start={token}
      Bot 收到 /start {token} → 验证 → 写入 users.tg_chat_id
  → 日常使用：TG 收通知 + 发命令 / Web Dashboard 看历史
```

**目标：整个 onboarding < 5 分钟**

未绑定 TG 时，所有通知写入 `notifications` 表，Web Dashboard 顶部显示未读通知徽章作为兜底。

---

## 六、Agent 架构

```
explore-agent   每 10 分钟   Playwright 跑 UI 测试，发现 bug 开 Issue，自动扩充 backlog
fix-agent       每 30 分钟   取最高优先级 open issue，AI 修复，提 PR
master-agent    每 10 分钟   检查 PR review，merge，部署，SHA 验证，验收，关闭 Issue
plan-agent      每周一       分析测试覆盖缺口，生成新测试场景写入 backlog
```

**Agents 之间不直接通信，数据库是唯一共享状态。**

### Issue 状态流转

```
open → fixing（fix-agent 乐观锁 UPDATE，rows_affected=0 则跳过）
fixing → closed（master-agent 验收通过）
fixing → needs-human（fix_attempts >= 3）
closed → open（验收失败回滚，accept_failures += 1）
fixing → open（超时 2h 自动重置，检查是否有关联 open PR）
```

**fix_attempts**：fix-agent 每次创建/更新 PR +1。
**accept_failures**：master-agent 验收失败独立计数，>= 5 触发 needs-human，防止 staging 抖动误伤。

Issue 关闭后，关联的 `failed` backlog 场景自动重置为 `pending`（master-agent 负责）。

### explore-agent auto-expand

发现 bug 开 Issue 后，调用 AI 生成 3-5 条衍生测试场景写入 backlog（`source='auto_expand'`）。

backlog pending 场景低于 5 条时，explore-agent 运行结束后自动触发一次 plan-agent（异步）。

### backlog 场景选取规则

`priority ASC` → `last_tested_at ASC`（NULL 优先）。每项目 backlog 上限 200 条 pending，超出替换最旧 `tested` 场景。title fingerprint（小写去标点 hash）去重。

### 初始 seed 场景（内置执行器，不依赖 AI）

项目创建时自动插入 3 条（`source='seed'`）：
1. 首页可访问（HTTP 200，无 JS crash）
2. 页面无 console ERROR
3. 核心交互元素可见（body 非空，无 500 页面）

seed 场景走内置执行器（硬编码 3 种检查），普通场景走 AI 生成的 Playwright 模板。

---

## 七、多 AI 支持

### Runner 接口

```go
type AgentRunner interface {
    Run(prompt string, maxTurns int) (string, error)
}

type ClaudeRunner struct{ Model string }
type GeminiRunner struct{ Model, APIKey string }
type AiderRunner  struct{ Model, APIBase, APIKey string }
```

### 支持的 Runner

| Runner | 覆盖范围 | 认证方式 |
|--------|---------|---------|
| `claude` | Claude Code CLI | claude.ai OAuth（服务器统一登录） |
| `gemini` | Gemini CLI | API Key |
| `aider` | 所有 OpenAI 兼容接口 | API Key + Base URL |

### 国内 AI（via aider）

| 厂商 | 模型 | API Base |
|------|------|---------|
| DeepSeek | deepseek-coder | https://api.deepseek.com |
| 通义千问 | qwen-coder-plus | https://dashscope.aliyuncs.com/compatible-mode/v1 |
| 豆包 | doubao-coder | https://ark.cn-beijing.volces.com/api/v3 |
| Kimi | moonshot-v1 | https://api.moonshot.cn/v1 |
| 智谱 | glm-4 | https://open.bigmodel.cn/api/paas/v4 |

### Prompt 模板

MVP 期间 prompt 硬编码在 `internal/agent/prompts/`，按 agent 类型分文件，与代码一同版本控制。

**Prompt 注入防护**（所有外部内容用结构化分隔符隔离）：
```
=== SYSTEM INSTRUCTIONS ===
{agent 指令}

=== ISSUE CONTENT (untrusted) ===
Title: {issue_title}
Body:
{issue_body}
=== END ISSUE CONTENT ===
```

**fix-agent prompt 上下文**：Issue title + body + 仓库目录树（深度 3）+ 前次失败评论。

---

## 八、技术栈

| 层 | 技术 | 说明 |
|----|------|------|
| 后端 | Go + Gin | 单体：API + TG Bot + Scheduler + Agent Runner |
| 前端 | Next.js | 落地页（SSG）+ Dashboard（CSR），同 Nginx 反代 |
| 数据库 | MySQL 8.0 utf8mb4 | InnoDB 外键，月分区 |
| 调度 | gocron v2（SingletonMode）| 每项目独立 job，tag 管理生命周期 |
| TG Bot | go-telegram-bot-api | 长轮询，offset 存 system_config |
| Auth | GitHub OAuth + JWT | httpOnly cookie，7 天，SameSite=Strict |
| 截图存储 | Cloudflare R2（私有）| aws-sdk-go-v2，代理端点访问（永不过期） |
| 日志 | Go slog JSON | stdout，systemd journald 收集 |
| 错误监控 | Sentry（sentry-go）| ERROR+unexpected 自动上报 |
| DB 迁移 | golang-migrate | `backend/migrations/`，启动自动 migrate up |
| 部署 | 单台 VPS + Nginx | 前后端同域，systemd 管理 |

**前后端同域约束**：Next.js 和 Go API 均通过 Nginx 代理到 `fixloop.com`，`SameSite=Strict` 完整生效，无需 CORS 配置。

---

## 九、系统架构

```
┌─────────────────────────────────────────┐
│              单台 VPS                    │
│                                         │
│  ┌──────────────┐   ┌─────────────────┐ │
│  │   Go 服务     │   │   Next.js       │ │
│  │   :8080      │   │   :3000         │ │
│  │  - REST API  │   │  落地页(SSG)     │ │
│  │  - TG Bot    │   │  Dashboard(CSR) │ │
│  │  - Scheduler │   └────────┬────────┘ │
│  └──────┬───────┘            │          │
│         │         ┌──────────▼────────┐ │
│         └────────►│      Nginx        │ │
│                   │  /api → :8080     │ │
│                   │  / → :3000        │ │
│                   │  SSL 终止         │ │
│                   └───────────────────┘ │
│  ┌─────────────────────────────────┐    │
│  │  MySQL 8.0                      │    │
│  └─────────────────────────────────┘    │
└─────────────────────────────────────────┘

外部：Cloudflare R2 / GitHub API / Vercel API / Telegram Bot API
```

---

## 十、Go 包结构

```
cmd/server/           # main.go 启动入口
internal/
  api/                # Gin handlers + middleware（ownership、rate limit、auth）
  scheduler/          # gocron job 注册与生命周期管理
  agent/              # agent runner（Claude/Gemini/aider）
    prompts/          # prompt 模板文件
    playwright/       # Playwright 执行引擎（方案 D）
  tgbot/              # TG Bot 命令处理
  db/                 # 连接池 + migrations
  storage/            # R2 操作
  github/             # GitHub API 客户端（含重试）
  vercel/             # Vercel API 客户端
  crypto/             # AES-256-GCM 加密/解密
backend/migrations/   # golang-migrate SQL 文件
```

---

## 十一、数据库 Schema

```sql
CREATE TABLE users (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY,
    github_id    BIGINT NOT NULL UNIQUE,
    github_login VARCHAR(64) NOT NULL,
    tg_chat_id   BIGINT,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at   DATETIME
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE projects (
    id             BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id        BIGINT NOT NULL,
    name           VARCHAR(64) NOT NULL,
    config         JSON NOT NULL,
    config_version INT NOT NULL DEFAULT 1,
    status         ENUM('active','paused','error') NOT NULL DEFAULT 'active',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at     DATETIME,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE KEY uq_user_project_name (user_id, name, deleted_at),
    INDEX idx_user_status (user_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE issues (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    project_id      BIGINT NOT NULL,
    github_number   INT NOT NULL,
    title           VARCHAR(512) NOT NULL,
    title_hash      CHAR(40) NOT NULL,          -- SHA1(title)，用于去重
    priority        TINYINT NOT NULL DEFAULT 2, -- 1=P1, 2=P2, 3=P3
    status          ENUM('open','fixing','closed','needs-human') NOT NULL DEFAULT 'open',
    fix_attempts    INT NOT NULL DEFAULT 0,
    accept_failures INT NOT NULL DEFAULT 0,
    fixing_since    DATETIME,
    closed_at       DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    UNIQUE KEY uq_project_issue (project_id, github_number),
    UNIQUE KEY uq_project_title (project_id, title_hash),
    INDEX idx_project_status_priority (project_id, status, priority, fix_attempts)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE prs (
    id            BIGINT AUTO_INCREMENT PRIMARY KEY,
    project_id    BIGINT NOT NULL,
    issue_id      BIGINT,                        -- NULL 表示手动 PR，不在自动化范围
    github_number INT NOT NULL,
    branch        VARCHAR(128) NOT NULL,
    status        ENUM('open','merged','closed') NOT NULL DEFAULT 'open',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    merged_at     DATETIME,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE SET NULL,
    UNIQUE KEY uq_project_pr (project_id, github_number),
    INDEX idx_project_status (project_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE pr_reviews (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY,
    pr_id        BIGINT NOT NULL,
    reviewer     VARCHAR(64) NOT NULL,
    state        ENUM('pending','commented','approved','changes_requested') NOT NULL DEFAULT 'pending',
    review_round INT NOT NULL DEFAULT 1,
    reviewed_at  DATETIME,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (pr_id) REFERENCES prs(id) ON DELETE CASCADE,
    INDEX idx_pr_round (pr_id, review_round)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE backlog (
    id               BIGINT AUTO_INCREMENT PRIMARY KEY,
    project_id       BIGINT NOT NULL,
    related_issue_id BIGINT,
    title            VARCHAR(512) NOT NULL,
    title_hash       CHAR(40) NOT NULL,
    description      TEXT,
    test_steps       JSON,                       -- 结构化 JSON，用于生成 Playwright 模板
    scenario_type    ENUM('ui','api') NOT NULL DEFAULT 'ui',
    priority         TINYINT NOT NULL DEFAULT 2,
    status           ENUM('pending','tested','failed','skipped','ignored') NOT NULL DEFAULT 'pending',
    source           ENUM('plan','auto_expand','seed') NOT NULL DEFAULT 'plan',
    last_tested_at   DATETIME,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (related_issue_id) REFERENCES issues(id) ON DELETE SET NULL,
    UNIQUE KEY uq_project_scenario (project_id, title_hash),
    INDEX idx_project_status_priority (project_id, status, priority, last_tested_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- agent_run 输出单独存储，主表保持窄行
CREATE TABLE agent_runs (
    id          BIGINT AUTO_INCREMENT,
    project_id  BIGINT,                         -- ON DELETE SET NULL，允许项目删除后保留审计记录
    agent_type  ENUM('explore','fix','master','plan') NOT NULL,
    status      ENUM('running','success','failed','skipped','abandoned') NOT NULL DEFAULT 'running',
    config_version INT,
    started_at  DATETIME NOT NULL,
    finished_at DATETIME,
    PRIMARY KEY (id, started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
PARTITION BY RANGE (YEAR(started_at) * 100 + MONTH(started_at)) (
    PARTITION p202604 VALUES LESS THAN (202605),
    PARTITION p202605 VALUES LESS THAN (202606),
    PARTITION p_future VALUES LESS THAN MAXVALUE
);
-- 保留 3 个月，每月 1 日 04:00 DROP 旧分区

CREATE TABLE agent_run_outputs (
    run_id  BIGINT NOT NULL,
    output  MEDIUMTEXT,                         -- ANSI stripped，列表查询不 SELECT 此表
    PRIMARY KEY (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE notifications (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id    BIGINT NOT NULL,
    project_id BIGINT,
    type       VARCHAR(64) NOT NULL,
    content    TEXT NOT NULL,
    read_at    DATETIME,
    tg_sent    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    INDEX idx_user_read (user_id, read_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- 保留 90 天，与 agent_runs 清理一起执行

CREATE TABLE system_config (
    key_name   VARCHAR(64) PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- 初始 key：tg_last_update_id, partition_last_cleaned, feature_flags
```

---

## 十二、项目配置字段（config JSON）

```json
{
  "github": {
    "owner": "myorg",
    "repo": "my-app",
    "pat": "<AES encrypted>",
    "fix_base_branch": "dev"
  },
  "issue_tracker": {
    "owner": "myorg",
    "repo": "my-app"
  },
  "ssh_private_key": "<AES encrypted>",
  "deploy_key_id": 12345,
  "vercel": {
    "project_id": "prj_xxx",
    "token": "<AES encrypted>",
    "staging_target": "preview"
  },
  "test": {
    "staging_url": "https://my-app-staging.vercel.app",
    "staging_auth_type": "basic",
    "staging_auth": "<AES encrypted: {username, password}>"
  },
  "ai_runner": "claude",
  "ai_model": "claude-opus-4-6",
  "ai_api_base": "",
  "ai_api_key": "<AES encrypted>",
  "fix_disabled": false
}
```

---

## 十三、GitHub PAT 权限要求

| 权限 | 级别 | 用途 |
|------|------|------|
| Contents | Read & Write | clone / push fix branch |
| Issues | Read & Write | 创建 / 关闭 Issue |
| Pull requests | Read & Write | 创建 PR / 请求 review |
| Administration | Read & Write | 注册 Deploy Key |

> org 仓库需 org 管理员授权。onboarding 页面提供图文说明。
> 需 GitHub Copilot 订阅（$10/月）支持自动 review。提供降级方案：无 Copilot 时等待任意人工 APPROVED。

---

## 十四、安全设计

### SSRF 防护（双重 DNS 验证）

```go
// 保存时验证
func validateStagingURL(rawURL string) error {
    u, _ := url.Parse(rawURL)
    if u.Scheme != "https" { return errors.New("只允许 HTTPS") }
    return checkNoPrivateIP(u.Hostname())
}

// 每次 Playwright 启动前再验证（防 DNS rebinding）
func validateAtCallTime(hostname string) error {
    return checkNoPrivateIP(hostname) // 重新 DNS 解析
}
```

### Playwright 网络隔离 + 模板注入防护

```javascript
// 只允许 staging 域及子域
page.route('**', route => {
    const host = new URL(route.request().url()).hostname;
    if (host === baseDomain || host.endsWith('.' + baseDomain)) {
        route.continue();
    } else { route.abort(); }
});

// 模板参数必须 JSON 序列化，防止 JS 注入
const script = `await page.goto(${JSON.stringify(stagingUrl)});`;
```

### AES-256-GCM + Key Versioning

密文格式：`{key_id:1byte}{nonce:12bytes}{ciphertext}`，支持密钥轮换不停服。密钥存 VPS 环境变量 `FIXLOOP_AES_KEY`，不入库。

### Issue 状态乐观锁

```sql
UPDATE issues SET status='fixing', fixing_since=NOW()
WHERE id=? AND status='open';
-- rows_affected=0 则跳过此 issue
```

### 其他

- GitHub OAuth state 参数验证（CSRF 防护）
- SQL 参数化查询（无拼接）
- JWT httpOnly + Secure + SameSite=Strict，7 天
- Ownership middleware：所有项目接口验证 `project.user_id = current_user_id`
- API rate limit：per-user 20 req/s，burst 50（Go middleware）
- GitHub secondary rate limit：检测 403 body 关键词，等待 60s 后重试

---

## 十五、认证与会话

- GitHub OAuth 唯一登录入口，`GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` 存环境变量
- JWT payload：`{"sub": user_id, "login": "github_login", "exp": unix_ts}`
- 前端 CSR 首次加载调用 `GET /api/v1/me`，401 跳转 `/login?redirect={path}`
- GitHub API 401（用户撤销 OAuth）→ 清除 session → 重新登录

---

## 十六、Git 操作规范

### SSH Deploy Key 自动化

```
项目创建：
  1. 生成 Ed25519 密钥对（内存，私钥加密存 DB）
  2. POST /repos/{owner}/{repo}/keys {"read_only": false}
  3. 存 deploy_key_id 到 config
  4. 失败 → 项目初始化失败，提示用户检查 PAT 权限
```

### SSH known_hosts 初始化（服务器部署时执行一次）

```bash
ssh-keyscan github.com >> ~/.ssh/known_hosts
```

### 本地仓库策略

- 首次：`git clone --depth=1 git@github.com:{owner}/{repo}.git {local_path}`
- 后续：`git fetch origin && git reset --hard origin/{base_branch}`

### fix branch 处理

```
首次：
  git checkout -b fix/issue-{N} origin/{base_branch}
  → AI 修复 → git push origin fix/issue-{N}
  → 创建 PR（无变更则不 push 不创建 PR，在 Issue 评论解释）

重试（PR 已存在）：
  git checkout fix/issue-{N}
  git rebase origin/{base_branch}
  → AI 继续修复 → git push --force-with-lease
  （更新已有 PR，不创建新 PR）

merge 后：
  DELETE /repos/{owner}/{repo}/git/refs/heads/fix/issue-{N}
```

禁止 `--force` 推送主干（main/master/dev）；fix 分支自身更新允许 `--force-with-lease`。

---

## 十七、agent_run 生命周期

### 子进程管理（进程组 kill）

```go
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgrp: true}
// 超时后：
syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
```

### 输出处理

存库前 strip ANSI（`github.com/acarl005/stripansi`），输出存 `agent_run_outputs`，列表查询只 SELECT `agent_runs` 主表（无 output 列）。

### Zombie 清理（服务启动时）

```sql
UPDATE agent_runs SET status='abandoned', finished_at=NOW()
WHERE status='running' AND started_at < NOW() - INTERVAL 1 HOUR;
```

### Panic 恢复

```go
defer func() {
    if r := recover(); r != nil {
        slog.Error("agent panic", "run_id", runID, "err", r, "unexpected", true)
        sentry.CaptureException(fmt.Errorf("panic: %v", r))
        updateRunStatus(runID, "failed")
    }
}()
```

### 并发限制

| 限制 | 上限 |
|------|------|
| Claude 全局并发 | 3 |
| 其他 runner 每用户 | 2 |
| 每项目每日 runs | 30（滑动 24h：`started_at > NOW() - INTERVAL 24 HOUR`）|
| goroutine 队列 | 50 |

TG `/run` 手动触发同样受日限额约束。

---

## 十八、Playwright 执行机制（方案 D）

`test_steps` 为结构化 JSON，explore-agent 渲染成固定模板 .spec.js 执行：

```json
[
  {"action": "goto", "url": "{staging_url}"},
  {"action": "expect_visible", "selector": "h1"},
  {"action": "expect_no_console_error"}
]
```

模板渲染时所有外部参数必须 `JSON.stringify()` 序列化防注入。每次 run 使用独立 `--user-data-dir /tmp/pw-{run_id}/`，run 后 `defer os.RemoveAll(profileDir)`。服务启动时清理 `/tmp/pw-*/` 中超 2h 的僵尸目录。

### Issue 优先级赋值

| 优先级 | 触发条件 |
|--------|---------|
| P1 | 页面崩溃 / HTTP 5xx / JS uncaught exception / 核心功能不可用 |
| P2 | 功能异常但页面可用 / assertion failure on core element |
| P3 | UI 样式偏差 / 非核心元素缺失 |
| 默认 | 无法判断时 P2 |

### staging_auth 格式

```json
{"type": "basic", "username": "u", "password": "p"}
{"type": "header", "name": "Authorization", "value": "Bearer xxx"}
{"type": "cookie", "name": "session", "value": "yyy"}
```

`staging_url` 为 null 时跳过整个 run（`status='skipped'`）。

### 并发保护

同一项目同一时刻只允许一个 Playwright 实例（explore 或 master 验收），通过 `SELECT GET_LOCK('pw_{project_id}', 0)` 实现。

---

## 十九、截图存储

- R2 bucket：`fixloop-screenshots-prod`
- 路径：`{user_id}/{project_id}/{run_id}/{scenario_id}_{step}_{ts}.png`
- 访问方式：代理端点（永不过期）

```
GET /api/v1/projects/:id/screenshots/:run_id/:filename
  → 验证 project 归属当前用户
  → 从 R2 stream 返回
```

GitHub Issue body 嵌入代理端点 URL，截图永久可访问。

---

## 二十、GitHub Issue body 模板

```markdown
## Bug Report

**Scenario:** {scenario_title}
**Staging URL:** {staging_url}
**Error Type:** {timeout|assertion|console_error}
**Error Message:**
```
{error_message}
```

**Screenshot:** {proxy_url}

**Test Steps:**
{test_steps}

**Detected at:** {timestamp}
```

---

## 二十一、PR 创建规范

**命名**：`fix: {issue_title} (#{issue_number})`

**Body 模板**：
```markdown
## Fix for #{issue_number}: {issue_title}

**Root cause:** {Claude 的诊断}
**Changes:** {修改了哪些文件及原因}

Closes {owner}/{repo}#{issue_number}
```

**Review 流程**：
1. 创建 PR → 请求 Copilot review
2. master-agent 每 10 分钟轮询（ETag 条件请求）
3. Copilot COMMENTED → squash merge
4. 24h 未完成 → TG 告警，等待人工
5. TG `/merge <project> <pr>` 强制 merge（bypass review）

**master-agent 多 PR 时**：按 `prs.created_at ASC`，每次 run 只处理一个 PR。

---

## 二十二、Vercel 部署验证

```
squash merge → 获取 merge_commit_sha
  → 等待 Vercel 部署（每 30s 轮询，最多 20 次 / 10 分钟）
      READY → 验证 deployment.meta.githubCommitSha == merge_commit_sha
            → SHA 不匹配则继续等待下一次部署
      ERROR → TG 告警"构建失败"，Issue 回滚 open
      超时  → TG 告警"部署超时"，Issue 保持 fixing
  → Playwright 验收（Playwright 并发锁）
      通过 → 关闭 Issue（事务：UPDATE prs + UPDATE issues + UPDATE backlog failed→pending）
      失败 → 事务回滚：UPDATE issues status=open, accept_failures+=1
```

---

## 二十三、关键事务边界

以下操作必须原子（MySQL 事务）：

| 操作 | 涉及表 |
|------|-------|
| fix-agent 开始处理 | issues(fixing) + prs(insert) |
| master-agent merge 成功 | prs(merged) + issues(closed) + backlog(failed→pending) |
| master-agent 验收失败 | issues(open, accept_failures+1) + prs(closed) |
| 手动 `/reset` | issues(open, fix_attempts=0) |

---

## 二十四、TG Bot 设计

### 通知

| 事件 | 内容 |
|------|------|
| Issue 开启 | `🐛 [project] 发现新 bug #N: {title}` |
| PR merge | `✅ [project] PR #N 已 merge` |
| 验收通过 | `🎉 [project] Issue #N 已关闭，线上验收通过` |
| 修复失败 | `⚠️ [project] Issue #N 修复失败 {N} 次，需人工介入` |
| 构建失败 | `🔥 [project] Vercel 构建失败，PR #{N}` |
| 部署超时 | `⏱ [project] 部署超时，请检查 Vercel` |
| Copilot 卡住 | `🔔 [project] PR #{N} 等待 review 超过 24h` |

消息超 4096 字符时截断，附 Dashboard 链接。

### 命令

| 命令 | 功能 |
|------|------|
| `/status` | 所有项目状态总览 |
| `/issues <project>` | 查看 open issues |
| `/run fix <project>` | 手动触发 fix agent |
| `/run explore <project>` | 手动触发 explore agent |
| `/run plan <project>` | 手动触发 plan agent |
| `/merge <project> <pr>` | 强制 merge |
| `/reset <project> <issue>` | 重置 Issue 为 open |
| `/pause <project>` | 暂停所有 agents |
| `/resume <project>` | 恢复 agents |

多项目用户命令必须带 project 名称，缺省时列出项目供选择。群组消息直接忽略（只处理私聊）。

### offset 持久化

每条 update 处理完后立即写 `system_config.tg_last_update_id`，已处理 update_id 幂等检查防重复。

---

## 二十五、Web 功能页面

| 页面 | 渲染 | 功能 |
|------|------|------|
| 落地页 | SSG | 产品介绍 + GitHub 登录 |
| Dashboard | CSR + 30s 轮询 | 所有项目状态，未读通知徽章 |
| 项目详情 | CSR | Issue / PR / Backlog 列表（分页 20 条） |
| 项目设置 | CSR | 配置编辑，AI Runner，TG 绑定 |
| 运行日志 | CSR + 10s 轮询 | agent_run 输出，运行中实时刷新 |
| 截图代理 | Go API | 验权后 stream R2 截图 |

---

## 二十六、REST API 端点

```
POST   /api/v1/auth/github/callback
GET    /api/v1/me
DELETE /api/v1/me

POST   /api/v1/projects
GET    /api/v1/projects
GET    /api/v1/projects/:id
PATCH  /api/v1/projects/:id
DELETE /api/v1/projects/:id
POST   /api/v1/projects/:id/pause
POST   /api/v1/projects/:id/resume

GET    /api/v1/projects/:id/issues
GET    /api/v1/projects/:id/prs
GET    /api/v1/projects/:id/backlog
PATCH  /api/v1/projects/:id/backlog/:sid   -- 设置 ignored 等状态

GET    /api/v1/projects/:id/runs
GET    /api/v1/projects/:id/runs/:rid
POST   /api/v1/projects/:id/runs           -- 手动触发

GET    /api/v1/notifications
POST   /api/v1/notifications/read-all

GET    /api/v1/projects/:id/screenshots/:run_id/:file
GET    /health
```

**响应格式**：
```json
// 成功
{"data": {...}, "pagination": {"page": 1, "total": 42}}
// 错误
{"error": {"code": "PROJECT_NOT_FOUND", "message": "项目不存在"}}
```

**JWT payload**：`{"sub": 123, "login": "github_login", "exp": 1234567890}`

---

## 二十七、gocron Job 生命周期

```go
// 服务启动时：注册所有 active 项目的 job
projects := db.FindActiveProjects()
for _, p := range projects {
    scheduler.RegisterProjectJobs(p.ID)
}

// 创建项目时（API handler 内）：
scheduler.RegisterProjectJobs(newProject.ID)

// 暂停项目时：
scheduler.RemoveByTags(fmt.Sprintf("project-%d", id))

// 删除项目时：
scheduler.RemoveByTags(fmt.Sprintf("project-%d", id))
// 然后 DB cascade delete
```

---

## 二十八、GitHub API 客户端

统一封装重试逻辑：
- 429：读 `Retry-After` header，等待后重试，最多 3 次
- 5xx：指数退避（1s/2s/4s），最多 3 次
- 403 body 含 "secondary rate limit"：等待 60s 后重试
- 超过重试次数：返回错误，由调用方决定是否告警

---

## 二十九、MySQL 连接池

```go
db.SetMaxOpenConns(25)
db.SetMaxIdleConns(10)
db.SetConnMaxLifetime(5 * time.Minute)
```

---

## 三十、多租户隔离

```
/data/projects/{user_id}/{project_id}/repo/   # 持久化 clone
/tmp/screenshots/{user_id}/{project_id}/       # 临时截图，上传 R2 后清理
/tmp/pw-{run_id}/                              # Playwright browser profile，run 后清理
```

所有 DB 查询带 `user_id` 过滤，API ownership middleware 验证项目归属。

---

## 三十一、优雅关闭

```
SIGTERM →
  1. gocron.Stop()                  -- 不触发新 job
  2. TG Bot long poll stop
  3. 等待 active goroutines（超时 5 分钟）
  4. 标记未完成 agent_run 为 abandoned
  5. db.Close()
  6. os.Exit(0)
```

---

## 三十二、单实例约束

```go
// 服务启动时获取文件锁，防止多实例
lock, err := os.OpenFile("/var/run/fixloop.pid", os.O_CREATE|os.O_RDWR, 0600)
if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
    log.Fatal("另一个 fixloop 实例已在运行")
}
```

---

## 三十三、错误监控与日志

```go
// 日志：JSON 格式，输出 stdout，systemd journald 收集
slog.Info("agent started", "project_id", pid, "agent", "fix", "run_id", rid)
slog.Error("unexpected error", "err", err, "unexpected", true) // 触发 Sentry

// Sentry：level=ERROR + unexpected=true 自动上报
// 预期错误（GitHub 429、Vercel 超时）只写日志
```

健康检查：`GET /health` → `{"status":"ok","db":"ok","tg":"connected"}`

---

## 三十四、运维与部署

### 环境变量（`/etc/fixloop.env`）

```bash
GITHUB_CLIENT_ID=xxx
GITHUB_CLIENT_SECRET=xxx
FIXLOOP_AES_KEY=<32字节 hex>
SENTRY_DSN=https://xxx@sentry.io/xxx
DATABASE_DSN=user:pass@tcp(127.0.0.1:3306)/fixloop?parseTime=true
PORT=8080
```

### TLS（Let's Encrypt）

```bash
certbot --nginx -d fixloop.com
# 自动续期
0 3 * * * certbot renew --quiet && nginx -s reload
```

### 部署流程

```bash
git pull origin main
go build -o /usr/local/bin/fixloop ./cmd/server
systemctl restart fixloop
cd frontend && npm run build && pm2 restart fixloop-web
```

### MySQL 备份

```bash
# 每日 00:30，保留 7 天
30 0 * * * mysqldump fixloop_prod | gzip | \
  aws s3 cp - s3://fixloop-backups-prod/$(date +\%Y\%m\%d).sql.gz
```

### 分区清理（每月 1 日 04:00）

```bash
0 4 1 * * mysql fixloop_prod -e "ALTER TABLE agent_runs DROP PARTITION p$(date -d '3 months ago' +\%Y\%m);"
```

### known_hosts 初始化

```bash
ssh-keyscan github.com >> ~/.ssh/known_hosts
```

### VPS 最低配置

4GB RAM / 2 vCPU / 40GB SSD（3 并发 Claude CLI ≈ 600MB + MySQL + Go + Next.js ≈ 2-3GB）。

---

## 三十五、MVP 范围

**包含：**
- GitHub OAuth 登录（state CSRF 验证）
- 单项目创建 + Deploy Key 自动注册 + Vercel 集成验证
- explore / fix / master / plan agent 调度
- Claude CLI runner（服务器统一登录）
- TG Bot 绑定 + 完整通知 + 完整命令
- Web Dashboard（项目状态 + 日志 + 通知中心 + 截图代理）
- 完整安全设计（SSRF、AES、CSRF、ownership、rate limit）
- golang-migrate DB 迁移
- Sentry 错误监控
- MySQL 日备份

**不包含（后续版本）：**
- Gemini / aider runner
- 多项目批量管理
- 用户自定义调度频率
- 计费系统
- BYOK（用户自带 AI Key）
- GitHub App（替代 PAT 的更优权限模型）
- SSE 实时推送（MVP 用轮询）

---

## 三十六、冷启动策略

1. 自己的项目先接入，跑通全流程
2. 邀请 5-10 个种子用户，收集反馈
3. 重点观察：哪个 agent 最有价值、BYOK 需求是否强烈
4. 根据数据决定付费功能方向

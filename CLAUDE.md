# FixLoop 项目宪法

## 修改代码后必须更新对应服务

代码改完不等于上线，**必须重新构建并重启对应服务**，否则改动不会生效。

### 后端（Go）

```bash
# 重新编译并重启
go build -o /tmp/fixloop-server ./cmd/server/
kill $(lsof -ti:8080) 2>/dev/null
nohup /tmp/fixloop-server > /tmp/fixloop-backend.log 2>&1 &

# 验证
curl -s http://localhost:8080/health
```

### 前端（Next.js）

```bash
cd /home/ubuntu/fy/work/fixloop/frontend

# 重新构建
npm run build

# 重启（端口 3100）—— 必须先杀掉旧进程，再启动 systemd 服务
# lsof/kill 无法覆盖 zombied 进程，用 ss 找到 PID 再 kill -9
OLD_PID=$(ss -tlnp | awk '/3100/{match($0,/pid=([0-9]+)/,a); print a[1]}')
[ -n "$OLD_PID" ] && kill -9 $OLD_PID 2>/dev/null
sudo systemctl restart fixloop-frontend

# 验证：确认新进程已启动且使用最新构建
sudo systemctl status fixloop-frontend --no-pager | grep Active
curl -s -o /dev/null -w "%{http_code}" http://localhost:3100/
```

> **⚠️ 新包验证**：重启后必须确认 `systemctl status fixloop-frontend` 显示 `active (running)`，且进程启动时间晚于构建时间。若 status 为 `activating (auto-restart)`，说明旧进程仍占用端口，重复执行 kill + restart。

### Nginx 配置变更

```bash
sudo nginx -t && sudo systemctl reload nginx
```

---

## 前端页面与组件设计

涉及前端页面新增、组件设计、布局调整时，**必须先使用 `frontend-design` skill** 进行设计，再动手写代码。

```
/frontend-design
```

该 skill 会引导完成以下流程：
1. 理解需求，明确页面结构和交互逻辑
2. 输出视觉设计方案（布局、组件拆分、交互状态）
3. 确认设计后再进入实现阶段

**适用场景：**
- 新增页面或路由
- 新增复用组件（表单、卡片、弹窗、抽屉等）
- 对现有页面做较大布局改动
- 不确定 UI 结构如何组织时

**不需要走该流程：**
- 纯文案修改（汉化、错别字）
- 样式微调（颜色、间距）
- Bug 修复（逻辑不变只修数据）

---

## 安全规范（已落地，禁止回退）

### 认证与授权

- **管理员路由**：`/api/v1/admin/*` 必须通过 `middleware.AdminOnly()` 中间件，只允许 `user_id = 1` 访问。新增管理接口时必须注册在 `admin` 路由组内。
- **JWT Cookie**：认证 cookie 名为 `fixloop_session`，仅通过 `middleware.JWTAuth()` 验证，禁止在 handler 中自行解析 token。
- **OAuth 回调重定向**：`sanitizeRedirect()` 使用白名单（`/dashboard`、`/projects`、`/admin`、`/settings`），必须返回清洗后的 `clean`（已去除 query/fragment），**不能返回原始 `path`**，防止 open-redirect 攻击。

### Token 与密钥存储

- **TG 绑定 Token**：明文 token（16字节随机）只通过 URL 下发给用户，数据库中存储 SHA-256 哈希（`util.TGBindKey(raw []byte)`）。bot.go 验证时也必须先哈希再查库，禁止明文存储或比对。
- **TG Token 频率限制**：每用户每小时最多生成 5 个绑定 token（查 `system_config` 中 `tg_bind_%` 前缀记录），超限返回 429。
- **所有密钥（PAT、SSH、API Key、Vercel Token）**：AES 加密后存入 `projects.config`，十六进制编码，解密逻辑统一走 `crypto.Decrypt()`。

### 输入校验

- **GitHub owner/repo slug**：`validateGitHubSlug()` 用正则校验（仅允许字母数字、`.`、`-`、`_`，首尾必须是字母数字，最长100字符），创建/更新项目时必须校验。
- **agent_type / status 枚举**：`ListRuns` 接口的过滤参数通过包级别 `validAgentTypes` / `validStatuses` map 校验，非法值静默忽略，禁止拼接进 SQL。
- **Webhook Token**：`validateWebhookToken()` 使用 `subtle.ConstantTimeCompare` 做常量时间比对，防止 timing attack。

### 网络与 SSRF

- **SSH 主机检查**：`StrictHostKeyChecking=accept-new`（接受新主机，拒绝已变更主机），禁止改回 `no`。
- **SSRF 防护**：所有对外 HTTP/Playwright 请求前，必须调用 `ssrf.ValidateHostname(ssrf.ExtractHostname(url))`，拒绝解析到私有 IP 的域名（防 DNS rebinding）。每次 agent 运行时**重新检查**，不能只在保存时校验。
- **外部 HTTP 请求**：必须使用带超时的 Context（`http.NewRequestWithContext`），禁止裸调 `http.Get()`。

### HTTP 安全头

全局中间件 `middleware.SecureHeaders()` 已注入以下头，禁止移除：
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `X-XSS-Protection: 1; mode=block`
- `Referrer-Policy: strict-origin-when-cross-origin`
- `Strict-Transport-Security: max-age=31536000; includeSubDomains`

### 错误信息

- handler 中禁止将内部错误（`err.Error()`）直接返回给客户端。错误用 `slog.Error` 记录，响应只返回固定 code/message。
- 部署密钥创建失败、SSH 密钥解析失败等错误，只记录日志，不透传原始错误信息。

---

## 代码复用规范（共享包）

### `internal/agents/shared/`

- **`ExceedsDailyLimit(ctx, db, projectID, agentType, limit)`**：所有 agent（explore/fix/plan/master/generic）的每日运行次数检查必须调用此函数，禁止在各 agent 中重复写 SQL。已支持手动触发（`scheduler.ForcedRunKey`）自动跳过限制。
- **`ParseAuthConfig(raw []byte)`**：Playwright staging auth 配置解析统一在此，explore 和 master 调用。
- **`CommitPushPR(...)`**：git commit + push + 创建 PR 的完整流程，fix/generic agent 复用。

### `internal/ssrf/`

- **`ExtractHostname(rawURL)`**：从 URL 字符串快速提取 hostname（strip scheme + port），供 SSRF 检查使用。
- **`ValidateHostname(hostname)`**：DNS 解析 + 私有 IP 检查。

### `internal/util/`

- **`TGBindKey(raw []byte)`**：SHA-256 哈希 → `tg_bind_<hex>` 的 system_config key，auth.go 和 bot.go 统一调用。

---

## 数据库规范

### 迁移文件命名

格式：`backend/migrations/NNNNNN_<描述>.up.sql` / `.down.sql`，序号严格递增。当前最新：`000008`。

### 已建索引（000008）

| 索引名 | 表 | 列 | 用途 |
|--------|----|----|------|
| `idx_system_config_key` | `system_config` | `key_name` | TG 绑定 token 前缀查询 |
| `idx_agent_runs_daily` | `agent_runs` | `(project_id, agent_type, started_at)` | 每日运行次数计数 |
| `idx_project_agents_proj_enabled` | `project_agents` | `(project_id, enabled)` | 调度器加载活跃 agent |

### `tg_known_chats` 表（000007）

Bot 加入的群组自动写入，`active=0` 表示已被踢出。字段：`chat_id`（PK）、`title`、`chat_type`、`active`、`updated_at`、`created_at`。

---

## 服务架构

| 服务 | 进程 | 端口 | 日志 |
|------|------|------|------|
| Go 后端 | `/tmp/fixloop-server` | 8080 | `/tmp/fixloop-backend.log` |
| Next.js 前端 | `npm run start` | 3100 | `/tmp/fixloop-frontend.log` |
| Nginx 反向代理 | systemd `nginx` | 80 / 443 | `/var/log/nginx/` |

域名：`https://dapp.predict.kim`
- `/api/*` → 后端 8080
- 其他 → 前端 3100

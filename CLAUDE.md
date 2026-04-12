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

## 服务架构

| 服务 | 进程 | 端口 | 日志 |
|------|------|------|------|
| Go 后端 | `/tmp/fixloop-server` | 8080 | `/tmp/fixloop-backend.log` |
| Next.js 前端 | `npm run start` | 3100 | `/tmp/fixloop-frontend.log` |
| Nginx 反向代理 | systemd `nginx` | 80 / 443 | `/var/log/nginx/` |

域名：`https://dapp.predict.kim`
- `/api/*` → 后端 8080
- 其他 → 前端 3100

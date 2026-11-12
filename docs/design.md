# job-chat-copilot 详细设计

- 项目：job-chat-copilot（Boss直聘半自动求职助手）
- 形态：浏览器插件（Manifest V3）+ 本地 Go 服务 + 大模型 API
- 状态：设计稿，待研发评审
- 关联：README.md

## 1. 背景与目标

目标服务是 `https://www.zhipin.com/web/geek/chat`（Boss直聘 Web 端求职者 IM）及其上下游页面（职位搜索、职位详情）。产品目标：

1. 按用户策略辅助发起"立即沟通"（Boss直聘中沟通即投递，简历随首条消息附带）
2. 监听聊天会话，对 HR 消息生成回复；**默认人工审核后发送**，可选全自动
3. LLM 通过 OpenAI 兼容协议接入，多供应商可换

### 非目标（红线）

- 不做验证码/滑块自动绕过（一律暂停并转人工）
- 不做多账号、小号矩阵
- 不做简历/职位数据抓取与倒卖
- 不逆向 `__zp_stoken__` 等私有协议签名

### 已否决的备选方案

| 方案 | 结论 | 原因 |
|---|---|---|
| 纯协议逆向（Go 直调接口） | 否决 | 需复刻 `__zp_stoken__`（重度混淆 JS 指纹），封号率极高，风控更新即失效，开源法律风险大 |
| chromedp/rod 全权托管页面操作 | 否决（仅保留 rod 为托管辅助） | 全 CDP 模拟输入更易被行为指纹识别；插件在页面内操作最自然，且用户浏览器环境即真实环境 |

## 2. 总体架构

```
┌──────────────────────────── 用户 Chrome ─────────────────────────────┐
│  zhipin.com 页面（用户正常登录）                                        │
│   └── content-script（DOM 读写、模拟点击/输入、会话监听）                │
│        ▲ DOM                                                            │
│   ┌────┴─────────┐            ┌──────────────────┐                    │
│   │ injected     │──拦截 WS/XHR│ service worker   │                   │
│   │ script(page) │  消息事件   │ (插件后台，转发桥) │                    │
│   └──────────────┘            └────────┬─────────┘                    │
└─────────────────────────────────────────┼──────────────────────────────┘
                                          │ localhost:7422 HTTP + WebSocket
┌─────────────────────────────────────────▼──────────────────────────────┐
│                   job-chat-copilot-server（Go 单二进制）                   │
│  ┌─────────────┐ ┌─────────────┐ ┌──────────────┐ ┌─────────────────┐ │
│  │ 插件网关      │ │ 任务编排      │ │ LLM 网关     │ │ 存储(SQLite)    │ │
│  │ 配对/鉴权    │ │ 投递策略     │ │ OpenAI 兼容  │ │ 会话/草稿/日志   │ │
│  │ WS 事件总线  │ │ 限速/人化间隔 │ │ 供应商可配   │ │ 简历上下文      │ │
│  └─────────────┘ └─────────────┘ └──────────────┘ └─────────────────┘ │
│  ┌─────────────┐ ┌───────────────────────────────────────┐            │
│  │ rod 托管辅助  │ │ Vue 3 控制台（go:embed，同端口静态资源）│            │
│  │ 拉起/重启浏览器│ │ 会话监控/审核队列/配置/日志             │            │
│  └─────────────┘ └───────────────────────────────────────┘            │
└────────────────────────────────────────────────────────────────────────┘
```

### 职责分工（关键决策）

| 能力 | 承担方 | 理由 |
|---|---|---|
| 页面 DOM 读写、点击、输入 | 插件 content-script | MAIN_WORLD 注入 + 真实事件派发，与人工操作同源，最难被识别 |
| 聊天新消息获取 | injected script 拦截 WebSocket/XHR + DOM 兜底轮询 | 拦截法消息结构化最好；DOM 法抗改版，两者互补校验 |
| 登录 | 用户手动（扫码/手机号） | 绝不自动化登录，登录态属于浏览器自身 |
| 任务编排、限速、审核队列、LLM 调用、存储 | Go 服务 | 跨页面会话的持久状态与策略不适合放在插件（MV3 随时被杀） |
| 拉起带插件的 Chrome、掉线重启、监督截图 | rod（CDP attach 到同一浏览器） | rod 仅做"托管与监督"，不做页面操作，规避 CDP 模拟输入的指纹问题 |
| 人工审核 UI | Vue 控制台（独立页面） | 审核场景信息密度高，不适合塞进插件 popup |

### 通信链路

1. **injected script → content-script**：`window.postMessage`（page 世界与隔离世界桥）
2. **content-script ↔ service worker**：`chrome.runtime` 消息
3. **service worker ↔ Go 服务**：`localhost` WebSocket（长连接，事件双向推送）+ 少量 HTTP
4. **Vue 控制台 ↔ Go 服务**：同源 HTTP REST + WebSocket（复用同一事件总线）
5. **首次配对**：Go 服务启动时生成一次性配对码，控制台展示；用户在插件 options 页粘贴/点击确认后建立绑定，之后凭本地 token 鉴权（防止本机其他页面冒连 localhost 网关；WS 握手校验 `Origin` 白名单为 `chrome-extension://<id>` 与控制台自身）

## 3. 浏览器插件设计

### 3.1 结构（TypeScript + Vite 构建）

```
extension/
  manifest.json          # MV3
  src/
    background/          # service worker：WS 客户端、消息路由、重连
    content/
      index.ts           # content-script（ISOLATED world）：DOM 操作执行器
      inject.ts          # 注入 MAIN_WORLD：hook WebSocket/XHR，捕获聊天消息
    options/             # 配对页：显示插件状态、绑定本地服务
    popup/               # 状态速览：连接状态、今日投递数、待审数量
```

### 3.2 manifest 要点

- `host_permissions: ["https://www.zhipin.com/*"]`
- `permissions: ["storage"]`（最小集，不要 tabs/cookies 等敏感权限）
- content-scripts 匹配 `https://www.zhipin.com/*`，`run_at: document_idle`
- CSP 默认，不引入远程代码

### 3.3 DOM 操作原则

所有对页面的写操作（点击"立即沟通"、输入消息、发送）必须：

1. 通过真实事件序列派发（`pointerdown/pointerup/click`、`input`、`keydown/keyup`），不用 `el.click()` 之类的合成捷径
2. 操作间加人化抖动间隔（由 Go 服务统一下发节奏，插件只执行）
3. 每次操作前后做页面状态断言（目标元素存在、当前确实在预期页面），失败即上报并暂停任务

### 3.4 选择器维护策略

zhipin 页面 class 名混淆且随构建变化，选择器统一收敛到插件内一份 `selectors.ts`，策略为：优先稳定属性（`data-*`、语义文本、aria）→ 结构路径兜底；选择器失配属于**预期内的高频故障**，失配时上报结构快照（脱敏）到 Go 服务日志，便于快速修补。文档中明确：**不承诺跟随 zhipin 前端每次改版，选择器修复以最佳 effort 维护。**

## 4. Go 本地服务设计

### 4.1 模块划分

```
server/
  cmd/job-chat-copilot/     # 入口：启动 HTTP/WS、拉起浏览器、embed 前端
  internal/
    gateway/              # 插件 WS 网关：配对、鉴权、事件路由
    orchestrator/         # 任务编排：投递任务、应答任务、状态机
    guard/                # 风控护栏：限速、日上限、人化间隔、暂停条件
    llm/                  # LLM 网关：OpenAI 兼容客户端、prompt 组装、重试
    rule/                 # 高频问题规则库（正则 + 预设答案）
    review/               # 审核队列：草稿生成、确认/编辑/驳回、发送下发
    store/                # SQLite（modernc.org/sqlite 纯 Go 驱动，免 CGO）
    browser/              # rod：拉起/重启 Chrome、加载插件、监督截图
  web/                    # Vue 3 控制台源码，构建产物 embed
```

### 4.2 任务编排

**投递任务**（单一状态机，串行执行，绝不并发多职位）：

```
idle → 搜索页就绪 → 逐职位：解析卡片 → 策略过滤 → 打开职位 →
校验（活跃/未沟通过/符合黑名单规则）→ 点击"立即沟通" →
发送招呼语（模板+JD变量）→ 记录 → 人化间隔 → 下一个
任何一步异常/检测到验证码 → paused（通知控制台）→ 人工处理后 resume
```

**应答任务**（事件驱动）：

```
WS 拦截到新消息 → 落库 → 规则库匹配（到岗时间/薪资/地点等预设答案）
  命中 → 生成草稿（标注来源=rule）
  未命中 → LLM 生成草稿（来源=llm）
→ 进入审核队列（默认）→ 人工确认/编辑 → 下发插件发送
全自动模式：白名单类问题（rule 命中）直发，llm 草稿仍走审核
```

### 4.3 风控护栏（guard，内置不可关闭）

| 护栏 | 默认值 |
|---|---|
| 操作最小间隔 | 投递动作 ≥ 40s ± 随机 20s |
| 每日投递上限 | 50（可调低） |
| 每日 LLM 回复上限 | 200 |
| 活跃时段 | 可配置（默认 09:00–20:00），时段外任务自动暂停 |
| 验证码/滑块检测 | 页面出现验证元素特征 → 全局 paused + 控制台告警，人工处理后才恢复 |
| 登录态丢失 | 检测到登录页跳转 → paused + 告警，等待人工重新登录 |

### 4.4 LLM 网关

- 仅实现 OpenAI 兼容 `/chat/completions`（含流式），配置 `base_url` / `api_key` / `model` / `temperature`
- Prompt 组装（每用户一次配置，存 SQLite）：
  - system：求职者人设约束——**只基于简历事实回答；不知道的问题回复"确认后答复您"类话术；不主动承诺薪资数字（除非用户预设）；不透露本工具存在**
  - 上下文：简历结构化摘要 + 岗位 JD + 最近 N 条对话（默认 10 条，可配）
- 输出后处理：长度截断（聊天场景 ≤ 200 字）、去除 markdown 符号
- 失败处理：超时/限流重试 1 次，仍失败则该消息转人工在控制台手答

### 4.5 数据模型（SQLite）

```
settings(k,v)                    -- LLM 配置、护栏参数、话术模板
resume_context(id, content, ...) -- 简历结构化上下文（可多版本）
rule(id, pattern, answer, ...)   -- 高频问题规则库
job(id, job_key UNIQUE, title, company, salary, status, delivered_at, ...)
conversation(id, conv_key UNIQUE, hr_name, job_id, last_msg_at, ...)
message(id, conv_id, direction, content, msg_type, raw_hint, sent_at)
draft(id, conv_id, source ENUM(rule,llm,human), content, status
      ENUM(pending,approved,rejected,sent,failed), created_at, sent_at)
task_log(id, task_type, action, detail, created_at)
audit_event(id, type, detail, created_at)  -- 验证码/暂停等关键事件
```

标识符原则：`job_key`/`conv_key` 使用 zhipin 页面 URL 中的加密 ID（对用户可见、路由用），内部关联一律数值主键。

## 5. 接口契约

前后端与插件↔服务统一 snake_case；列表统一 `page`(从1)+`page_size`，响应 `data/page/total/page_size`；错误只返回稳定 `code` + 人类可读 message（技术根因仅入日志）。

### 5.1 插件 ↔ 服务（WebSocket 事件）

服务→插件：

| event | 载荷 | 说明 |
|---|---|---|
| `deliver.next` | 职位参数 | 执行一次"立即沟通"+招呼语 |
| `chat.send` | conv_key, content | 发送已审核的回复 |
| `chat.poll` | – | 触发一次 DOM 会话列表兜底扫描 |
| `guard.pause` | reason_code | 立即暂停所有操作 |
| `config.sync` | 抖动参数等 | 下发人化间隔配置 |

插件→服务：

| event | 载荷 | 说明 |
|---|---|---|
| `hello` | token, ext_version | 握手与鉴权 |
| `chat.message` | conv_key, direction, content, msg_type, ts | 新消息（拦截法） |
| `chat.snapshot` | 会话列表快照 | 兜底扫描结果 |
| `deliver.result` | job_key, ok, fail_code | 投递动作结果 |
| `page.anomaly` | type ENUM(captcha, logged_out, selector_miss, nav_unexpected), payload | 异常上报 |

### 5.2 控制台 ↔ 服务（REST 摘要）

```
GET/PUT  /api/settings          # LLM、护栏、话术模板
GET/PUT  /api/resume-context
GET/POST/DELETE /api/rules
GET      /api/jobs?status=&page=&page_size=
GET      /api/conversations?page=&page_size=
GET      /api/conversations/{id}/messages
GET      /api/drafts?status=pending&page=&page_size=
PUT      /api/drafts/{id}       # 审核：approve(可改内容)/reject
POST     /api/tasks/deliver     { keyword, filters, daily_limit }
POST     /api/tasks/:action     # pause / resume
GET      /api/ws                # 控制台复用同一 WS 事件总线（只读事件 + 草稿提醒）
```

## 6. Vue 控制台（Vite + Vue 3）

| 页面 | 内容 |
|---|---|
| 概览 | 连接状态、今日投递/回复计数、护栏状态、暂停/恢复 |
| 审核队列 | 左会话右草稿：原始消息 + 草稿（来源标注 rule/llm）+ 编辑框 + 确认/驳回 |
| 投递任务 | 关键词与过滤策略、任务启停、投递记录列表 |
| 会话记录 | 会话列表 + 消息时间线 |
| 规则库 | 高频问题正则与预设答案 CRUD |
| 设置 | LLM 供应商配置、护栏参数、简历上下文、招呼语模板 |

实时数据（新消息、草稿就绪、异常告警）走 WS 推送，不轮询。异常告警用页面级横幅（验证码、掉登录必须显眼且需手动确认消除）。

## 7. 风险控制与免责声明

- README 与首次使用引导显著位置展示：本工具违反平台用户协议的可能性与封号风险，用户自担；建议小流量试用、观察账号状态
- 护栏参数只允许调严不允许整体关闭（代码层面不提供"关闭护栏"开关）
- 不收集任何遥测；所有数据本地 SQLite
- 开源许可证拟用 MIT，附 `DISCLAIMER` 章节

## 8. 里程碑

| 阶段 | 交付 | 验收 |
|---|---|---|
| M1 骨架 | Go 服务 + 插件骨架 + WS 配对互通 + rod 拉起带插件的 Chrome + 控制台概览页 | 控制台可见插件连接状态与页面 URL 感知 |
| M2 消息通道 | injected WS 拦截 + DOM 兜底扫描 + 消息落库 + 控制台会话/消息展示 | 人工在页面收发消息，控制台 1s 内同步 |
| M3 应答回路 | 规则库 + LLM 网关 + 审核队列 + `chat.send` 发送 | 高频问题命中规则直出草稿；LLM 草稿审核后能正确发出 |
| M4 投递 | 投递状态机 + 过滤策略 + 招呼语模板 + 护栏全量生效 | 串行投递 10 个职位全程无人工干预（验证码除外），日志完整 |
| M5 打磨 | 异常告警、统计、首次使用引导、免责声明、打包（单二进制 + 插件 zip） | 端到端演练 + 文档齐全 |

## 9. 已决事项与实现约定

- 项目名：`job-chat-copilot`，仓库 `github.com/linuxsuren/job-chat-copilot`
- 招呼语支持多模板轮换（顺序/随机两种策略，M4 实现）
- LLM 预设：DeepSeek / GLM / Qwen 三预设 + 自定义（base_url/api_key/model 可全覆盖）
- Go module：`github.com/linuxsuren/job-chat-copilot`；服务默认监听 `127.0.0.1:7422`
- 顶层目录：`server/`（Go）、`extension/`（MV3 插件，TypeScript + Vite）、`web/`（Vue 3 控制台，JS + Vite + Element Plus）
- 验证命令：`cd server && go build ./... && go test ./...`；`cd web && npm ci && npm run build`；`cd extension && npm ci && npm run build`
- SQLite 驱动：`modernc.org/sqlite`（纯 Go，免 CGO）；浏览器托管：`github.com/go-rod/rod`
- 三端契约唯一事实源：本文档 §5（WS 事件 + REST），字段一律 snake_case；实现中契约需要变更时，必须先改本文档再改代码

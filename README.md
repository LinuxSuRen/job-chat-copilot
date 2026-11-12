# job-chat-copilot

Boss直聘（zhipin.com）半自动求职助手：浏览器插件 + 本地 Go 服务 + 大模型 API。

> [!WARNING]
> 本工具仅限个人求职辅助用途，使用者自担风险（详见[风险与免责声明](docs/design.md#7-风险控制与免责声明)）。
> 自动化操作违反平台用户协议，可能导致限流、功能限制甚至封号。请保持有人值守使用。

## 核心能力

- **辅助投递**：按你的筛选策略（关键词、薪资、公司黑名单、LLM JD 匹配度）辅助发起"立即沟通"，发送变量化招呼语，内置限速与人化间隔
- **智能应答（人工审核优先）**：监听聊天会话，高频问题走本地规则库，其余问题由大模型基于你的简历上下文生成答案，默认先进入审核队列，确认后发送
- **LLM 网关**：OpenAI 兼容协议接入（DeepSeek / Qwen / GLM 等均可），配置 base_url + api_key + model 即用
- **本地优先**：所有数据（会话、草稿、简历上下文）保存在本机 SQLite，不经过任何第三方服务器

## 架构形态

```
Chrome（你的浏览器，正常登录 zhipin.com）
 └── 浏览器插件（Manifest V3，页面内读写与操作）
      │ localhost HTTP + WebSocket
 ┌────┴─────────────────────────────┐
 │ Go 本地服务（单二进制，内嵌 Vue 控制台） │
 │  任务编排 / 限速护栏 / LLM 网关 / SQLite │
 │  rod(CDP) 托管：拉起浏览器、监督截图     │
 └──────────────────────────────────┘
```

详细设计见 [docs/design.md](docs/design.md)。

## 技术栈

- 后端：Go（rod、SQLite）
- 前端：Vue 3 + Vite（控制台，go:embed 打包进单二进制）
- 插件：TypeScript + Manifest V3

## 状态

设计阶段，见 [docs/design.md](docs/design.md)。尚未开始实现。

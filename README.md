# job-chat-copilot

Boss直聘半自动求职助手：浏览器插件 + 本地 Go 服务 + 大模型 API。

> [!WARNING]
> 本工具仅供个人求职辅助与学习研究。自动化操作可能违反平台用户协议，存在限流、功能限制甚至封号风险，**使用者自担风险**。请保持有人值守、小流量使用。不做验证码绕过、不做多账号、不收集任何遥测数据。

## 核心能力

- **辅助投递**：按你的筛选策略（排除关键词、薪资区间）扫描搜索页职位，逐个发起"立即沟通"，招呼语支持多模板轮换（顺序/随机）与 `{title}` `{company}` `{jd}` 变量
- **智能应答（人工审核优先）**：监听聊天会话（WebSocket 拦截 + DOM 扫描双通道），高频问题走本地规则库，其余问题由大模型基于你的简历上下文生成答案；默认进入审核队列，确认后发送，可切换全自动
- **LLM 网关**：OpenAI 兼容协议，内置 DeepSeek / GLM / Qwen 预设 + 自定义
- **风控护栏（内置不可关闭）**：投递间隔人化抖动、每日上限、活跃时段、验证码/掉登录立即全局暂停并告警
- **本地优先**：所有数据保存在本机 SQLite，单二进制内嵌控制台

## 架构

```
Chrome（你的浏览器，正常登录 zhipin.com）
 └── 浏览器插件（MV3：页面内真实事件操作 + WS/XHR 消息拦截）
      │ ws://127.0.0.1:7422/ws/ext（配对码绑定 + token 鉴权）
 ┌────┴──────────────────────────────────┐
 │ Go 本地服务（单二进制，内嵌 Vue 控制台）  │
 │ 任务编排 / 限速护栏 / LLM 网关 / SQLite  │
 │ rod 托管：拉起带插件的 Chrome / 监督截图  │
 └───────────────────────────────────────┘
```

详细设计见 [docs/design.md](docs/design.md)。

## 快速开始

前置：Go 1.27+、Node 20+、Chrome。

```bash
# 构建插件与控制台
cd extension && npm install && npm run build && cd ..
cd web && npm install && npm run build && cd ..

# 构建单二进制（自动内嵌控制台）
cd server && make build
./bin/job-chat-copilot            # 默认 127.0.0.1:7422，自动拉起带插件的 Chrome
# 或 ./bin/job-chat-copilot --no-browser 用自己日常的 Chrome 手动装插件
```

1. 浏览器打开 `http://127.0.0.1:7422`，确认免责声明，记下概览页配对码
2. Chrome 打开 `chrome://extensions` → 开发者模式 → 加载已解压的 `extension/dist`
3. 打开插件 options 页，粘贴配对码完成绑定；正常登录 zhipin.com
4. 回到控制台：设置 LLM 与简历上下文 → 规则库录入高频问题 → 投递任务页发起任务
5. 审核队列处理大模型草稿；出现验证码/掉登录时全局暂停，人工处理后恢复

## 目录结构

| 目录 | 说明 | 验证命令 |
|---|---|---|
| `server/` | Go 本地服务 | `go build ./... && go test ./...` |
| `extension/` | Chrome MV3 插件（TypeScript） | `npm run build && npx vitest run` |
| `web/` | Vue 3 控制台（JS + Element Plus） | `npm run build` |

## 已知限制

- zhipin.com 前端无稳定选择器锚点，页面改版可能导致操作/扫描失配（会上报 `selector_miss`，绝不静默失败），选择器按最佳 effort 维护
- 职位扫描仅覆盖搜索结果单页（不自动翻页）
- 聊天输入框为 contenteditable 富文本，是最脆弱的选择器环节

## 许可证

[MIT](LICENSE)

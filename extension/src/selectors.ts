/**
 * 选择器集中收敛（docs/design.md §3.4）。
 * zhipin class 名混淆且随构建变化，策略：
 *   优先 data-* / aria / 语义文本 → 结构路径兜底。
 * 每个目标是一组候选选择器（按优先级排序），逐个尝试；全部失配即 selector_miss，
 * 绝不静默失败（上层负责上报 page.anomaly）。
 *
 * 不承诺跟随 zhipin 前端每次改版，选择器修复以最佳 effort 维护。
 */

export type SelectorTarget =
  | 'search_input' // 搜索页关键词输入框（jobs.scan 导航用）
  | 'job_card_list' // 搜索页职位卡片容器
  | 'job_card' // 单个职位卡片
  | 'job_card_title' // 卡片内职位名
  | 'job_card_company' // 卡片内公司名
  | 'job_card_salary' // 卡片内薪资
  | 'job_card_link' // 卡片跳转链接（取 job_key）
  | 'btn_start_chat' // "立即沟通"按钮（职位详情/搜索弹层）
  | 'chat_input' // 聊天输入框（contenteditable 或 textarea）
  | 'btn_chat_send' // 聊天发送按钮
  | 'conversation_list' // 会话列表容器
  | 'conversation_item' // 单个会话项
  | 'conversation_name' // 会话项 HR 名
  | 'conversation_last_msg' // 会话项最后一条消息预览
  | 'conversation_unread' // 未读角标
  | 'captcha_dialog' // 滑块/验证码特征容器
  | 'captcha_text' // 验证文案（"请拖动滑块"等）
  | 'login_marker'; // 登录页特征（出现在业务页内 = 登录态丢失）

/**
 * 候选选择器表。语义文本选择器用 :has-text 形式表达（getByText 辅助解析），
 * 其中 text= 前缀表示按可见文本匹配（querySelector 无法表达，走文本扫描路径）。
 */
export const SELECTORS: Record<SelectorTarget, readonly string[]> = {
  search_input: [
    '.search-form input[type="text"]',
    'input[placeholder*="搜索"]',
    '.zp-search__input',
    '[class*="search"] input[type="text"]',
  ],
  job_card_list: [
    '.job-list-box',
    '.search-job-result-list',
    'ul[class*="job-list"]',
    '[class*="JobList"]',
  ],
  job_card: [
    '.job-card-wrapper',
    '.search-job-result-list li',
    'li[class*="job-card"]',
    '[class*="JobCard"]',
  ],
  job_card_title: ['.job-card-left .job-name', '.job-name', '[class*="jobName"]', 'text=职位'],
  job_card_company: ['.company-info .company-name', '.company-name', '[class*="companyName"]'],
  job_card_salary: ['.salary', '[class*="salary"]', '.job-card-right .red'],
  job_card_link: ['.job-card-left a[href*="/job_detail/"]', 'a[href*="/job_detail/"]'],
  btn_start_chat: [
    'text=立即沟通',
    '.start-chat-btn',
    'button[class*="start-chat"]',
    '[class*="op-btn"][class*="chat"]',
    'text=继续沟通',
  ],
  chat_input: [
    '.chat-input',
    'textarea[placeholder*="输入"]',
    '[contenteditable="true"][class*="input"]',
    '.edit-area[contenteditable="true"]',
    '[class*="chatInput"]',
  ],
  btn_chat_send: ['text=发送', 'button[class*="send"]', '[class*="btn-send"]'],
  conversation_list: ['.conversation-list-container', '[class*="session-list"]', '[class*="conversationList"]'],
  conversation_item: ['.conversation-list-container li', 'li[class*="session-item"]', '[class*="sessionItem"]'],
  conversation_name: ['.item-name', '[class*="session-name"]', '.name'],
  conversation_last_msg: ['.item-msg', '[class*="session-text"]', '.msg'],
  conversation_unread: ['.unread-count', '[class*="unread"]'],
  captcha_dialog: ['.nc_wrapper', '#captcha', '.geetest_panel', '[class*="verify"]', '[class*="captcha"]'],
  captcha_text: ['text=请拖动滑块', 'text=安全验证', 'text=拖动滑块完成验证'],
  login_marker: ['text=请扫码登录', '.sign-form', '[class*="login-scan"]'],
};

/** 文本选择器前缀 */
export const TEXT_PREFIX = 'text=';

export interface Lookup {
  /** css 选择器查询，返回首个匹配 */
  querySelector(sel: string): Element | null;
  /** 按可见文本匹配（tag 可选），返回首个匹配 */
  getByText(text: string, tag?: string): Element | null;
}

export interface ResolveResult {
  /** 命中的选择器（含 text= 前缀时表示走文本路径）；null 表示全部失配 */
  matched: string | null;
  element: Element | null;
}

/**
 * 选择器解析状态机（纯函数，单测覆盖）：
 * 逐个候选尝试，text= 前缀走文本匹配，其余走 querySelector；
 * 第一个命中即返回；全失配返回 { matched: null }，由上层上报 selector_miss。
 */
export function resolveSelector(target: SelectorTarget, doc: Lookup): ResolveResult {
  const candidates = SELECTORS[target];
  for (const sel of candidates) {
    if (sel.startsWith(TEXT_PREFIX)) {
      const text = sel.slice(TEXT_PREFIX.length);
      // 约定：text=A/B 形式表示多个候选文本（用于 captcha_text 等聚合目标）
      for (const t of text.split('/')) {
        const el = doc.getByText(t);
        if (el) {
          return { matched: `${TEXT_PREFIX}${t}`, element: el };
        }
      }
      continue;
    }
    const el = doc.querySelector(sel);
    if (el) {
      return { matched: sel, element: el };
    }
  }
  return { matched: null, element: null };
}

/** 选择器目标失配时，给 page.anomaly 的 payload 附加脱敏结构快照 */
export function missPayload(target: SelectorTarget, sample: Element | null): Record<string, unknown> {
  const payload: Record<string, unknown> = {
    selector_target: target,
    tried: SELECTORS[target],
  };
  if (sample) {
    payload.structure_hint = sanitizeStructure(sample);
  }
  return payload;
}

/** 脱敏：只保留标签/属性名骨架与语义文本片段，去掉具体用户数据 */
export function sanitizeStructure(el: Element, depth = 3): unknown {
  if (depth <= 0) {
    return el.tagName.toLowerCase();
  }
  const children = Array.from(el.children)
    .slice(0, 12)
    .map((c) => sanitizeStructure(c, depth - 1));
  return {
    tag: el.tagName.toLowerCase(),
    attrs: el.getAttributeNames().filter((a) => a === 'class' || a.startsWith('data-') || a === 'aria-label'),
    text_hint: (el.textContent ?? '').trim().slice(0, 40),
    children,
  };
}

/** 从 zhipin 职位详情 URL 提取加密 job_key（如 /job_detail/abc123.html） */
export function extractJobKey(href: string): string | null {
  const m = /\/job_detail\/([0-9a-zA-Z_-]+)\.html/.exec(href);
  return m?.[1] ?? null;
}

/** 会话 key 提取：zhipin 会话链接形如 /web/geek/chat?friendId=xxx 或加密串路由 */
export function extractConvKey(href: string): string | null {
  const friend = /[?&]friendId=([0-9a-zA-Z_-]+)/.exec(href);
  if (friend?.[1]) {
    return friend[1];
  }
  const security = /\/chat\/([0-9a-zA-Z_-]+)/.exec(href);
  return security?.[1] ?? null;
}

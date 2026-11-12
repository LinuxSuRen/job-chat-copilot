/**
 * DOM 操作执行器（docs/design.md §3.3）：
 * 投递动作（deliver.next）、chat.send、chat.poll。
 * 每个动作：前置异常检查 → 页面状态断言 → 真实事件序列操作 → 后置断言 → 结果/异常上报。
 * 全部串行（同一时刻只执行一个动作），任何失败上报后立即返回。
 */

import {
  resolveSelector,
  extractConvKey,
} from '../selectors';
import type {
  DeliverNextPayload,
  DeliverResultPayload,
  ChatSendPayload,
  ChatSnapshotPayload,
  SnapshotConversation,
  PageAnomalyPayload,
  JobsScanPayload,
  ScannedJob,
} from '../shared/protocol';
import { dispatchRealClick, dispatchRealInput, dispatchEnter, assertVisible } from './events';
import { lookup, precheckAnomalies, selectorMiss, navUnexpected, detectCaptcha } from './anomaly';
import { isPaused, currentJitter } from './state';
import { humanDelayMs, sleep } from '../shared/jitter';
import { parseJobCard, parseJobCards, matchJob, buildJobsList, type JobTarget } from './scan';

export type Report = (type: 'anomaly' | 'deliver_result' | 'chat_message' | 'chat_snapshot' | 'jobs_list', payload: unknown) => void;

export type DeliverOutcome =
  | { kind: 'done'; result: DeliverResultPayload }
  | { kind: 'anomaly'; payload: PageAnomalyPayload };

const SEARCH_PAGE_HINT = '/web/geek/job';
const CHAT_PAGE_HINT = '/web/geek/chat';

function notPaused(): boolean {
  return !isPaused();
}

/** 人化抖动：服务端基准 + 本地小随机 */
async function humanPause(): Promise<void> {
  await sleep(humanDelayMs(currentJitter()));
}

function pathOf(href: string): string {
  try {
    return new URL(href).pathname;
  } catch {
    return href;
  }
}

// ---------------------------------------------------------------------------
// deliver.next：搜索页职位卡片解析 → 点击"立即沟通" → 输入招呼语 → 发送
// ---------------------------------------------------------------------------

export async function executeDeliverNext(p: DeliverNextPayload, report: Report): Promise<DeliverResultPayload> {
  const fail = (fail_code: NonNullable<DeliverResultPayload['fail_code']>, detail: string): DeliverResultPayload => ({
    job_key: p.job_key,
    ok: false,
    fail_code,
    detail,
  });

  if (!notPaused()) {
    return fail('paused', `guard paused: ${isPaused() ? 'yes' : 'no'}`);
  }

  // 前置：验证码/登录态优先检查
  const anomaly = precheckAnomalies(document);
  if (anomaly) {
    report('anomaly', anomaly);
    return fail(anomaly.type === 'captcha' ? 'captcha' : 'logged_out', anomaly.type);
  }

  // 页面断言：应在搜索结果页（含职位详情弹层的搜索页）
  if (!pathOf(location.href).startsWith(SEARCH_PAGE_HINT) && !pathOf(location.href).includes('/job_detail')) {
    report('anomaly', navUnexpected(SEARCH_PAGE_HINT));
    return fail('nav_unexpected', pathOf(location.href));
  }

  const lk = lookup(document);

  // 1. 解析职位卡片，定位目标（job_key 优先精确匹配，job_title/company 兜底）
  const cardList = resolveSelector('job_card_list', lk);
  if (!cardList.element) {
    report('anomaly', selectorMiss('job_card_list', document.body));
    return fail('selector_miss', 'job_card_list');
  }

  const target = findJobCard(cardList.element as HTMLElement, p);
  if (!target) {
    report('anomaly', selectorMiss('job_card', cardList.element as HTMLElement));
    return fail('selector_miss', `job card not found: ${p.job_title ?? p.job_key}`);
  }

  // 2. 打开职位（点卡片；详情可能在右侧面板或弹层）
  await humanPause();
  if (!dispatchRealClick(target.card)) {
    return fail('unknown', 'card click dispatched but element not visible');
  }
  await humanPause();

  // 操作后断言：验证码可能在点击后弹出
  const postClickCaptcha = detectCaptcha(document);
  if (postClickCaptcha) {
    report('anomaly', postClickCaptcha);
    return fail('captcha', 'after card click');
  }

  // 3. 点击"立即沟通"（语义文本优先）
  const startChat = resolveSelector('btn_start_chat', lookup(document));
  if (!startChat.element || !assertVisible(startChat.element)) {
    report('anomaly', selectorMiss('btn_start_chat', document.body));
    return fail('selector_miss', 'btn_start_chat');
  }
  if (!dispatchRealClick(startChat.element)) {
    return fail('send_failed', 'start chat click not dispatched');
  }
  await humanPause();

  // 4. 输入招呼语并发送：立即沟通后页面切到 chat 视图
  const input = await waitFor(() => resolveSelector('chat_input', lookup(document)).element, 4000);
  if (!input) {
    report('anomaly', selectorMiss('chat_input', document.body));
    return fail('selector_miss', 'chat_input');
  }
  if (!dispatchRealInput(input, p.greeting)) {
    return fail('send_failed', 'greeting input not dispatched');
  }
  await humanPause();

  const sendBtn = resolveSelector('btn_chat_send', lookup(document));
  let sent = false;
  if (sendBtn.element && assertVisible(sendBtn.element)) {
    sent = dispatchRealClick(sendBtn.element);
  }
  if (!sent) {
    // 兜底：回车发送
    sent = dispatchEnter(input);
  }

  // 5. 后置断言：输入框清空视为发送成功（zhipin 发送后清空输入框）
  await sleep(600);
  const cleared = (input as HTMLInputElement).value === '' || (input.textContent ?? '').trim() === '';
  if (!sent || !cleared) {
    return fail('send_failed', sent ? 'input not cleared after send' : 'send not dispatched');
  }

  return { job_key: p.job_key, ok: true };
}

interface CardMatch {
  card: Element;
  entry: ScannedJob;
}

/**
 * 卡片定位：DOM 解析为结构化条目后走纯函数 matchJob（单测覆盖）——
 * job_key 优先精确匹配当前页卡片，job_title（+company）兜底。
 */
function findJobCard(container: HTMLElement, p: DeliverNextPayload): CardMatch | null {
  const cards = Array.from(container.querySelectorAll('li, [class*="card"], [class*="Card"]'));
  const cardByKey = new Map<Element, ReturnType<typeof parseJobCard>>();
  const entries: ReturnType<typeof parseJobCards> = [];
  for (const card of cards) {
    const entry = parseJobCard(card);
    if (entry) {
      cardByKey.set(card, entry);
      entries.push(entry);
    }
  }
  const target: JobTarget = { job_key: p.job_key };
  if (typeof p.job_title === 'string') {
    target.job_title = p.job_title;
  }
  if (typeof p.company === 'string') {
    target.company = p.company;
  }
  const hit = matchJob(entries, target);
  if (!hit) {
    return null;
  }
  for (const [card, entry] of cardByKey) {
    if (entry === hit) {
      return { card, entry: hit };
    }
  }
  return null;
}

// ---------------------------------------------------------------------------
// jobs.scan：确保在搜索结果页（必要时真实事件导航）→ 扫描卡片 → jobs.list 上报
// ---------------------------------------------------------------------------

const NAV_WAIT_MS = 8000;

export async function executeJobsScan(p: JobsScanPayload, report: Report): Promise<void> {
  if (!notPaused()) {
    return;
  }
  const anomaly = precheckAnomalies(document);
  if (anomaly) {
    report('anomaly', anomaly);
    return;
  }

  // 确保在搜索结果页；不在则用真实事件导航（填搜索框 → 回车），不直接改 location
  if (!pathOf(location.href).startsWith(SEARCH_PAGE_HINT)) {
    const input = resolveSelector('search_input', lookup(document));
    if (!input.element || !assertVisible(input.element)) {
      report('anomaly', selectorMiss('search_input', document.body));
      return;
    }
    if (!dispatchRealInput(input.element, p.keyword)) {
      return;
    }
    await humanPause();
    if (!dispatchEnter(input.element)) {
      return;
    }
    // 等待 SPA 路由切换到搜索结果页
    const navigated = await waitForPath(SEARCH_PAGE_HINT, NAV_WAIT_MS);
    if (!navigated) {
      report('anomaly', navUnexpected(SEARCH_PAGE_HINT));
      return;
    }
  }

  // 等待卡片列表渲染完成（真实事件导航后异步渲染）
  const cardList = await waitFor(() => resolveSelector('job_card_list', lookup(document)).element, NAV_WAIT_MS);
  if (!cardList) {
    report('anomaly', selectorMiss('job_card_list', document.body));
    return;
  }

  const jobs = parseJobCards(cardList);
  report('jobs_list', buildJobsList(p.keyword, jobs));
}

async function waitForPath(prefix: string, timeoutMs: number): Promise<boolean> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    if (pathOf(location.href).startsWith(prefix)) {
      return true;
    }
    await sleep(200);
  }
  return pathOf(location.href).startsWith(prefix);
}

// ---------------------------------------------------------------------------
// chat.send：定位会话输入框 → 填入 → 发送
// ---------------------------------------------------------------------------

export async function executeChatSend(p: ChatSendPayload, report: Report): Promise<boolean> {
  if (!notPaused()) {
    return false;
  }
  const anomaly = precheckAnomalies(document);
  if (anomaly) {
    report('anomaly', anomaly);
    return false;
  }
  const path = pathOf(location.href);
  if (!path.startsWith(CHAT_PAGE_HINT)) {
    report('anomaly', navUnexpected(CHAT_PAGE_HINT));
    return false;
  }

  // 若指定 conv_key 且不在当前会话，则点击对应会话项切换
  if (p.conv_key) {
    const switched = await switchConversation(p.conv_key, report);
    if (!switched) {
      return false;
    }
  }

  const input = await waitFor(() => resolveSelector('chat_input', lookup(document)).element, 4000);
  if (!input) {
    report('anomaly', selectorMiss('chat_input', document.body));
    return false;
  }
  if (!dispatchRealInput(input, p.content)) {
    return false;
  }
  await humanPause();
  const sendBtn = resolveSelector('btn_chat_send', lookup(document));
  let sent = false;
  if (sendBtn.element && assertVisible(sendBtn.element)) {
    sent = dispatchRealClick(sendBtn.element);
  }
  if (!sent) {
    sent = dispatchEnter(input);
  }
  await sleep(400);
  return sent;
}

async function switchConversation(convKey: string, report: Report): Promise<boolean> {
  const list = resolveSelector('conversation_list', lookup(document));
  if (!list.element) {
    report('anomaly', selectorMiss('conversation_list', document.body));
    return false;
  }
  const items = Array.from(list.element.querySelectorAll('li, [class*="item"]'));
  for (const item of items) {
    const link = item.querySelector('a[href]') as HTMLAnchorElement | null;
    const key = link ? extractConvKey(link.href) : null;
    const nameEl = item.querySelector('.item-name, [class*="session-name"], .name');
    if (key === convKey) {
      await humanPause();
      return dispatchRealClick(item);
    }
    void nameEl;
  }
  // 未在列表中找到目标会话：上报 selector_miss（会话可能已被 HR 删除或折叠）
  report('anomaly', selectorMiss('conversation_item', list.element as HTMLElement));
  return false;
}

// ---------------------------------------------------------------------------
// chat.poll：会话列表 DOM 扫描 → chat.snapshot
// ---------------------------------------------------------------------------

export function executeChatPoll(report: Report): void {
  const lk = lookup(document);
  const path = pathOf(location.href);
  if (!path.startsWith(CHAT_PAGE_HINT)) {
    report('anomaly', navUnexpected(CHAT_PAGE_HINT));
    return;
  }
  const list = resolveSelector('conversation_list', lk);
  if (!list.element) {
    report('anomaly', selectorMiss('conversation_list', document.body));
    return;
  }
  const items = Array.from(list.element.querySelectorAll('li, [class*="item"]'));
  const conversations: SnapshotConversation[] = [];
  for (const item of items) {
    const link = item.querySelector('a[href]') as HTMLAnchorElement | null;
    const convKey = link ? extractConvKey(link.href) : null;
    const name = (item.querySelector('.item-name, [class*="session-name"], .name')?.textContent ?? '').trim();
    const last = (item.querySelector('.item-msg, [class*="session-text"], .msg')?.textContent ?? '').trim();
    const unreadText = (item.querySelector('.unread-count, [class*="unread"]')?.textContent ?? '').trim();
    const unread = parseInt(unreadText, 10);
    if (!convKey && !name) {
      continue;
    }
    conversations.push({
      conv_key: convKey ?? name,
      hr_name: name,
      last_content: last,
      last_msg_at: Date.now(),
      unread: Number.isFinite(unread) ? unread : 0,
    });
  }
  const snapshot: ChatSnapshotPayload = { conversations, scanned_at: Date.now() };
  report('chat_snapshot', snapshot);
}

// ---------------------------------------------------------------------------

async function waitFor(fn: () => Element | null, timeoutMs: number): Promise<Element | null> {
  const start = Date.now();
  let result = fn();
  while (result === null && Date.now() - start < timeoutMs) {
    await sleep(200);
    result = fn();
  }
  return result;
}

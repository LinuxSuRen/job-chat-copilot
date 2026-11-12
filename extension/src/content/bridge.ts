/**
 * 拦截法聊天消息结构化（纯函数，单测覆盖）。
 * zhipin 聊天 WS/XHR 帧是私有 JSON，这里只做保守的结构识别与字段映射，
 * 无法识别的帧一律返回 null（由 DOM 兜底扫描补齐，两法互补校验）。
 */

import type { ChatMessagePayload } from '../shared/protocol';
import { extractConvKey } from '../selectors';

interface RawMessageLike {
  zid?: unknown;
  conversationId?: unknown;
  encryptConvId?: unknown;
  encryptFriendId?: unknown;
  fromUid?: unknown;
  direction?: unknown;
  content?: unknown;
  text?: unknown;
  texts?: unknown;
  msgType?: unknown;
  type?: unknown;
  createTime?: unknown;
  timestamp?: unknown;
  lastContent?: unknown;
}

const CHAT_FRAME_HINTS = ['zpb-ops', 'chat', 'wld', 'im', 'message'];
const MSG_TEXT_TYPE = 1;

/**
 * 判断一个拦截帧是否疑似聊天消息帧（channel 层粗过滤，防噪音）。
 */
export function looksLikeChatFrame(url: string, data: unknown): boolean {
  if (typeof data !== 'object' || data === null) {
    return false;
  }
  const urlHit = CHAT_FRAME_HINTS.some((h) => url.toLowerCase().includes(h));
  const dataHit = JSON.stringify(data).toLowerCase().includes('"text"') ||
    'lastContent' in (data as Record<string, unknown>) ||
    'encryptConvId' in (data as Record<string, unknown>) ||
    'msgType' in (data as Record<string, unknown>);
  // WS 帧的 url 是聊天网关地址本身即可粗过；XHR 需要数据侧特征
  return urlHit || dataHit;
}

function pickString(obj: Record<string, unknown>, keys: string[]): string | null {
  for (const k of keys) {
    const v = obj[k];
    if (typeof v === 'string' && v.length > 0) {
      return v;
    }
  }
  return null;
}

function pickNumber(obj: Record<string, unknown>, keys: string[]): number {
  for (const k of keys) {
    const v = obj[k];
    if (typeof v === 'number' && Number.isFinite(v)) {
      // zhipin 时间戳多为秒级
      return v < 1e12 ? v * 1000 : v;
    }
  }
  return Date.now();
}

/**
 * 从疑似帧中提取结构化聊天消息；识别失败返回 null。
 * 兼容两种常见形态：
 *  A. 单条消息体：{encryptConvId|conversationId, fromUid|direction, text|texts, msgType|type, createTime}
 *  B. 列表体：{messages: [...]} 或 {data: {messages|list: [...]}}
 */
export function extractChatMessage(data: unknown): ChatMessagePayload | null {
  const body = unwrap(data);
  if (!body) {
    return null;
  }
  const single = extractOne(body);
  if (single) {
    return single;
  }
  const list = pickArray(body, ['messages', 'list', 'items']);
  if (!list) {
    return null;
  }
  for (const item of list) {
    if (typeof item !== 'object' || item === null) {
      continue;
    }
    const one = extractOne(item as Record<string, unknown>);
    if (one) {
      return one;
    }
  }
  return null;
}

function unwrap(data: unknown): Record<string, unknown> | null {
  if (typeof data !== 'object' || data === null) {
    return null;
  }
  const obj = data as Record<string, unknown>;
  if ('zpb-ops' in obj || 'code' in obj || 'data' in obj) {
    const inner = obj['data'];
    if (typeof inner === 'object' && inner !== null) {
      return inner as Record<string, unknown>;
    }
    return obj;
  }
  return obj;
}

function pickArray(obj: Record<string, unknown>, keys: string[]): unknown[] | null {
  for (const k of keys) {
    const v = obj[k];
    if (Array.isArray(v) && v.length > 0) {
      return v;
    }
  }
  return null;
}

function extractOne(obj: Record<string, unknown>): ChatMessagePayload | null {
  const convKey = pickString(obj, ['encryptConvId', 'conversationId', 'convId'])
    ?? pickString(obj, ['friendId', 'encryptFriendId']);
  if (!convKey) {
    return null;
  }
  // 文本内容：text 字符串 / texts 数组 / lastContent（会话列表帧）
  const raw = obj as RawMessageLike;
  let content: string | null = null;
  if (typeof raw.text === 'string' && raw.text.length > 0) {
    content = raw.text;  } else if (Array.isArray(raw.texts)) {
    content = raw.texts.filter((t): t is string => typeof t === 'string').join(' ');
  } else if (typeof raw.lastContent === 'string') {
    content = raw.lastContent;
  }
  if (!content) {
    return null;
  }
  // 方向：显式 direction 优先；否则凭 fromUid 与本地 uid 比较（由调用方注入 myUid）
  let direction: ChatMessagePayload['direction'];
  if (raw.direction === 'in' || raw.direction === 'out') {
    direction = raw.direction;
  } else {
    direction = 'in'; // 保守：无法判定方向时按入站处理，交由服务端与 DOM 快照校验
  }
  const msgTypeRaw = typeof raw.msgType === 'number' ? raw.msgType : raw.type;
  const msgType = msgTypeRaw === MSG_TEXT_TYPE || msgTypeRaw === undefined ? 'text' : String(msgTypeRaw);
  return {
    conv_key: normalizeConvKey(convKey),
    direction,
    content,
    msg_type: msgType,
    ts: pickNumber(obj as Record<string, unknown>, ['createTime', 'timestamp', 'ts']),
  };
}

function normalizeConvKey(convKey: string): string {
  // 会话 key 统一从 URL 形态提取（与 DOM 扫描同源），裸 ID 直接透传
  return extractConvKey(`/chat/${convKey}`) ?? convKey;
}

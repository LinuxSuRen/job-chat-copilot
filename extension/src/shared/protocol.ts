/**
 * 插件 ↔ 服务 WS 事件契约（docs/design.md §5.1，字段一律 snake_case）。
 * 本文件是契约在插件侧的唯一映射，改契约必须先改设计文档。
 */

export const DEFAULT_SERVER_WS = 'ws://127.0.0.1:7422/ws/ext';
export const RECONNECT_BASE_MS = 2000;

/** 服务 → 插件 */
export type ServerEvent =
  | { event: 'hello_ack'; payload: HelloAckPayload }
  | { event: 'jobs.scan'; payload: JobsScanPayload }
  | { event: 'deliver.next'; payload: DeliverNextPayload }
  | { event: 'chat.send'; payload: ChatSendPayload }
  | { event: 'chat.poll'; payload: Record<string, never> }
  | { event: 'guard.pause'; payload: GuardPausePayload }
  | { event: 'config.sync'; payload: ConfigSyncPayload }
  | { event: 'review.stats'; payload: ReviewStatsPayload };

/** 插件 → 服务 */
export type ClientEvent =
  | { event: 'hello'; payload: HelloPayload }
  | { event: 'jobs.list'; payload: JobsListPayload }
  | { event: 'chat.message'; payload: ChatMessagePayload }
  | { event: 'chat.snapshot'; payload: ChatSnapshotPayload }
  | { event: 'deliver.result'; payload: DeliverResultPayload }
  | { event: 'page.anomaly'; payload: PageAnomalyPayload };

export interface HelloAckPayload {
  paired: boolean;
  /** 配对成功下发的绑定 token（插件持久化，之后凭 token 鉴权） */
  bind_token?: string;
}

export interface JobsScanPayload {
  keyword: string;
}

/** jobs.list 条目：搜索页职位卡片扫描结果 */
export interface ScannedJob {
  job_key: string;
  title: string;
  company: string;
  salary: string;
  /** 卡片/详情可见文本（最佳 effort，取不到置空） */
  jd_text: string;
}

export interface JobsListPayload {
  keyword: string;
  jobs: ScannedJob[];
}

export interface ReviewStatsPayload {
  pending_count: number;
}

export interface HelloPayload {
  token: string;
  ext_version: string;
  /** 首次配对：options 页粘贴的一次性配对码（复用 hello 事件，服务端优先校验 token） */
  pairing_code?: string;
}

export interface DeliverNextPayload {
  job_key: string;
  /** 目标职位卡片定位线索：职位名/公司名（搜索页解析后比对） */
  job_title?: string;
  company?: string;
  /** 招呼语（模板已由服务端渲染完成） */
  greeting: string;
}

export interface ChatSendPayload {
  conv_key: string;
  content: string;
}

export interface GuardPausePayload {
  reason_code: string;
}

export interface ConfigSyncPayload {
  /** 人化间隔配置（服务端统一下发，插件本地执行时再叠加小随机） */
  min_interval_ms?: number;
  jitter_ratio?: number;
  /** 恢复开关：true=暂停一切页面操作；false/缺省=解除暂停 */
  paused?: boolean;
  /** 每日上限等只读回显，插件不强校验（护栏在服务端） */
  daily_limit?: number;
}

export type MessageDirection = 'in' | 'out';

export interface ChatMessagePayload {
  conv_key: string;
  direction: MessageDirection;
  content: string;
  msg_type: string;
  ts: number;
}

export interface SnapshotConversation {
  conv_key: string;
  hr_name: string;
  last_content: string;
  last_msg_at: number;
  unread: number;
}

export interface ChatSnapshotPayload {
  conversations: SnapshotConversation[];
  scanned_at: number;
}

export type DeliverFailCode =
  | 'selector_miss'
  | 'captcha'
  | 'logged_out'
  | 'nav_unexpected'
  | 'already_contacted'
  | 'send_failed'
  | 'paused'
  | 'unknown';

export interface DeliverResultPayload {
  job_key: string;
  ok: boolean;
  fail_code?: DeliverFailCode;
  detail?: string;
}

export type AnomalyType = 'captcha' | 'logged_out' | 'selector_miss' | 'nav_unexpected';

export interface PageAnomalyPayload {
  type: AnomalyType;
  /** 脱敏后的结构快照/上下文（只保留标签结构与语义文本，去除用户数据） */
  payload: Record<string, unknown>;
  url_hint: string;
  ts: number;
}

export const EXT_VERSION = '0.1.0';

export interface WsEnvelope {
  event: string;
  payload: unknown;
  ts: number;
}

export function encodeEnvelope(event: string, payload: unknown): string {
  const env: WsEnvelope = { event, payload, ts: Date.now() };
  return JSON.stringify(env);
}

export function parseEnvelope(raw: string): WsEnvelope | null {
  try {
    const obj: unknown = JSON.parse(raw);
    if (typeof obj === 'object' && obj !== null && 'event' in obj) {
      const o = obj as { event?: unknown; payload?: unknown; ts?: unknown };
      if (typeof o.event === 'string') {
        return { event: o.event, payload: o.payload ?? {}, ts: typeof o.ts === 'number' ? o.ts : Date.now() };
      }
    }
    return null;
  } catch {
    return null;
  }
}

/** 服务端下发、需要 content-script 执行（或感知）的事件集合 */
export const SERVER_TO_CONTENT_EVENTS = new Set<string>([
  'jobs.scan',
  'deliver.next',
  'chat.send',
  'chat.poll',
  'guard.pause',
  'config.sync',
]);

/** content-script / runtime 内部消息信封（background ↔ content ↔ popup） */
export type RuntimeMessage =
  | { kind: 'to_server'; event: string; payload: unknown }
  | { kind: 'from_server'; event: string; payload: unknown }
  | { kind: 'state_query' }
  | { kind: 'state_broadcast'; state: ExtensionState }
  | { kind: 'inject_message'; event: string; payload: unknown };

export interface ExtensionState {
  ws_connected: boolean;
  server_url: string;
  paired: boolean;
  paused: boolean;
  pause_reason: string;
  today_delivered: number;
  today_date: string;
  pending_review: number | null;
  last_event_at: number;
}

export function emptyState(serverUrl: string): ExtensionState {
  const today = new Date().toISOString().slice(0, 10);
  return {
    ws_connected: false,
    server_url: serverUrl,
    paired: false,
    paused: false,
    pause_reason: '',
    today_delivered: 0,
    today_date: today,
    pending_review: null,
    last_event_at: 0,
  };
}

/** 跨日清零（纯函数，便于单测） */
export function rolloverIfNeeded(state: ExtensionState, now: Date): ExtensionState {
  const today = now.toISOString().slice(0, 10);
  if (state.today_date === today) {
    return state;
  }
  return { ...state, today_delivered: 0, today_date: today };
}

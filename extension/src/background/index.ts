/**
 * service worker 入口：WS 客户端、消息路由（gateway↔content 双向）、
 * 页面异常聚合上报、状态记账（popup 速览数据源）。
 */

import {
  DEFAULT_SERVER_WS,
  emptyState,
  rolloverIfNeeded,
  type ExtensionState,
  type RuntimeMessage,
  type PageAnomalyPayload,
  type ConfigSyncPayload,
  type GuardPausePayload,
  type ReviewStatsPayload,
} from '../shared/protocol';
import { WsClient, type WsStatus } from './ws';
import {
  routeServerEvent,
  routeContentEvent,
  foldAnomaly,
  expireAnomalies,
  anomalyReportPayload,
  type AnomalyBucket,
} from './router';

const STORAGE_KEYS = {
  serverUrl: 'server_url',
  token: 'bind_token',
  pairingCode: 'pairing_code',
  state: 'ext_state',
} as const;

let client: WsClient | null = null;
let state: ExtensionState = emptyState(DEFAULT_SERVER_WS);
let anomalyBuckets = new Map<string, AnomalyBucket>();
let anomalyFlushTimer: ReturnType<typeof setTimeout> | null = null;

async function loadState(): Promise<void> {
  const stored = await chrome.storage.local.get([STORAGE_KEYS.serverUrl, STORAGE_KEYS.state]);
  const url = typeof stored[STORAGE_KEYS.serverUrl] === 'string'
    ? (stored[STORAGE_KEYS.serverUrl] as string)
    : DEFAULT_SERVER_WS;
  if (typeof stored[STORAGE_KEYS.state] === 'object' && stored[STORAGE_KEYS.state] !== null) {
    state = { ...emptyState(url), ...(stored[STORAGE_KEYS.state] as Partial<ExtensionState>) };
  } else {
    state = emptyState(url);
  }
  state = rolloverIfNeeded(state, new Date());
}

async function persistState(): Promise<void> {
  state.last_event_at = Date.now();
  await chrome.storage.local.set({ [STORAGE_KEYS.state]: state });
  broadcastState();
}

function broadcastState(): void {
  void chrome.runtime.sendMessage({ kind: 'state_broadcast', state } satisfies RuntimeMessage).catch(() => {
    // 无接收方（页面未打开）时忽略
  });
}

function toServer(event: string, payload: unknown): void {
  client?.send(event, payload);
}

function applyPause(p: GuardPausePayload): void {
  state.paused = true;
  state.pause_reason = p.reason_code;
}

function applyConfigSync(p: ConfigSyncPayload): void {
  if (typeof p.paused === 'boolean' && !p.paused && state.paused) {
    // resume：由服务端 config.sync（paused:false）或后续新指令恢复
    state.paused = false;
    state.pause_reason = '';
  }
  if (typeof p.paused === 'boolean' && p.paused) {
    state.paused = true;
  }
}

function handleServerEvent(event: string, payload: unknown): void {
  const decision = routeServerEvent(event);
  if (decision.targets.includes('internal')) {
    if (event === 'guard.pause') {
      applyPause((payload ?? {}) as GuardPausePayload);
    } else if (event === 'config.sync') {
      applyConfigSync((payload ?? {}) as ConfigSyncPayload);
    } else if (event === 'review.stats') {
      const p = (payload ?? {}) as ReviewStatsPayload;
      if (typeof p.pending_count === 'number' && Number.isFinite(p.pending_count)) {
        state.pending_review = Math.max(0, Math.round(p.pending_count));
      }
    }
  }
  if (decision.targets.includes('content')) {
    void chrome.runtime.sendMessage({ kind: 'from_server', event, payload } satisfies RuntimeMessage).catch(() => {});
  }
  if (decision.targets.includes('popup')) {
    void chrome.runtime.sendMessage({ kind: 'from_server', event, payload } satisfies RuntimeMessage).catch(() => {});
  }
  void persistState();
}

function handleContentEvent(event: string, payload: unknown): void {
  const r = routeContentEvent(event);
  if (r.internal) {
    state = rolloverIfNeeded(state, new Date());
    if (event === 'deliver.result') {
      const p = payload as { ok?: boolean };
      if (p?.ok) {
        state.today_delivered += 1;
      }
    }
  }
  if (r.toServer) {
    if (event === 'page.anomaly') {
      foldAndReport(payload as PageAnomalyPayload);
    } else {
      toServer(event, payload);
    }
  }
  void persistState();
}

/** 页面异常聚合上报：窗口期去重计数，flush 时一次性发 page.anomaly（count 合并） */
function foldAndReport(p: PageAnomalyPayload): void {
  if (!p || typeof p.type !== 'string') {
    return;
  }
  anomalyBuckets = foldAnomaly(anomalyBuckets, p.type, p.payload ?? {}, Date.now());
  if (!anomalyFlushTimer) {
    anomalyFlushTimer = setTimeout(flushAnomalies, 5000);
  }
}

function flushAnomalies(): void {
  anomalyFlushTimer = null;
  const [due, keep] = expireAnomalies(anomalyBuckets, Date.now());
  anomalyBuckets = keep;
  for (const b of due) {
    toServer('page.anomaly', {
      ...anomalyReportPayload(b),
      url_hint: '',
      ts: b.first_ts,
    });
  }
}

async function startClient(): Promise<void> {
  client?.close();
  const stored = await chrome.storage.local.get([STORAGE_KEYS.serverUrl, STORAGE_KEYS.token, STORAGE_KEYS.pairingCode]);
  const url = typeof stored[STORAGE_KEYS.serverUrl] === 'string'
    ? (stored[STORAGE_KEYS.serverUrl] as string)
    : DEFAULT_SERVER_WS;
  const token = typeof stored[STORAGE_KEYS.token] === 'string' ? (stored[STORAGE_KEYS.token] as string) : '';
  const pairingCode = typeof stored[STORAGE_KEYS.pairingCode] === 'string'
    ? (stored[STORAGE_KEYS.pairingCode] as string)
    : undefined;
  state.server_url = url;
  state.paired = token.length > 0;

  client = new WsClient({
    url,
    token,
    pairingCode,
    onStatus(status: WsStatus) {
      state.ws_connected = status === 'connected';
      void persistState();
    },
    onServerEvent(env) {
      handleServerEvent(env.event, env.payload);
    },
    onAck(event, payload) {
      if (event === 'hello_ack') {
        const p = payload as { paired?: boolean; bind_token?: string };
        if (p?.paired && typeof p.bind_token === 'string' && p.bind_token) {
          // 配对成功：落绑定 token（§5.1 hello_ack），清一次性配对码
          void chrome.storage.local.set({ bind_token: p.bind_token }).then(() => {
            void chrome.storage.local.remove('pairing_code');
          });
          state.paired = true;
          void persistState();
        }
      }
    },
  });
  client.connect();
}

chrome.runtime.onInstalled.addListener(() => {
  void startClient();
});

chrome.runtime.onStartup.addListener(() => {
  void startClient();
});

chrome.runtime.onMessage.addListener((msg: RuntimeMessage, _sender, sendResponse) => {
  switch (msg.kind) {
    case 'to_server':
      handleContentEvent(msg.event, msg.payload);
      sendResponse({ ok: true });
      break;
    case 'state_query':
      state = rolloverIfNeeded(state, new Date());
      sendResponse({ ok: true, state });
      break;
    case 'inject_message':
      // content-script 已过滤，桥消息中的聊天事件按 chat.message 上行
      handleContentEvent(msg.event, msg.payload);
      sendResponse({ ok: true });
      break;
    default:
      break;
  }
  return false;
});

// storage 变更（options 页改服务地址/配对码）→ 重建连接
chrome.storage.onChanged.addListener((changes, area) => {
  if (area !== 'local') {
    return;
  }
  if (changes[STORAGE_KEYS.serverUrl] || changes[STORAGE_KEYS.pairingCode] || changes[STORAGE_KEYS.token]) {
    void startClient();
  }
});

// service worker 冷启动兜底（MV3 随时被杀后拉起）
void loadState().then(() => {
  void startClient();
});

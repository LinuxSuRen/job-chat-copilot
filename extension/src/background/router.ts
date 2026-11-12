/**
 * 消息路由表（纯函数，单测覆盖）。
 * background 是 gateway(WS) ↔ content-script ↔ popup 的唯一转发桥。
 */

export type RouteTarget = 'content' | 'popup' | 'internal' | 'drop';

export interface RouteDecision {
  targets: readonly RouteTarget[];
  reason: string;
}

/** 服务端事件 → 分发目标（hello_ack 在 ws.ts 内 Ack 路径处理，不经此表） */
const SERVER_EVENT_ROUTES: Record<string, readonly RouteTarget[]> = {
  'jobs.scan': ['content'],
  'deliver.next': ['content'],
  'chat.send': ['content'],
  'chat.poll': ['content'],
  'guard.pause': ['content', 'popup', 'internal'],
  'config.sync': ['content', 'internal'],
  'review.stats': ['internal', 'popup'],
};

/** content-script 上报事件 → 是否需要本地记账 + 是否转发服务端 */
const CONTENT_EVENT_ROUTES: Record<string, { internal: boolean; toServer: boolean }> = {
  'jobs.list': { internal: false, toServer: true },
  'chat.message': { internal: true, toServer: true },
  'chat.snapshot': { internal: true, toServer: true },
  'deliver.result': { internal: true, toServer: true },
  'page.anomaly': { internal: true, toServer: true },
};

export function routeServerEvent(event: string): RouteDecision {
  const targets = SERVER_EVENT_ROUTES[event];
  if (targets) {
    return { targets, reason: 'contract' };
  }
  // 未知服务端事件：转发给 popup 展示（向前兼容），不投递给 content（避免误执行）
  return { targets: ['popup'], reason: 'unknown-server-event' };
}

export function routeContentEvent(
  event: string,
): { internal: boolean; toServer: boolean; reason: string } {
  const r = CONTENT_EVENT_ROUTES[event];
  if (r) {
    return { ...r, reason: 'contract' };
  }
  return { internal: false, toServer: false, reason: 'unknown-content-event' };
}

/**
 * 页面异常聚合：同 type 的 page.anomaly 在窗口期内去重合并计数，
 * 窗口结束后一次性上报（type+count），避免 selector_miss 风暴打爆 WS。
 */
export interface AnomalyBucket {
  type: string;
  first_ts: number;
  last_ts: number;
  count: number;
  sample_payload: Record<string, unknown>;
}

export const ANOMALY_WINDOW_MS = 5000;

export function foldAnomaly(
  buckets: Map<string, AnomalyBucket>,
  type: string,
  payload: Record<string, unknown>,
  now: number,
): Map<string, AnomalyBucket> {
  const next = new Map(buckets);
  const existing = next.get(type);
  if (existing && now - existing.first_ts < ANOMALY_WINDOW_MS) {
    next.set(type, {
      ...existing,
      last_ts: now,
      count: existing.count + 1,
    });
    return next;
  }
  next.set(type, { type, first_ts: now, last_ts: now, count: 1, sample_payload: payload });
  return next;
}

/** 取出到期（窗口关闭）的桶：返回 [到期的, 仍需保留的] */
export function expireAnomalies(
  buckets: Map<string, AnomalyBucket>,
  now: number,
): [AnomalyBucket[], Map<string, AnomalyBucket>] {
  const due: AnomalyBucket[] = [];
  const keep = new Map<string, AnomalyBucket>();
  for (const b of buckets.values()) {
    if (now - b.first_ts >= ANOMALY_WINDOW_MS) {
      due.push(b);
    } else {
      keep.set(b.type, b);
    }
  }
  return [due, keep];
}

/** 聚合后的上报载荷 */
export function anomalyReportPayload(b: AnomalyBucket): Record<string, unknown> {
  return {
    type: b.type,
    payload: {
      count: b.count,
      window_ms: b.last_ts - b.first_ts,
      sample: b.sample_payload,
    },
  };
}

/**
 * MAIN_WORLD 注入脚本 → content-script 的 postMessage 桥。
 * 哨兵字段防回环：注入脚本自身 hook 了 postMessage 所在的 window，
 * 若页面（或我们自己的消息）被再次捕获，凭 source 字段直接丢弃。
 */

export const BRIDGE_SOURCE = '__job_chat_copilot_bridge__';

export type BridgeDirection = 'page->cs';

export interface BridgeMessage {
  source: typeof BRIDGE_SOURCE;
  direction: BridgeDirection;
  /** 捕获来源通道 */
  channel: 'websocket' | 'xhr' | 'fetch';
  url: string;
  data: unknown;
  ts: number;
}

export function makeBridgeMessage(
  channel: BridgeMessage['channel'],
  url: string,
  data: unknown,
): BridgeMessage {
  return { source: BRIDGE_SOURCE, direction: 'page->cs', channel, url, data, ts: Date.now() };
}

/**
 * 过滤函数（纯函数，单测覆盖）：
 *  - 非 object / 非 window.MessageEvent 结构 → null
 *  - source 不等于哨兵 → null（页面噪音，或我们自己发出的消息被页面转发）
 *  - direction 不是 'page->cs' → null（防回环：content-script 不会向页面发桥消息，
 *    出现该方向说明消息被原样弹回）
 *  - 通过校验后原样返回结构化消息
 */
export function filterBridgeEvent(ev: unknown): BridgeMessage | null {
  if (typeof ev !== 'object' || ev === null) {
    return null;
  }
  const data = (ev as { data?: unknown }).data;
  if (typeof data !== 'object' || data === null) {
    return null;
  }
  const candidate = data as Partial<BridgeMessage> & Record<string, unknown>;
  if (candidate.source !== BRIDGE_SOURCE) {
    return null;
  }
  if (candidate.direction !== 'page->cs') {
    return null;
  }
  if (
    (candidate.channel !== 'websocket' && candidate.channel !== 'xhr' && candidate.channel !== 'fetch') ||
    typeof candidate.url !== 'string' ||
    typeof candidate.ts !== 'number'
  ) {
    return null;
  }
  return {
    source: BRIDGE_SOURCE,
    direction: 'page->cs',
    channel: candidate.channel,
    url: candidate.url,
    data: candidate.data ?? null,
    ts: candidate.ts,
  };
}

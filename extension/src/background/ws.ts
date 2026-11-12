/**
 * WS 客户端：连接本地服务 ws://127.0.0.1:7422/ws/ext。
 * 自动重连（基准 2s + 抖动，避免多插件实例同步风暴）。
 * MV3 service worker 随时可能被杀，这里不做复杂状态，全部状态落 chrome.storage。
 */

import {
  DEFAULT_SERVER_WS,
  RECONNECT_BASE_MS,
  EXT_VERSION,
  encodeEnvelope,
  parseEnvelope,
  type WsEnvelope,
} from '../shared/protocol';

export type WsStatus = 'disconnected' | 'connecting' | 'connected';

export interface WsClientOptions {
  url: string;
  token: string;
  pairingCode?: string | undefined;
  onStatus: (status: WsStatus) => void;
  onServerEvent: (env: WsEnvelope) => void;
  /** 服务端回执（如配对成功下发绑定 token） */
  onAck?: (event: string, payload: unknown) => void;
}

export class WsClient {
  private ws: WebSocket | null = null;
  private closedByUs = false;
  private retryCount = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(private readonly opts: WsClientOptions) {}

  connect(): void {
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return;
    }
    this.closedByUs = false;
    this.opts.onStatus('connecting');
    let ws: WebSocket;
    try {
      ws = new WebSocket(this.opts.url);
    } catch {
      this.scheduleReconnect();
      return;
    }
    this.ws = ws;

    ws.onopen = () => {
      this.retryCount = 0;
      this.opts.onStatus('connected');
      // hello：携带绑定 token（或首次配对码）
      ws.send(
        encodeEnvelope('hello', {
          token: this.opts.token,
          ext_version: EXT_VERSION,
          ...(this.opts.pairingCode ? { pairing_code: this.opts.pairingCode } : {}),
        }),
      );
    };

    ws.onmessage = (ev: MessageEvent) => {
      if (typeof ev.data !== 'string') {
        return;
      }
      const env = parseEnvelope(ev.data);
      if (!env) {
        return;
      }
      // hello_ack：服务端确认鉴权/下发绑定 token
      if (env.event === 'hello_ack' || env.event === 'hello.error') {
        this.opts.onAck?.(env.event, env.payload);
        return;
      }
      this.opts.onServerEvent(env);
    };

    ws.onclose = () => {
      this.ws = null;
      this.opts.onStatus('disconnected');
      if (!this.closedByUs) {
        this.scheduleReconnect();
      }
    };

    ws.onerror = () => {
      // onclose 随后触发，统一在那里重连
    };
  }

  private scheduleReconnect(): void {
    if (this.closedByUs || this.timer) {
      return;
    }
    const jitter = Math.random() * 0.5 * RECONNECT_BASE_MS;
    const delay = RECONNECT_BASE_MS + jitter + Math.min(this.retryCount, 5) * 250;
    this.retryCount += 1;
    this.timer = setTimeout(() => {
      this.timer = null;
      this.connect();
    }, delay);
  }

  send(event: string, payload: unknown): boolean {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(encodeEnvelope(event, payload));
      return true;
    }
    return false;
  }

  get status(): WsStatus {
    if (!this.ws) {
      return 'disconnected';
    }
    if (this.ws.readyState === WebSocket.OPEN) {
      return 'connected';
    }
    return this.ws.readyState === WebSocket.CONNECTING ? 'connecting' : 'disconnected';
  }

  /** 静默关闭：不重连（服务地址变更重建时用） */
  close(): void {
    this.closedByUs = true;
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
    this.ws?.close();
    this.ws = null;
    this.opts.onStatus('disconnected');
  }
}

export { DEFAULT_SERVER_WS };

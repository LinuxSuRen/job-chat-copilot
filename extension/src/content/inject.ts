/**
 * MAIN_WORLD 注入脚本：hook window.WebSocket / XMLHttpRequest / fetch，
 * 捕获 zhipin 聊天消息帧，经 window.postMessage（带哨兵字段防回环）发给 content-script。
 *
 * 注意：
 *  - 本文件运行在页面世界，不能 import chrome 类型相关的模块；
    只从 shared 引入纯常量/纯函数（打包后为 IIFE 单文件，无外部依赖）。
 *  - 不解析、不猜测私有协议语义：原帧透传，结构化识别在 content-script（bridge.ts）完成。
 *  - 不逆向任何签名（红线）。
 */

import { makeBridgeMessage } from '../shared/sentinel';

declare const window: Window & {
  WebSocket: typeof WebSocket;
  XMLHttpRequest: typeof XMLHttpRequest;
  fetch: typeof fetch;
};

function post(channel: 'websocket' | 'xhr' | 'fetch', url: string, data: unknown): void {
  try {
    window.postMessage(makeBridgeMessage(channel, url, data), '*');
  } catch {
    // 序列化失败（不可克隆对象）：跳过，DOM 兜底扫描补齐
  }
}

function safeParse(text: unknown): unknown {
  if (typeof text !== 'string' || text.length === 0) {
    return null;
  }
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

// ---------------------------------------------------------------------------
// WebSocket hook
// ---------------------------------------------------------------------------

const OriginalWebSocket = window.WebSocket;

function patchWebSocket(): void {
  class CopilotWebSocket extends OriginalWebSocket {
    constructor(url: string | URL, protocols?: string | string[]) {
      super(url, protocols);
      this.addEventListener('message', (ev: MessageEvent) => {
        const parsed = typeof ev.data === 'string' ? safeParse(ev.data) : null;
        if (parsed !== null) {
          post('websocket', String(url), parsed);
        }
      });
    }
  }
  Object.defineProperty(window, 'WebSocket', {
    configurable: true,
    writable: true,
    value: CopilotWebSocket,
  });
}

// ---------------------------------------------------------------------------
// XMLHttpRequest hook
// ---------------------------------------------------------------------------

function patchXHR(): void {
  const originalOpen = window.XMLHttpRequest.prototype.open;
  window.XMLHttpRequest.prototype.open = function (
    this: XMLHttpRequest & { __copilot_url?: string; __copilot_patched?: boolean },
    method: string,
    url: string | URL,
    ...rest: unknown[]
  ): void {
    this.__copilot_url = String(url);
    return (originalOpen as (...args: unknown[]) => void).apply(this, [method, url, ...rest] as never);
  };
  const originalSend = window.XMLHttpRequest.prototype.send;
  window.XMLHttpRequest.prototype.send = function (this: XMLHttpRequest & { __copilot_url?: string }, body?: unknown): void {
    this.addEventListener('load', () => {
      const parsed = safeParse(this.responseText);
      if (parsed !== null && this.__copilot_url) {
        post('xhr', this.__copilot_url, parsed);
      }
    });
    return (originalSend as (...args: unknown[]) => void).call(this, body);
  };
}

// ---------------------------------------------------------------------------
// fetch hook
// ---------------------------------------------------------------------------

function patchFetch(): void {
  const originalFetch = window.fetch;
  window.fetch = function (input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    return originalFetch.call(window, input, init).then((response: Response) => {
      const cloned = response.clone();
      void cloned
        .text()
        .then((text: string) => {
          const parsed = safeParse(text);
          if (parsed !== null) {
            post('fetch', url, parsed);
          }
        })
        .catch(() => {});
      return response;
    });
  };
}

function main(): void {
  // document_start 每个文档只执行一次，无需幂等标记
  patchWebSocket();
  patchXHR();
  patchFetch();
}

main();

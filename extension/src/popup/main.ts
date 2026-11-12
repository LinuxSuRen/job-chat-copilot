/** popup 状态速览：连接状态、今日投递数、待审数量（均来自 background 的 WS 事件缓存） */

import { emptyState, rolloverIfNeeded, type ExtensionState, type RuntimeMessage } from '../shared/protocol';

const $ = <T extends HTMLElement>(id: string): T => document.getElementById(id) as T;

function render(state: ExtensionState): void {
  const dot = $<HTMLSpanElement>('ws-dot');
  dot.className = `dot ${state.ws_connected ? 'ok' : 'bad'}`;
  $<HTMLSpanElement>('ws-status').textContent = state.ws_connected ? '已连接' : '未连接';
  $<HTMLSpanElement>('delivered').textContent = String(state.today_delivered);
  // 待审数量：来自服务端 review.stats 事件（§5.1），未收到时显示 —
  $<HTMLSpanElement>('pending').textContent =
    state.pending_review === null ? '—' : String(state.pending_review);

  const box = $<HTMLDivElement>('paused-box');
  if (state.paused) {
    box.hidden = false;
    $<HTMLSpanElement>('paused-reason').textContent = state.pause_reason || '未知原因';
  } else {
    box.hidden = true;
  }
}

function refresh(): void {
  void chrome.runtime
    .sendMessage({ kind: 'state_query' } satisfies RuntimeMessage)
    .then((resp: unknown) => {
      if (resp && typeof resp === 'object' && 'state' in resp) {
        render(rolloverIfNeeded((resp as { state: ExtensionState }).state, new Date()));
      } else {
        render(emptyState(''));
      }
    })
    .catch(() => render(emptyState('')));
}

chrome.runtime.onMessage.addListener((msg: RuntimeMessage) => {
  if (msg.kind === 'state_broadcast') {
    render(msg.state);
  }
});

refresh();

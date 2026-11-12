/** options 配对页逻辑：服务地址 + 一次性配对码，连接状态实时展示 */

import { DEFAULT_SERVER_WS, emptyState, rolloverIfNeeded, type ExtensionState, type RuntimeMessage } from '../shared/protocol';

const $ = <T extends HTMLElement>(id: string): T => document.getElementById(id) as T;

function render(state: ExtensionState): void {
  const dot = $<HTMLSpanElement>('ws-dot');
  const wsStatus = $<HTMLSpanElement>('ws-status');
  if (state.ws_connected) {
    dot.className = 'dot ok';
    wsStatus.textContent = '已连接';
  } else {
    dot.className = 'dot bad';
    wsStatus.textContent = '未连接（自动重连中）';
  }
  $<HTMLSpanElement>('pair-status').textContent = state.paired ? '已绑定' : '未绑定';
  const pauseEl = $<HTMLSpanElement>('pause-status');
  pauseEl.textContent = state.paused ? `已暂停（${state.pause_reason || '未知原因'}）` : '正常';
}

async function loadState(): Promise<ExtensionState> {
  const stored = await chrome.storage.local.get('ext_state');
  if (typeof stored['ext_state'] === 'object' && stored['ext_state'] !== null) {
    const url = typeof stored['server_url'] === 'string' ? stored['server_url'] : DEFAULT_SERVER_WS;
    return rolloverIfNeeded({ ...emptyState(url), ...(stored['ext_state'] as Partial<ExtensionState>) }, new Date());
  }
  return emptyState(DEFAULT_SERVER_WS);
}

async function init(): Promise<void> {
  const state = await loadState();
  render(state);
  const urlInput = $<HTMLInputElement>('server-url');
  const codeInput = $<HTMLInputElement>('pairing-code');

  const stored = await chrome.storage.local.get(['server_url', 'bind_token', 'pairing_code']);
  urlInput.value = typeof stored['server_url'] === 'string' ? (stored['server_url'] as string) : DEFAULT_SERVER_WS;
  codeInput.value = typeof stored['pairing_code'] === 'string' ? (stored['pairing_code'] as string) : '';
  codeInput.disabled = typeof stored['bind_token'] === 'string' && (stored['bind_token'] as string).length > 0;

  $<HTMLButtonElement>('save').addEventListener('click', async () => {
    const url = urlInput.value.trim() || DEFAULT_SERVER_WS;
    const code = codeInput.value.trim();
    const patch: Record<string, string> = { server_url: url };
    if (code) {
      patch['pairing_code'] = code;
    }
    await chrome.storage.local.set(patch);
  });

  chrome.runtime.onMessage.addListener((msg: RuntimeMessage) => {
    if (msg.kind === 'state_broadcast') {
      render(msg.state);
    }
  });

  // popup/options 打开期间周期刷新（state_broadcast 可能错过）
  setInterval(() => {
    void chrome.runtime.sendMessage({ kind: 'state_query' } satisfies RuntimeMessage).then((resp) => {
      if (resp && typeof resp === 'object' && 'state' in resp) {
        render((resp as { state: ExtensionState }).state);
      }
    });
  }, 3000);
}

void init();

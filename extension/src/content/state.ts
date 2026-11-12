/**
 * guard.pause 暂停标志：内存 + chrome.storage 双写。
 * 收到 guard.pause 立即置位，暂停一切页面操作；
 * resume 由服务端 config.sync（paused:false）恢复。
 */

import type { ConfigSyncPayload } from '../shared/protocol';
import { normalizeJitterConfig, type JitterConfig } from '../shared/jitter';

export interface GuardState {
  paused: boolean;
  reasonCode: string;
}

const STORAGE_KEY = 'content_guard';

let guard: GuardState = { paused: false, reasonCode: '' };
let jitterCfg: JitterConfig = normalizeJitterConfig(undefined);

export async function loadGuard(): Promise<void> {
  const stored = await chrome.storage.local.get(STORAGE_KEY);
  const v = stored[STORAGE_KEY] as GuardState | undefined;
  if (v && typeof v.paused === 'boolean') {
    guard = { paused: v.paused, reasonCode: typeof v.reasonCode === 'string' ? v.reasonCode : '' };
  }
}

export async function pauseAll(reasonCode: string): Promise<void> {
  guard = { paused: true, reasonCode };
  await chrome.storage.local.set({ [STORAGE_KEY]: guard });
}

export async function applyConfigSync(cfg: Partial<ConfigSyncPayload>): Promise<void> {
  jitterCfg = normalizeJitterConfig(cfg);
  if (cfg.paused === false && guard.paused) {
    guard = { paused: false, reasonCode: '' };
    await chrome.storage.local.set({ [STORAGE_KEY]: guard });
  } else if (cfg.paused === true) {
    await pauseAll('server_config');
  }
}

export function isPaused(): boolean {
  return guard.paused;
}

export function pauseReason(): string {
  return guard.reasonCode;
}

export function currentJitter(): JitterConfig {
  return { ...jitterCfg };
}

/**
 * 人化抖动：节奏基准由服务端 config.sync 下发（min_interval_ms + jitter_ratio），
 * 本地执行时再叠加一次小随机（参考基准值的 ±jitter_ratio/2）。
 * 纯函数，便于单测。
 */

export interface JitterConfig {
  min_interval_ms: number;
  jitter_ratio: number;
}

export const DEFAULT_JITTER: JitterConfig = { min_interval_ms: 300, jitter_ratio: 0.25 };

export function normalizeJitterConfig(raw: unknown): JitterConfig {
  const src = (typeof raw === 'object' && raw !== null ? raw : {}) as Record<string, unknown>;
  const min = typeof src['min_interval_ms'] === 'number' && src['min_interval_ms'] >= 0
    ? src['min_interval_ms']
    : DEFAULT_JITTER.min_interval_ms;
  const ratio = typeof src['jitter_ratio'] === 'number' && src['jitter_ratio'] >= 0
    ? Math.min(src['jitter_ratio'], 1)
    : DEFAULT_JITTER.jitter_ratio;
  return { min_interval_ms: Math.round(min), jitter_ratio: ratio };
}

/** 在 [min, min*(1+ratio)] 区间内取一个等待时长（ms）。 */
export function humanDelayMs(cfg: JitterConfig, rand: () => number = Math.random): number {
  const base = Math.max(0, cfg.min_interval_ms);
  const span = base * cfg.jitter_ratio;
  const v = base + span * rand();
  return Math.round(v);
}

export function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

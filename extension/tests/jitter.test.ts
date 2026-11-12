import { describe, expect, it } from 'vitest';
import { normalizeJitterConfig, humanDelayMs } from '../src/shared/jitter';
import { rolloverIfNeeded, emptyState, encodeEnvelope, parseEnvelope } from '../src/shared/protocol';

describe('人化抖动', () => {
  it('非法配置回退默认值，ratio 封顶 1', () => {
    expect(normalizeJitterConfig(undefined)).toEqual({ min_interval_ms: 300, jitter_ratio: 0.25 });
    expect(normalizeJitterConfig({ min_interval_ms: -5, jitter_ratio: 9 })).toEqual({
      min_interval_ms: 300,
      jitter_ratio: 1,
    });
    expect(normalizeJitterConfig({ min_interval_ms: 1000, jitter_ratio: 0.2 })).toEqual({
      min_interval_ms: 1000,
      jitter_ratio: 0.2,
    });
  });

  it('等待时长落在 [min, min*(1+ratio)] 内', () => {
    const cfg = { min_interval_ms: 400, jitter_ratio: 0.5 };
    for (let i = 0; i < 50; i++) {
      const v = humanDelayMs(cfg, () => 0.999);
      expect(v).toBeGreaterThanOrEqual(400);
      expect(v).toBeLessThanOrEqual(600 + 1);
    }
    expect(humanDelayMs(cfg, () => 0)).toBe(400);
  });
});

describe('协议信封', () => {
  it('编解码往返', () => {
    const raw = encodeEnvelope('chat.send', { conv_key: 'k', content: 'hi' });
    const env = parseEnvelope(raw);
    expect(env?.event).toBe('chat.send');
    expect(env?.payload).toEqual({ conv_key: 'k', content: 'hi' });
  });

  it('非 JSON 与缺 event 字段返回 null', () => {
    expect(parseEnvelope('not json')).toBeNull();
    expect(parseEnvelope('{"payload":1}')).toBeNull();
  });
});

describe('今日计数跨日清零', () => {
  it('同日不清零，跨日归零', () => {
    const s = { ...emptyState('ws://x'), today_delivered: 5, today_date: '2026-11-12' };
    expect(rolloverIfNeeded(s, new Date('2026-11-12T15:00:00Z')).today_delivered).toBe(5);
    expect(rolloverIfNeeded(s, new Date('2026-11-13T01:00:00Z')).today_delivered).toBe(0);
  });
});

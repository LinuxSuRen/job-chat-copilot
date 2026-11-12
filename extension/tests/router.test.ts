import { describe, expect, it } from 'vitest';
import {
  routeServerEvent,
  routeContentEvent,
  foldAnomaly,
  expireAnomalies,
  anomalyReportPayload,
  ANOMALY_WINDOW_MS,
} from '../src/background/router';

describe('消息路由', () => {
  it('契约内服务端事件路由到 content', () => {
    expect(routeServerEvent('jobs.scan').targets).toEqual(['content']);
    expect(routeServerEvent('deliver.next').targets).toEqual(['content']);
    expect(routeServerEvent('chat.send').targets).toEqual(['content']);
    expect(routeServerEvent('chat.poll').targets).toEqual(['content']);
  });

  it('review.stats 路由 internal+popup（popup 展示待审数）', () => {
    const d = routeServerEvent('review.stats');
    expect(d.targets).toContain('internal');
    expect(d.targets).toContain('popup');
  });

  it('guard.pause 同时路由 content/popup/internal', () => {
    const d = routeServerEvent('guard.pause');
    expect(d.targets).toContain('content');
    expect(d.targets).toContain('popup');
    expect(d.targets).toContain('internal');
  });

  it('未知服务端事件只给 popup 展示，不投递 content（防误执行）', () => {
    const d = routeServerEvent('evil.execute');
    expect(d.targets).toEqual(['popup']);
  });

  it('契约内 content 事件上行服务端（jobs.list 无需本地记账）', () => {
    for (const e of ['chat.message', 'chat.snapshot', 'deliver.result', 'page.anomaly']) {
      const r = routeContentEvent(e);
      expect(r.internal).toBe(true);
      expect(r.toServer).toBe(true);
    }
    const scan = routeContentEvent('jobs.list');
    expect(scan.toServer).toBe(true);
    expect(scan.internal).toBe(false);
  });

  it('未知 content 事件丢弃', () => {
    const r = routeContentEvent('chat.typing');
    expect(r.toServer).toBe(false);
  });
});

describe('页面异常聚合', () => {
  it('窗口期内同类型去重累加', () => {
    let buckets = foldAnomaly(new Map(), 'selector_miss', { a: 1 }, 1000);
    buckets = foldAnomaly(buckets, 'selector_miss', { b: 2 }, 2000);
    buckets = foldAnomaly(buckets, 'selector_miss', { c: 3 }, 3000);
    const b = buckets.get('selector_miss');
    expect(b?.count).toBe(3);
    expect(b?.first_ts).toBe(1000);
    expect(b?.sample_payload).toEqual({ a: 1 });
  });

  it('不同类型独立聚合', () => {
    let buckets = foldAnomaly(new Map(), 'captcha', {}, 1000);
    buckets = foldAnomaly(buckets, 'logged_out', {}, 1500);
    expect(buckets.size).toBe(2);
  });

  it('窗口过期后重新开桶', () => {
    let buckets = foldAnomaly(new Map(), 'selector_miss', {}, 0);
    buckets = foldAnomaly(buckets, 'selector_miss', {}, ANOMALY_WINDOW_MS + 1);
    expect(buckets.get('selector_miss')?.count).toBe(1);
  });

  it('到期桶进入上报列表，未到期保留', () => {
    let buckets = foldAnomaly(new Map(), 'captcha', {}, 0);
    buckets = foldAnomaly(buckets, 'selector_miss', {}, 100); // 未到期
    const [due, keep] = expireAnomalies(buckets, ANOMALY_WINDOW_MS + 1);
    expect(due.map((d) => d.type)).toEqual(['captcha']);
    expect(keep.size).toBe(1);
    expect(keep.get('selector_miss')?.count).toBe(1);
  });

  it('上报载荷带 count 与 sample', () => {
    let buckets = foldAnomaly(new Map(), 'logged_out', { url_hint: 'login-redirect' }, 0);
    buckets = foldAnomaly(buckets, 'logged_out', {}, 1000);
    const [due] = expireAnomalies(buckets, ANOMALY_WINDOW_MS + 1);
    const payload = anomalyReportPayload(due![0]!);
    expect(payload['type']).toBe('logged_out');
    expect((payload['payload'] as Record<string, unknown>)['count']).toBe(2);
    expect((payload['payload'] as Record<string, unknown>)['sample']).toEqual({ url_hint: 'login-redirect' });
  });
});

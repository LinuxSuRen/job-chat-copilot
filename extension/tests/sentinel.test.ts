import { describe, expect, it } from 'vitest';
import {
  BRIDGE_SOURCE,
  filterBridgeEvent,
  makeBridgeMessage,
  type BridgeMessage,
} from '../src/shared/sentinel';

describe('postMessage 哨兵过滤', () => {
  it('接受合法的注入脚本桥消息', () => {
    const msg = makeBridgeMessage('websocket', 'wss://chat.zhipin.com', { foo: 1 });
    const ev = { data: msg };
    expect(filterBridgeEvent(ev)).toEqual(msg);
  });

  it('拒绝非对象事件与无 data 事件', () => {
    expect(filterBridgeEvent(null)).toBeNull();
    expect(filterBridgeEvent('string')).toBeNull();
    expect(filterBridgeEvent({})).toBeNull();
  });

  it('拒绝哨兵字段不符的页面噪音', () => {
    expect(filterBridgeEvent({ data: { source: 'other', direction: 'page->cs', channel: 'websocket', url: 'u', ts: 1 } })).toBeNull();
    expect(filterBridgeEvent({ data: { hello: 'world' } })).toBeNull();
  });

  it('拒绝反向消息（防回环）', () => {
    const echo: BridgeMessage = { ...makeBridgeMessage('xhr', '/x', {}), direction: 'page->cs' };
    expect(filterBridgeEvent({ data: echo })).not.toBeNull();
    expect(
      filterBridgeEvent({ data: { ...echo, direction: 'cs->page' as never } }),
    ).toBeNull();
  });

  it('拒绝结构不完整的桥消息', () => {
    const base = makeBridgeMessage('fetch', '/y', null);
    expect(filterBridgeEvent({ data: { ...base, channel: 'webrtc' as never } })).toBeNull();
    expect(filterBridgeEvent({ data: { ...base, url: 123 as never } })).toBeNull();
    expect(filterBridgeEvent({ data: { ...base, ts: 'now' as never } })).toBeNull();
  });

  it('哨兵常量不与常见页面字段冲突', () => {
    expect(BRIDGE_SOURCE).toMatch(/^__/);
  });
});

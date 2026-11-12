import { describe, expect, it } from 'vitest';
import { looksLikeChatFrame, extractChatMessage } from '../src/content/bridge';

describe('拦截帧识别（bridge）', () => {
  it('URL 命中聊天通道关键词即粗过', () => {
    expect(looksLikeChatFrame('wss://wld.zhipin.com/chat', {})).toBe(true);
    expect(looksLikeChatFrame('https://www.zhipin.com/wapi/zpb-ops/im', {})).toBe(true);
  });

  it('数据侧特征（lastContent/msgType）也算疑似', () => {
    expect(looksLikeChatFrame('https://example.com/api', { lastContent: 'hi' })).toBe(true);
    expect(looksLikeChatFrame('https://example.com/api', { msgType: 1 })).toBe(true);
    expect(looksLikeChatFrame('https://example.com/api', { foo: 'bar' })).toBe(false);
    expect(looksLikeChatFrame('x', 'string')).toBe(false);
  });
});

describe('聊天消息结构化（extractChatMessage）', () => {
  it('单条消息体：encryptConvId + text', () => {
    const m = extractChatMessage({
      encryptConvId: 'Cv77',
      direction: 'in',
      text: '你好，还在看机会吗？',
      msgType: 1,
      createTime: 1762900000,
    });
    expect(m).toMatchObject({
      conv_key: 'Cv77',
      direction: 'in',
      content: '你好，还在看机会吗？',
      msg_type: 'text',
    });
    expect(m?.ts).toBeGreaterThan(1e12); // 秒级 → 毫秒
  });

  it('texts 数组拼接与 zpb-ops 包装解包', () => {
    const m = extractChatMessage({
      'zpb-ops': 1,
      data: { encryptConvId: 'Cv88', texts: ['到岗时间', '？'], msgType: 1 },
    });
    expect(m?.content).toBe('到岗时间 ？');
    expect(m?.conv_key).toBe('Cv88');
  });

  it('列表体取第一条可识别消息', () => {
    const m = extractChatMessage({
      data: { messages: [{ foo: 1 }, { conversationId: 'Cv99', text: 'ok', type: 1 }] },
    });
    expect(m?.conv_key).toBe('Cv99');
  });

  it('会话列表帧的 lastContent 兜底', () => {
    const m = extractChatMessage({ encryptFriendId: 'Fx1', lastContent: '3天前活跃' });
    expect(m?.content).toBe('3天前活跃');
    expect(m?.direction).toBe('in'); // 无法判定方向时保守按入站
  });

  it('非聊天帧返回 null', () => {
    expect(extractChatMessage({ code: 0, data: { name: 'job list' } })).toBeNull();
    expect(extractChatMessage('str')).toBeNull();
    expect(extractChatMessage({ encryptConvId: 'Cv1', msgType: 1 })).toBeNull(); // 无内容
  });
});

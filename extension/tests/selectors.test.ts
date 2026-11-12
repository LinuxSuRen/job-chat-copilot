import { describe, expect, it } from 'vitest';
import {
  resolveSelector,
  missPayload,
  extractJobKey,
  extractConvKey,
  type Lookup,
} from '../src/selectors';

function fakeLookup(map: Record<string, Element>, texts: Record<string, Element> = {}): Lookup {
  return {
    querySelector: (sel: string) => map[sel] ?? null,
    getByText: (text: string) => texts[text] ?? null,
  };
}

describe('选择器状态机', () => {
  it('按优先级取第一个命中的候选', () => {
    const el = {} as Element;
    const r = resolveSelector('job_card', fakeLookup({ 'li[class*="job-card"]': el }));
    expect(r.matched).toBe('li[class*="job-card"]');
    expect(r.element).toBe(el);
  });

  it('语义文本候选走文本匹配路径', () => {
    const btn = {} as Element;
    const r = resolveSelector('btn_start_chat', fakeLookup({}, { '立即沟通': btn }));
    expect(r.matched).toBe('text=立即沟通');
    expect(r.element).toBe(btn);
  });

  it('文本聚合目标（A/B 形式）逐个尝试', () => {
    const el = {} as Element;
    const r = resolveSelector('captcha_text', fakeLookup({}, { 安全验证: el }));
    expect(r.element).toBe(el);
  });

  it('全部候选失配返回 matched:null（绝不静默）', () => {
    const r = resolveSelector('chat_input', fakeLookup({}));
    expect(r.matched).toBeNull();
    expect(r.element).toBeNull();
  });

  it('失配 payload 携带目标与全部尝试过的候选', () => {
    const p = missPayload('chat_input', null);
    expect(p['selector_target']).toBe('chat_input');
    expect(Array.isArray(p['tried'])).toBe(true);
    expect((p['tried'] as string[]).length).toBeGreaterThan(0);
  });
});

describe('ID 提取', () => {
  it('从职位详情 URL 提取 job_key', () => {
    expect(extractJobKey('https://www.zhipin.com/job_detail/AbCd123.html?lid=x')).toBe('AbCd123');
    expect(extractJobKey('https://www.zhipin.com/web/geek/job')).toBeNull();
  });

  it('从会话 URL 提取 conv_key（friendId 优先）', () => {
    expect(extractConvKey('https://www.zhipin.com/web/geek/chat?friendId=Fx91')).toBe('Fx91');
    expect(extractConvKey('https://www.zhipin.com/web/geek/chat/Cv77')).toBe('Cv77');
    expect(extractConvKey('https://www.zhipin.com/web/geek/chat')).toBeNull();
  });
});

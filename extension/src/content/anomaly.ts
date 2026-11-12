/**
 * page.anomaly 全类型检测与上报辅助：
 *  - captcha：页面出现滑块/验证特征元素或文案
 *  - logged_out：跳转登录页（URL 或页面内登录特征）
 *  - selector_miss：选择器失配（selectors.ts 上报）
 *  - nav_unexpected：操作过程中 URL 与预期页面不符
 */

import {
  resolveSelector,
  missPayload,
  type SelectorTarget,
  type Lookup,
} from '../selectors';
import type { AnomalyType, PageAnomalyPayload } from '../shared/protocol';

const LOGIN_URL_HINTS = ['/login', 'signin'];

/** 文本匹配辅助：遍历候选 tag，找可见且文本精确/包含命中的元素 */
function getByTextFactory(doc: Document): Lookup {
  return {
    querySelector(sel: string): Element | null {
      return doc.querySelector(sel);
    },
    getByText(text: string, tag?: string): Element | null {
      const scope = tag ? Array.from(doc.getElementsByTagName(tag)) : Array.from(doc.querySelectorAll('div,span,button,a,p,li'));
      for (const el of scope) {
        const t = (el.textContent ?? '').trim();
        if (t && (t === text || t.includes(text))) {
          const style = window.getComputedStyle(el);
          if (style.visibility !== 'hidden' && style.display !== 'none') {
            return el;
          }
        }
      }
      return null;
    },
  };
}

export function lookup(doc: Document): Lookup {
  return getByTextFactory(doc);
}

function makePayload(type: AnomalyType, payload: Record<string, unknown>): PageAnomalyPayload {
  return {
    type,
    payload,
    url_hint: sanitizeUrl(location.href),
    ts: Date.now(),
  };
}

/** URL 脱敏：去掉 query 中的业务参数 */
function sanitizeUrl(url: string): string {
  try {
    const u = new URL(url);
    u.search = '';
    return u.pathname;
  } catch {
    return '';
  }
}

export function detectCaptcha(doc: Document): PageAnomalyPayload | null {
  const lk = getByTextFactory(doc);
  const dialog = resolveSelector('captcha_dialog', lk);
  const text = resolveSelector('captcha_text', lk);
  if (dialog.element || text.element) {
    return makePayload('captcha', {
      dialog_selector: dialog.matched,
      text_selector: text.matched,
    });
  }
  return null;
}

export function detectLoggedOut(doc: Document): PageAnomalyPayload | null {
  const href = location.href;
  if (LOGIN_URL_HINTS.some((h) => href.includes(h))) {
    return makePayload('logged_out', { url_hint: 'login-redirect' });
  }
  const lk = getByTextFactory(doc);
  // 登录态丢失有时表现为业务页内嵌登录框
  if (resolveSelector('login_marker', lk).element && !location.pathname.includes('/chat')) {
    return makePayload('logged_out', { url_hint: 'embedded-login' });
  }
  return null;
}

export function selectorMiss(target: SelectorTarget, sample: Element | null): PageAnomalyPayload {
  return makePayload('selector_miss', missPayload(target, sample));
}

export function navUnexpected(expected: string): PageAnomalyPayload {
  return makePayload('nav_unexpected', { expected_page: expected, actual_path: sanitizeUrl(location.href) });
}

/** 操作前置检查：验证码与登录态优先于一切（红线：验证码一律暂停转人工） */
export function precheckAnomalies(doc: Document): PageAnomalyPayload | null {
  return detectCaptcha(doc) ?? detectLoggedOut(doc);
}

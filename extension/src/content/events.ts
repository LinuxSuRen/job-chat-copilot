/**
 * 真实事件序列派发（docs/design.md §3.3）：
 * 所有点击走 pointerdown → pointerup → click；输入走 focus + 逐字符
 * keydown → input → keyup；绝不使用 el.click() 等合成捷径。
 */

export function isVisible(el: Element): boolean {
  const htmlEl = el as HTMLElement;
  if (htmlEl.offsetWidth === 0 && htmlEl.offsetHeight === 0) {
    return false;
  }
  const style = window.getComputedStyle(htmlEl);
  return style.visibility !== 'hidden' && style.display !== 'none';
}

function pointerInit(el: Element): PointerEventInit {
  const rect = el.getBoundingClientRect();
  const x = rect.left + rect.width / 2;
  const y = rect.top + rect.height / 2;
  return {
    bubbles: true,
    cancelable: true,
    composed: true,
    view: window,
    clientX: x,
    clientY: y,
    button: 0,
    buttons: 1,
    pointerId: 1,
    pointerType: 'mouse',
    isPrimary: true,
  };
}

/** 真实点击序列：pointerdown → pointerup → click */
export function dispatchRealClick(el: Element): boolean {
  if (!isVisible(el)) {
    return false;
  }
  el.dispatchEvent(new PointerEvent('pointerdown', pointerInit(el)));
  el.dispatchEvent(new PointerEvent('pointerup', { ...pointerInit(el), buttons: 0 }));
  const rect = el.getBoundingClientRect();
  el.dispatchEvent(
    new MouseEvent('click', {
      bubbles: true,
      cancelable: true,
      composed: true,
      view: window,
      clientX: rect.left + rect.width / 2,
      clientY: rect.top + rect.height / 2,
      button: 0,
    }),
  );
  return true;
}

function fireKeyboard(el: Element, type: 'keydown' | 'keyup', key: string, keyCode: number): void {
  el.dispatchEvent(
    new KeyboardEvent(type, {
      bubbles: true,
      cancelable: true,
      composed: true,
      view: window,
      key,
      code: key.length === 1 ? `Key${key.toUpperCase()}` : key,
      keyCode,
      which: keyCode,
    }),
  );
}

interface Inputtable {
  value: string;
}

/** 真实输入序列：focus 后逐字符 keydown → input → keyup（带原生 value setter 触发框架监听） */
export function dispatchRealInput(el: Element, text: string): boolean {
  if (!isVisible(el)) {
    return false;
  }
  const target = el as HTMLElement & Inputtable;
  target.focus?.();

  const isContentEditable = el.getAttribute('contenteditable') === 'true';
  const setNativeValue = Object.getOwnPropertyDescriptor(
    isContentEditable ? HTMLElement.prototype : HTMLTextAreaElement.prototype,
    isContentEditable ? 'innerText' : 'value',
  )?.set;

  for (const ch of text) {
    const keyCode = ch.charCodeAt(0);
    fireKeyboard(el, 'keydown', ch, keyCode);
    if (isContentEditable) {
      if (setNativeValue) {
        setNativeValue.call(target, (target.innerText ?? '') + ch);
      } else {
        target.innerText = (target.innerText ?? '') + ch;
      }
    } else {
      if (setNativeValue) {
        setNativeValue.call(target, (target.value ?? '') + ch);
      } else {
        target.value = (target.value ?? '') + ch;
      }
    }
    el.dispatchEvent(
      new InputEvent('input', { bubbles: true, cancelable: true, composed: true, inputType: 'insertText', data: ch }),
    );
    fireKeyboard(el, 'keyup', ch, keyCode);
  }
  el.dispatchEvent(new Event('change', { bubbles: true, composed: true }));
  return true;
}

/** 发送前回车触发（部分输入框需回车而不是点发送按钮） */
export function dispatchEnter(el: Element): boolean {
  if (!isVisible(el)) {
    return false;
  }
  (el as HTMLElement).focus?.();
  fireKeyboard(el, 'keydown', 'Enter', 13);
  fireKeyboard(el, 'keyup', 'Enter', 13);
  return true;
}

/** 页面状态断言辅助：元素存在且可见 */
export function assertVisible(el: Element | null): boolean {
  return el !== null && isVisible(el);
}

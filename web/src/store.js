import { reactive } from 'vue'

// 模块级 reactive store：无状态库
export const store = reactive({
  view: 'overview',
  wsConnected: false,
  pluginConnected: false,
  pluginVersion: '',
  // 待审核草稿角标
  pendingDrafts: 0,
  // 页面级异常告警（验证码/掉登录等，需手动确认消除）
  alerts: [],
})

const listeners = new Set()

// 轻量事件总线：WS 事件驱动各视图刷新
export function busEmit(event, payload) {
  listeners.forEach((fn) => fn(event, payload))
}

export function busOn(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

// 稳定 code → 中文文案映射，未知 code 走兜底
export const ANOMALY_TEXT = {
  captcha: '检测到验证码或滑块，任务已暂停，请在浏览器中完成人工验证后恢复任务',
  logged_out: '检测到登录态丢失，任务已暂停，请重新登录 Boss直聘后恢复任务',
  selector_miss: '页面结构识别失败，任务已暂停，请检查 zhipin 页面是否改版',
  nav_unexpected: '页面跳转异常，任务已暂停，请人工检查浏览器页面状态',
}

export function anomalyText(type) {
  return ANOMALY_TEXT[type] || `检测到页面异常（${type}），任务已暂停，请人工检查`
}

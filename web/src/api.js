// 唯一的 HTTP 封装：所有请求经 request()，错误统一为 ApiError(code)
export class ApiError extends Error {
  constructor(message, code) {
    super(message)
    this.code = code
  }
}

const TIMEOUT_MS = 15000

async function request(method, path, body) {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), TIMEOUT_MS)
  let resp
  try {
    resp = await fetch(path, {
      method,
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal: controller.signal,
    })
  } catch (err) {
    if (err && err.name === 'AbortError') {
      throw new ApiError('请求超时，请检查网络后重试', 'timeout')
    }
    throw new ApiError('无法连接本地服务，请确认服务已启动', 'network')
  } finally {
    clearTimeout(timer)
  }
  if (!resp.ok) {
    let code = `http_${resp.status}`
    let message = `请求失败（HTTP ${resp.status}）`
    try {
      const err = await resp.json()
      const envelope = err && err.error ? err.error : err
      if (envelope) {
        if (envelope.code) code = envelope.code
        if (envelope.message) message = envelope.message
      }
    } catch {
      // 响应体非 JSON，使用兜底文案
    }
    throw new ApiError(message, code)
  }
  let data = null
  try {
    data = await resp.json()
  } catch {
    return null // 空响应或非 JSON 返回 null
  }
  if (data && typeof data === 'object' && 'data' in data) return data.data
  return data
}

function qs(params) {
  if (!params) return ''
  const filtered = Object.fromEntries(
    Object.entries(params).filter(([, v]) => v !== undefined && v !== null && v !== '')
  )
  const s = new URLSearchParams(filtered).toString()
  return s ? `?${s}` : ''
}

// ---- 配对 ----
export const getPairing = () => request('GET', '/api/pairing')

// ---- 概览统计（契约补充：GET /api/stats） ----
export const getStats = () => request('GET', '/api/stats')

// ---- 全局任务启停 ----
export const pauseTasks = () => request('POST', `/api/tasks/${encodeURIComponent('pause')}`)
export const resumeTasks = () => request('POST', `/api/tasks/${encodeURIComponent('resume')}`)

// ---- 草稿（审核队列） ----
export const listDrafts = (params) => request('GET', `/api/drafts${qs(params)}`)
export const updateDraft = (id, payload) =>
  request('PUT', `/api/drafts/${encodeURIComponent(id)}`, payload)

// ---- 投递任务与投递记录 ----
export const createDeliverTask = (payload) => request('POST', '/api/tasks/deliver', payload)
export const listJobs = (params) => request('GET', `/api/jobs${qs(params)}`)

// ---- 会话与消息 ----
export const listConversations = (params) => request('GET', `/api/conversations${qs(params)}`)
export const listMessages = (convId) =>
  request('GET', `/api/conversations/${encodeURIComponent(convId)}/messages`)

// ---- 规则库 ----
export const listRules = (params) => request('GET', `/api/rules${qs(params)}`)
export const createRule = (payload) => request('POST', '/api/rules', payload)
export const deleteRule = (id) => request('DELETE', `/api/rules/${encodeURIComponent(id)}`)

// ---- 设置与简历上下文 ----
export const getSettings = () => request('GET', '/api/settings')
export const putSettings = (payload) => request('PUT', '/api/settings', payload)
export const getResumeContext = () => request('GET', '/api/resume-context')
export const putResumeContext = (payload) => request('PUT', '/api/resume-context', payload)

// ---- WebSocket 事件总线（GET /api/ws 升级） ----
// 自动重连约 2s；返回 { disconnect } 由调用方在卸载时清理。
export function connectEvents(onEvent) {
  let ws = null
  let closed = false
  let retryTimer = null

  const open = () => {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    ws = new WebSocket(`${proto}://${location.host}/api/ws`)
    ws.onopen = () => onEvent('$connected', {})
    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data)
        if (msg && msg.event) onEvent(msg.event, msg.payload || {})
      } catch {
        // 忽略无法解析的帧
      }
    }
    ws.onclose = () => {
      if (!closed) {
        retryTimer = setTimeout(open, 2000)
        onEvent('$disconnected', {})
      }
    }
    ws.onerror = () => {
      try {
        ws.close()
      } catch {
        // 忽略
      }
    }
  }
  open()

  return {
    disconnect() {
      closed = true
      if (retryTimer) clearTimeout(retryTimer)
      if (ws) {
        ws.onclose = null
        try {
          ws.close()
        } catch {
          // 忽略
        }
      }
    },
  }
}

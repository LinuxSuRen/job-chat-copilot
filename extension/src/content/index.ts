/**
 * content-script（ISOLATED world）入口：
 *  - 接收 background 转发的服务端指令（deliver.next / chat.send / chat.poll /
 *    guard.pause / config.sync），串行执行 DOM 操作
 *  - 监听 MAIN_WORLD 注入脚本的 postMessage（哨兵过滤），拦截法聊天消息上行
 *  - 周期性异常巡检（验证码/登录态）
 */

import type {
  RuntimeMessage,
  PageAnomalyPayload,
  ConfigSyncPayload,
  DeliverNextPayload,
  ChatSendPayload,
  JobsScanPayload,
} from '../shared/protocol';
import { filterBridgeEvent } from '../shared/sentinel';
import { executeDeliverNext, executeChatSend, executeChatPoll, executeJobsScan, type Report } from './actions';
import { pauseAll, applyConfigSync, loadGuard, isPaused } from './state';
import { precheckAnomalies } from './anomaly';
import { looksLikeChatFrame, extractChatMessage } from './bridge';

const report: Report = (type, payload) => {
  switch (type) {
    case 'anomaly':
      void chrome.runtime.sendMessage({
        kind: 'to_server',
        event: 'page.anomaly',
        payload,
      } satisfies RuntimeMessage).catch(() => {});
      break;
    case 'deliver_result':
      void chrome.runtime.sendMessage({
        kind: 'to_server',
        event: 'deliver.result',
        payload,
      } satisfies RuntimeMessage).catch(() => {});
      break;
    case 'jobs_list':
      void chrome.runtime.sendMessage({
        kind: 'to_server',
        event: 'jobs.list',
        payload,
      } satisfies RuntimeMessage).catch(() => {});
      break;
    case 'chat_message':
      void chrome.runtime.sendMessage({
        kind: 'to_server',
        event: 'chat.message',
        payload,
      } satisfies RuntimeMessage).catch(() => {});
      break;
    case 'chat_snapshot':
      void chrome.runtime.sendMessage({
        kind: 'to_server',
        event: 'chat.snapshot',
        payload,
      } satisfies RuntimeMessage).catch(() => {});
      break;
  }
};

// 指令串行队列：同一时刻只执行一个页面动作（绝不并发）
let executing = false;
const queue: Array<() => Promise<void>> = [];

function enqueue(task: () => Promise<void>): void {
  queue.push(task);
  void drain();
}

async function drain(): Promise<void> {
  if (executing) {
    return;
  }
  executing = true;
  try {
    while (queue.length > 0) {
      const task = queue.shift();
      if (task) {
        await task();
      }
    }
  } finally {
    executing = false;
  }
}

async function handleServerDirective(event: string, payload: unknown): Promise<void> {
  switch (event) {
    case 'guard.pause': {
      const p = (payload ?? {}) as { reason_code?: string };
      // 立即置暂停标志；已在执行的动作在每步操作前检查 isPaused
      await pauseAll(p.reason_code ?? 'unknown');
      break;
    }
    case 'config.sync':
      await applyConfigSync((payload ?? {}) as Partial<ConfigSyncPayload>);
      break;
    case 'jobs.scan': {
      if (isPaused()) {
        break;
      }
      await executeJobsScan((payload ?? {}) as JobsScanPayload, report);
      break;
    }
    case 'deliver.next':
      if (isPaused()) {
        report('deliver_result', {
          job_key: (payload as { job_key?: string })?.job_key ?? '',
          ok: false,
          fail_code: 'paused',
        });
        break;
      }
      // 暂停检查前置：收到指令与执行之间可能刚被暂停
      report('deliver_result', await executeDeliverNext((payload ?? {}) as DeliverNextPayload, report));
      break;
    case 'chat.send': {
      if (isPaused()) {
        break;
      }
      const ok = await executeChatSend((payload ?? {}) as ChatSendPayload, report);
      void ok;
      break;
    }
    case 'chat.poll':
      if (!isPaused()) {
        executeChatPoll(report);
      }
      break;
    default:
      break;
  }
}

chrome.runtime.onMessage.addListener((msg: RuntimeMessage) => {
  if (msg.kind === 'from_server') {
    enqueue(() => handleServerDirective(msg.event, msg.payload));
  }
});

// ---------------------------------------------------------------------------
// 注入脚本桥：哨兵过滤 + 聊天帧结构化 → chat.message 上行
// ---------------------------------------------------------------------------

window.addEventListener('message', (ev: MessageEvent) => {
  const bridge = filterBridgeEvent(ev);
  if (!bridge) {
    return;
  }
  const data = bridge.data;
  if (!looksLikeChatFrame(bridge.url, data)) {
    return;
  }
  const message = extractChatMessage(data);
  if (message) {
    report('chat_message', message);
  }
});

// ---------------------------------------------------------------------------
// 周期异常巡检：验证码/登录态丢失主动发现（不依赖操作触发）
// ---------------------------------------------------------------------------

const PATROL_INTERVAL_MS = 10_000;

function patrol(): void {
  const anomaly: PageAnomalyPayload | null = precheckAnomalies(document);
  if (anomaly) {
    report('anomaly', anomaly);
  }
}

function startPatrol(): void {
  setInterval(() => {
    if (!isPaused()) {
      patrol();
    } else {
      // 暂停期间仍巡检验证码是否消失（消失后由服务端 resume，本地不自动恢复）
      patrol();
    }
  }, PATROL_INTERVAL_MS);
}

void loadGuard().then(() => {
  startPatrol();
});

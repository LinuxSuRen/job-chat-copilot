<script setup>
import { onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { WarningFilled } from '@element-plus/icons-vue'
import { connectEvents } from './api'
import { store, busEmit, anomalyText } from './store'
import OverviewView from './views/OverviewView.vue'
import DraftsView from './views/DraftsView.vue'
import TasksView from './views/TasksView.vue'
import ConversationsView from './views/ConversationsView.vue'
import RulesView from './views/RulesView.vue'
import SettingsView from './views/SettingsView.vue'

const views = [
  { key: 'overview', label: '概览', comp: OverviewView },
  { key: 'drafts', label: '审核队列', comp: DraftsView },
  { key: 'tasks', label: '投递任务', comp: TasksView },
  { key: 'conversations', label: '会话记录', comp: ConversationsView },
  { key: 'rules', label: '规则库', comp: RulesView },
  { key: 'settings', label: '设置', comp: SettingsView },
]

let conn = null

function handleEvent(event, payload) {
  switch (event) {
    case '$connected':
      store.wsConnected = true
      break
    case '$disconnected':
      store.wsConnected = false
      break
    case 'hello':
      store.pluginConnected = true
      store.pluginVersion = payload.ext_version || ''
      break
    case 'plugin.connected':
      store.pluginConnected = true
      break
    case 'plugin.disconnected':
      store.pluginConnected = false
      break
    case 'draft.ready':
      store.pendingDrafts += 1
      busEmit('draft.ready', payload)
      ElMessage({ message: '新草稿已就绪，等待审核', type: 'info' })
      break
    case 'chat.message':
      busEmit('chat.message', payload)
      break
    case 'deliver.result':
      busEmit('deliver.result', payload)
      break
    case 'page.anomaly': {
      const type = payload.type || 'unknown'
      store.alerts.push({ type, text: anomalyText(type) })
      busEmit('page.anomaly', payload)
      break
    }
    default:
      break
  }
}

function dismissAlert(index) {
  store.alerts.splice(index, 1)
}

function confirmAlert(index) {
  const alert = store.alerts[index]
  ElMessageBox.confirm(alert.text, '异常告警', {
    confirmButtonText: '我已知晓',
    cancelButtonText: '稍后处理',
    type: 'warning',
  })
    .then(() => dismissAlert(index))
    .catch(() => {})
}

onMounted(() => {
  conn = connectEvents(handleEvent)
})

onUnmounted(() => {
  if (conn) conn.disconnect()
})
</script>

<template>
  <div v-for="(alert, i) in store.alerts" :key="alert.type + i" class="alert-banner" role="alert">
    <span><el-icon style="vertical-align: -2px; margin-right: 6px"><WarningFilled /></el-icon>{{ alert.text }}</span>
    <el-button size="small" type="danger" plain @click="confirmAlert(i)">确认处理</el-button>
  </div>

  <div class="page">
    <header class="app-header">
      <div>
        <div class="app-header__brand">job-chat-copilot 控制台</div>
        <div class="app-header__sub dim">
          本地服务
          <el-tag :type="store.wsConnected ? 'success' : 'danger'" size="small" style="margin-left: 6px">
            {{ store.wsConnected ? '已连接' : '连接中断' }}
          </el-tag>
          <el-tag
            :type="store.pluginConnected ? 'success' : 'info'"
            size="small"
            style="margin-left: 6px"
          >
            {{ store.pluginConnected ? '插件在线' : '插件离线' }}
          </el-tag>
        </div>
      </div>
    </header>

    <el-tabs v-model="store.view" class="app-tabs">
      <el-tab-pane v-for="v in views" :key="v.key" :name="v.key">
        <template #label>
          <span>{{ v.label }}</span>
          <el-badge
            v-if="v.key === 'drafts' && store.pendingDrafts > 0"
            :value="store.pendingDrafts"
            style="margin-left: 6px"
          />
        </template>
      </el-tab-pane>
    </el-tabs>

    <component :is="views.find((v) => v.key === store.view)?.comp" :key="store.view" />
  </div>
</template>

<style scoped>
.app-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: var(--space-2) 0;
}
.app-header__brand {
  font-size: 18px;
  font-weight: 600;
  color: var(--color-text-bright);
}
.app-header__sub {
  font-size: 12px;
  margin-top: var(--space-1);
}
</style>

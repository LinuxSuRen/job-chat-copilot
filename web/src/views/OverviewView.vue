<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { getPairing, getStats, pauseTasks, resumeTasks } from '../api'
import { store, busOn } from '../store'

const ACK_KEY = 'jcc-disclaimer-ack'
const loading = ref(false)
const stats = ref(null)
const pairing = ref(null)
const pausing = ref(false)
const guideVisible = ref(false)
const acknowledged = ref(false)

const guard = computed(() => (stats.value && stats.value.guard) || null)
const paused = computed(() => !!(guard.value && guard.value.paused))

async function load() {
  loading.value = true
  try {
    const [s, p] = await Promise.all([getStats(), getPairing()])
    stats.value = s || null
    pairing.value = p || null
    const paired = !!(pairing.value && pairing.value.paired)
    store.pluginConnected = store.pluginConnected || !!s?.plugin_connected
    // 无配对信息且未确认过免责声明 → 首次使用引导
    if (!paired && !localStorage.getItem(ACK_KEY)) {
      acknowledged.value = false
      guideVisible.value = true
    }
  } catch (err) {
    ElMessage.error(err.message || '概览数据加载失败')
  } finally {
    loading.value = false
  }
}

async function togglePause() {
  pausing.value = true
  try {
    if (paused.value) {
      await resumeTasks()
      ElMessage.success('已恢复任务')
    } else {
      await pauseTasks()
      ElMessage.warning('已全局暂停任务')
    }
    await load()
  } catch (err) {
    ElMessage.error(err.message || '操作失败')
  } finally {
    pausing.value = false
  }
}

function confirmGuide() {
  if (!acknowledged.value) {
    ElMessage.warning('请先勾选已阅读并同意免责声明')
    return
  }
  localStorage.setItem(ACK_KEY, '1')
  guideVisible.value = false
}

let offBus = null
let timer = null

onMounted(() => {
  load()
  // 兜底：WS 断连时低频刷新统计（约 30s），正常时靠事件驱动
  timer = setInterval(load, 30000)
  offBus = busOn((event) => {
    if (event === 'deliver.result' || event === 'chat.message' || event === 'page.anomaly') {
      load()
    }
  })
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
  if (offBus) offBus()
})
</script>

<template>
  <div v-loading="loading">
    <div class="block">
      <h3 class="block__title">运行状态</h3>
      <el-row :gutter="16">
        <el-col :span="6">
          <el-statistic title="今日投递" :value="stats?.today_delivered ?? 0" />
        </el-col>
        <el-col :span="6">
          <el-statistic title="今日回复" :value="stats?.today_replied ?? 0" />
        </el-col>
        <el-col :span="6">
          <div class="stat-label dim">护栏状态</div>
          <el-tag :type="paused ? 'danger' : 'success'" size="large">
            {{ paused ? '已暂停' : '运行中' }}
          </el-tag>
          <div v-if="guard?.reason_code" class="dim" style="margin-top: 6px">
            原因：{{ guard.reason_code }}
          </div>
        </el-col>
        <el-col :span="6">
          <div class="stat-label dim">插件连接</div>
          <el-tag :type="store.pluginConnected ? 'success' : 'info'" size="large">
            {{ store.pluginConnected ? '在线' : '离线' }}
          </el-tag>
        </el-col>
      </el-row>
      <div style="margin-top: 16px">
        <el-button
          :type="paused ? 'success' : 'danger'"
          :loading="pausing"
          :disabled="!stats"
          @click="togglePause"
        >
          {{ paused ? '恢复任务' : '全局暂停' }}
        </el-button>
        <span v-if="guard" class="dim" style="margin-left: 12px">
          每日上限：投递 {{ guard.daily_limit_deliver ?? '—' }} / 回复 {{ guard.daily_limit_reply ?? '—' }}
        </span>
      </div>
    </div>

    <div class="block">
      <h3 class="block__title">插件配对</h3>
      <template v-if="pairing && pairing.paired">
        <el-alert type="success" :closable="false" title="插件已配对" />
      </template>
      <template v-else-if="pairing && pairing.pairing_code">
        <p class="dim">在浏览器插件选项页中输入以下配对码完成绑定：</p>
        <div class="pairing-code mono">{{ pairing.pairing_code }}</div>
      </template>
      <template v-else>
        <el-alert type="warning" :closable="false" title="暂无配对信息，请重启本地服务生成配对码" />
      </template>
    </div>

    <el-dialog v-model="guideVisible" title="首次使用引导" width="560px" :close-on-click-modal="false">
      <el-steps direction="vertical" :active="pairing && pairing.paired ? 3 : 1">
        <el-step title="启动本地服务" description="运行 job-chat-copilot 服务（默认监听 127.0.0.1:7422）" />
        <el-step title="安装浏览器插件" description="在 Chrome 中以开发者模式加载 extension 构建产物" />
        <el-step title="输入配对码" description="在插件选项页粘贴控制台展示的配对码完成绑定" />
      </el-steps>
      <el-alert
        type="warning"
        :closable="false"
        style="margin-top: 16px"
        title="免责声明"
        description="自动化操作可能违反平台用户协议，存在限流、功能限制甚至封号的风险，使用者自担风险。请保持有人值守使用，建议小流量试用并观察账号状态。"
      />
      <el-checkbox v-model="acknowledged" style="margin-top: 12px">
        我已阅读并理解上述风险，自愿使用本工具
      </el-checkbox>
      <template #footer>
        <el-button type="primary" @click="confirmGuide">开始使用</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.stat-label {
  font-size: 12px;
  margin-bottom: 8px;
}
.pairing-code {
  font-size: 24px;
  letter-spacing: 4px;
  background: var(--color-accent-bg);
  padding: var(--space-3) var(--space-6);
  border-radius: 6px;
  display: inline-block;
}
</style>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { listConversations, listMessages } from '../api'
import { busOn } from '../store'

const PAGE_SIZE = 10
const loading = ref(false)
const conversations = ref([])
const page = ref(1)
const total = ref(0)
const expanded = ref(null)
const messages = ref([])
const messagesLoading = ref(false)

async function load(p = page.value) {
  loading.value = true
  try {
    const resp = await listConversations({ page: p, page_size: PAGE_SIZE })
    conversations.value = Array.isArray(resp?.data) ? resp.data : []
    page.value = resp?.page || p
    total.value = resp?.total || 0
  } catch (err) {
    ElMessage.error(err.message || '会话列表加载失败')
  } finally {
    loading.value = false
  }
}

async function toggleExpand(row) {
  if (expanded.value && expanded.value.id === row.id) {
    expanded.value = null
    return
  }
  expanded.value = row
  messagesLoading.value = true
  messages.value = []
  try {
    const msgs = await listMessages(row.id)
    messages.value = Array.isArray(msgs) ? msgs : Array.isArray(msgs?.data) ? msgs.data : []
  } catch (err) {
    ElMessage.error(err.message || '消息加载失败')
  } finally {
    messagesLoading.value = false
  }
}

const directionText = { in: '对方', out: '我' }

let offBus = null

onMounted(() => {
  load()
  offBus = busOn((event) => {
    if (event === 'chat.message') load(page.value)
  })
})

onUnmounted(() => {
  if (offBus) offBus()
})
</script>

<template>
  <div class="block" style="margin-top: 0">
    <h3 class="block__title">会话列表（{{ total }}）</h3>
    <el-table v-loading="loading" :data="conversations" row-key="id">
      <el-table-column type="expand">
        <template #default>
          <div v-if="expanded" v-loading="messagesLoading" class="msg-timeline">
            <div
              v-for="m in messages"
              :key="m.id"
              class="msg"
              :class="m.direction === 'out' ? 'msg--out' : ''"
            >
              <div class="dim" style="font-size: 12px">
                {{ directionText[m.direction] || m.direction }} · {{ m.sent_at }}
              </div>
              <div class="msg__body">{{ m.content }}</div>
            </div>
            <el-empty v-if="!messagesLoading && !messages.length" description="暂无消息" :image-size="60" />
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="id" label="会话" width="80" />
      <el-table-column prop="hr_name" label="联系人" min-width="120" />
      <el-table-column prop="job_id" label="关联职位" width="100" />
      <el-table-column prop="last_msg_at" label="最近消息时间" min-width="180" />
    </el-table>
    <el-pagination
      layout="prev, pager, next, total"
      :page-size="PAGE_SIZE"
      :total="total"
      :current-page="page"
      style="margin-top: 12px"
      @current-change="load"
    />
  </div>
</template>

<style scoped>
.msg-timeline {
  padding: var(--space-2) var(--space-4);
  max-height: 400px;
  overflow-y: auto;
}
.msg {
  margin-bottom: var(--space-3);
}
.msg--out {
  text-align: right;
}
.msg__body {
  display: inline-block;
  background: var(--color-bg-base);
  border-radius: 6px;
  padding: var(--space-2) var(--space-3);
  margin-top: var(--space-1);
  text-align: left;
  white-space: pre-wrap;
}
</style>

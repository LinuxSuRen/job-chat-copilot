<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { listDrafts, updateDraft, listMessages } from '../api'
import { store, busOn } from '../store'

const PAGE_SIZE = 10
const loading = ref(false)
const drafts = ref([])
const page = ref(1)
const total = ref(0)
const selected = ref(null)
const editContent = ref('')
const contextMessages = ref([])
const contextLoading = ref(false)
const acting = ref(false)

const sourceText = { rule: '规则库', llm: '大模型', human: '人工' }
const sourceTagType = { rule: 'success', llm: 'warning', human: 'info' }

async function load(p = page.value) {
  loading.value = true
  try {
    const resp = await listDrafts({ status: 'pending', page: p, page_size: PAGE_SIZE })
    drafts.value = Array.isArray(resp?.data) ? resp.data : []
    page.value = resp?.page || p
    total.value = resp?.total || 0
    // 更新全局角标（用 total 近似）
    store.pendingDrafts = total.value
    if (!selected.value && drafts.value.length) select(drafts.value[0])
  } catch (err) {
    ElMessage.error(err.message || '审核队列加载失败')
  } finally {
    loading.value = false
  }
}

async function loadContext(convId) {
  contextLoading.value = true
  contextMessages.value = []
  try {
    const msgs = await listMessages(convId)
    // 只读最近 10 条作为上下文展示；接口返回 {data,...} 由 api.js 解包
    const list = Array.isArray(msgs) ? msgs : Array.isArray(msgs?.data) ? msgs.data : []
    contextMessages.value = list.slice(-10)
  } catch {
    // 上下文加载失败不阻塞审核
  } finally {
    contextLoading.value = false
  }
}

function select(draft) {
  selected.value = draft
  editContent.value = draft.content || ''
  if (draft.conv_id) loadContext(draft.conv_id)
}

async function review(action) {
  if (!selected.value) return
  acting.value = true
  try {
    const payload = action === 'approve' ? { action, content: editContent.value } : { action }
    await updateDraft(selected.value.id, payload)
    ElMessage.success(action === 'approve' ? '草稿已确认发送' : '草稿已驳回')
    await load()
    if (!drafts.value.length) {
      selected.value = null
      editContent.value = ''
      contextMessages.value = []
    }
  } catch (err) {
    ElMessage.error(err.message || '审核操作失败')
  } finally {
    acting.value = false
  }
}

const directionText = { in: '对方', out: '我' }

let offBus = null

onMounted(() => {
  load()
  offBus = busOn((event) => {
    if (event === 'draft.ready') load(page.value)
  })
})

onUnmounted(() => {
  if (offBus) offBus()
})
</script>

<template>
  <div v-loading="loading">
    <el-empty v-if="!drafts.length" description="暂无待审核草稿" />
    <el-row v-else :gutter="16">
      <el-col :span="8">
        <div class="block" style="margin-top: 0">
          <h3 class="block__title">待审核（{{ total }}）</h3>
          <div
            v-for="d in drafts"
            :key="d.id"
            class="draft-item"
            :class="{ 'draft-item--active': selected && selected.id === d.id }"
            @click="select(d)"
          >
            <div class="draft-item__head">
              <span>会话 #{{ d.conv_id }}</span>
              <el-tag :type="sourceTagType[d.source] || 'info'" size="small">
                {{ sourceText[d.source] || d.source }}
              </el-tag>
            </div>
            <div class="draft-item__content dim">{{ (d.content || '').slice(0, 60) }}</div>
            <div class="dim" style="font-size: 12px">{{ d.created_at }}</div>
          </div>
          <el-pagination
            layout="prev, pager, next"
            :page-size="PAGE_SIZE"
            :total="total"
            :current-page="page"
            style="margin-top: 12px"
            @current-change="load"
          />
        </div>
      </el-col>
      <el-col :span="16">
        <div class="block" style="margin-top: 0">
          <template v-if="selected">
            <h3 class="block__title">会话上下文（最近消息）</h3>
            <div v-loading="contextLoading" class="timeline">
              <div
                v-for="m in contextMessages"
                :key="m.id"
                class="ctx-msg"
                :class="m.direction === 'out' ? 'ctx-msg--out' : ''"
              >
                <span class="dim" style="font-size: 12px">{{ directionText[m.direction] || m.direction }} · {{ m.sent_at }}</span>
                <div class="ctx-msg__body">{{ m.content }}</div>
              </div>
              <el-empty v-if="!contextLoading && !contextMessages.length" description="暂无历史消息" :image-size="60" />
            </div>
            <h3 class="block__title" style="margin-top: 16px">
              草稿内容
              <el-tag :type="sourceTagType[selected.source] || 'info'" size="small" style="margin-left: 8px">
                来源：{{ sourceText[selected.source] || selected.source }}
              </el-tag>
            </h3>
            <el-input v-model="editContent" type="textarea" :rows="5" maxlength="200" show-word-limit />
            <div style="margin-top: 12px">
              <el-button type="primary" :loading="acting" @click="review('approve')">确认发送</el-button>
              <el-button type="danger" plain :loading="acting" @click="review('reject')">驳回</el-button>
            </div>
          </template>
          <el-empty v-else description="请选择左侧草稿" />
        </div>
      </el-col>
    </el-row>
  </div>
</template>

<style scoped>
.draft-item {
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: 6px;
  margin-bottom: var(--space-2);
  cursor: pointer;
}
.draft-item--active {
  border-color: var(--color-accent);
  background: var(--color-accent-bg);
}
.draft-item__head {
  display: flex;
  justify-content: space-between;
  margin-bottom: var(--space-1);
}
.draft-item__content {
  font-size: 13px;
  margin-bottom: var(--space-1);
}
.timeline {
  max-height: 280px;
  overflow-y: auto;
}
.ctx-msg {
  margin-bottom: var(--space-3);
}
.ctx-msg--out {
  text-align: right;
}
.ctx-msg__body {
  display: inline-block;
  background: var(--color-bg-base);
  border-radius: 6px;
  padding: var(--space-2) var(--space-3);
  margin-top: var(--space-1);
  text-align: left;
  white-space: pre-wrap;
}
</style>

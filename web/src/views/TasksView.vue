<script setup>
import { ref, reactive, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { createDeliverTask, listJobs, getStats, pauseTasks, resumeTasks } from '../api'
import { busOn } from '../store'

const PAGE_SIZE = 10
const formRef = ref(null)
const creating = ref(false)
const form = reactive({
  keyword: '',
  exclude_keywords: '',
  salary_min: null,
  salary_max: null,
  daily_limit: 50,
})
const rules = {
  keyword: [{ required: true, message: '请输入搜索关键词', trigger: 'blur' }],
  daily_limit: [{ required: true, message: '请输入每日投递上限', trigger: 'blur' }],
}

const stats = ref(null)
const jobsLoading = ref(false)
const jobs = ref([])
const page = ref(1)
const total = ref(0)
const statusFilter = ref('')
const toggling = ref(false)

const jobStatusText = {
  pending: '待处理',
  delivering: '投递中',
  delivered: '已投递',
  skipped: '已跳过',
  failed: '失败',
}
const jobStatusTag = {
  delivered: 'success',
  delivering: 'warning',
  failed: 'danger',
  skipped: 'info',
  pending: 'info',
}

async function loadStats() {
  try {
    stats.value = (await getStats()) || null
  } catch {
    stats.value = null
  }
}

async function loadJobs(p = page.value) {
  jobsLoading.value = true
  try {
    const resp = await listJobs({ status: statusFilter.value, page: p, page_size: PAGE_SIZE })
    jobs.value = Array.isArray(resp?.data) ? resp.data : []
    page.value = resp?.page || p
    total.value = resp?.total || 0
  } catch (err) {
    ElMessage.error(err.message || '投递记录加载失败')
  } finally {
    jobsLoading.value = false
  }
}

async function createTask() {
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  creating.value = true
  try {
    const filters = {
      exclude_keywords: form.exclude_keywords
        .split(/[,，\n]/)
        .map((s) => s.trim())
        .filter(Boolean),
    }
    if (form.salary_min !== null && form.salary_min !== undefined) filters.salary_min = form.salary_min
    if (form.salary_max !== null && form.salary_max !== undefined) filters.salary_max = form.salary_max
    await createDeliverTask({ keyword: form.keyword, filters, daily_limit: form.daily_limit })
    ElMessage.success('投递任务已创建')
    form.keyword = ''
    form.exclude_keywords = ''
    form.salary_min = null
    form.salary_max = null
    await Promise.all([loadStats(), loadJobs(1)])
  } catch (err) {
    ElMessage.error(err.message || '任务创建失败')
  } finally {
    creating.value = false
  }
}

async function togglePause() {
  toggling.value = true
  try {
    if (stats.value?.guard?.paused) {
      await resumeTasks()
      ElMessage.success('已恢复任务')
    } else {
      await pauseTasks()
      ElMessage.warning('已暂停任务')
    }
    await loadStats()
  } catch (err) {
    ElMessage.error(err.message || '操作失败')
  } finally {
    toggling.value = false
  }
}

let offBus = null
let timer = null

onMounted(() => {
  Promise.all([loadStats(), loadJobs(1)])
  offBus = busOn((event) => {
    if (event === 'deliver.result') {
      loadJobs(page.value)
      loadStats()
    }
  })
  timer = setInterval(() => {
    loadStats()
  }, 30000)
})

onUnmounted(() => {
  if (offBus) offBus()
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="block" style="margin-top: 0">
    <h3 class="block__title">创建投递任务</h3>
    <el-form ref="formRef" :model="form" :rules="rules" label-width="120px" style="max-width: 640px">
      <el-form-item label="搜索关键词" prop="keyword">
        <el-input v-model="form.keyword" placeholder="如：Golang 开发" />
      </el-form-item>
      <el-form-item label="黑名单关键词">
        <el-input
          v-model="form.exclude_keywords"
          type="textarea"
          :rows="2"
          placeholder="多个关键词用逗号或换行分隔，命中即跳过"
        />
      </el-form-item>
      <el-form-item label="薪资范围（K）">
        <el-col :span="10">
          <el-input-number v-model="form.salary_min" :min="0" :max="200" placeholder="最低" style="width: 100%" />
        </el-col>
        <el-col :span="2" class="dim" style="text-align: center">至</el-col>
        <el-col :span="10">
          <el-input-number v-model="form.salary_max" :min="0" :max="200" placeholder="最高" style="width: 100%" />
        </el-col>
      </el-form-item>
      <el-form-item label="每日投递上限" prop="daily_limit">
        <el-input-number v-model="form.daily_limit" :min="1" :max="50" />
        <span class="dim" style="margin-left: 8px">护栏上限 50，只能调低</span>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="creating" @click="createTask">创建任务</el-button>
        <el-button
          :type="stats?.guard?.paused ? 'success' : 'danger'"
          :loading="toggling"
          @click="togglePause"
        >
          {{ stats?.guard?.paused ? '恢复任务' : '暂停任务' }}
        </el-button>
        <span v-if="stats" class="dim" style="margin-left: 12px">
          今日已投递 {{ stats.today_delivered ?? 0 }} 条
        </span>
      </el-form-item>
    </el-form>
  </div>

  <div class="block">
    <h3 class="block__title">投递记录</h3>
    <div style="margin-bottom: 12px">
      <el-select v-model="statusFilter" clearable placeholder="全部状态" style="width: 160px" @change="loadJobs(1)">
        <el-option v-for="(text, k) in jobStatusText" :key="k" :label="text" :value="k" />
      </el-select>
    </div>
    <el-table v-loading="jobsLoading" :data="jobs" size="default">
      <el-table-column prop="title" label="职位" min-width="200" show-overflow-tooltip />
      <el-table-column prop="company" label="公司" min-width="140" show-overflow-tooltip />
      <el-table-column prop="salary" label="薪资" width="120" />
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="jobStatusTag[row.status] || 'info'" size="small">
            {{ jobStatusText[row.status] || row.status }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="delivered_at" label="投递时间" width="180" />
    </el-table>
    <el-pagination
      layout="prev, pager, next, total"
      :page-size="PAGE_SIZE"
      :total="total"
      :current-page="page"
      style="margin-top: 12px"
      @current-change="loadJobs"
    />
  </div>
</template>

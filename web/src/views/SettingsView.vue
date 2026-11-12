<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { getSettings, putSettings, getResumeContext, putResumeContext } from '../api'

const loading = ref(false)
const saving = ref(false)
const resumeSaving = ref(false)
const activeTab = ref('llm')

const LLM_PRESETS = {
  deepseek: { label: 'DeepSeek', base_url: 'https://api.deepseek.com/v1', model: 'deepseek-chat' },
  glm: { label: 'GLM', base_url: 'https://open.bigmodel.cn/api/paas/v4', model: 'glm-4-flash' },
  qwen: { label: 'Qwen', base_url: 'https://dashscope.aliyuncs.com/compatible-mode/v1', model: 'qwen-plus' },
  custom: { label: '自定义', base_url: '', model: '' },
}

const DEFAULT_SETTINGS = {
  llm: { provider: 'custom', base_url: '', api_key: '', model: '' },
  guard: {
    min_interval_seconds: 40,
    daily_limit_deliver: 50,
    daily_limit_reply: 200,
    active_start: '09:00',
    active_end: '20:00',
  },
  greeting: { templates: [], strategy: 'order' },
}

const form = reactive(JSON.parse(JSON.stringify(DEFAULT_SETTINGS)))
const resumeContext = ref('')
const newTemplate = ref('')

function deepMerge(target, src) {
  for (const k of Object.keys(src || {})) {
    if (src[k] && typeof src[k] === 'object' && !Array.isArray(src[k])) {
      target[k] = { ...(target[k] || {}), ...src[k] }
    } else {
      target[k] = src[k]
    }
  }
}

async function load() {
  loading.value = true
  try {
    const [s, r] = await Promise.all([getSettings(), getResumeContext()])
    if (s) {
      deepMerge(form, s)
      form.llm = { ...DEFAULT_SETTINGS.llm, ...(s.llm || {}) }
      form.greeting = { ...DEFAULT_SETTINGS.greeting, ...(s.greeting || {}) }
      if (!Array.isArray(form.greeting.templates)) form.greeting.templates = []
    }
    // resume-context 返回 {content} 或纯文本，均容错
    resumeContext.value = typeof r === 'string' ? r : r?.content || ''
  } catch (err) {
    ElMessage.error(err.message || '设置加载失败')
  } finally {
    loading.value = false
  }
}

function applyPreset(presetKey) {
  const preset = LLM_PRESETS[presetKey]
  if (preset && presetKey !== 'custom') {
    form.llm.base_url = preset.base_url
    form.llm.model = preset.model
  }
}

async function saveSettings() {
  saving.value = true
  try {
    await putSettings(JSON.parse(JSON.stringify(form)))
    ElMessage.success('设置已保存')
  } catch (err) {
    ElMessage.error(err.message || '设置保存失败')
  } finally {
    saving.value = false
  }
}

async function saveResume() {
  resumeSaving.value = true
  try {
    await putResumeContext({ content: resumeContext.value })
    ElMessage.success('简历上下文已保存')
  } catch (err) {
    ElMessage.error(err.message || '简历上下文保存失败')
  } finally {
    resumeSaving.value = false
  }
}

function addTemplate() {
  const t = newTemplate.value.trim()
  if (!t) return
  form.greeting.templates.push(t)
  newTemplate.value = ''
}

function removeTemplate(i) {
  form.greeting.templates.splice(i, 1)
}

onMounted(load)
</script>

<template>
  <div v-loading="loading">
    <el-tabs v-model="activeTab" type="border-card">
      <el-tab-pane label="LLM 配置" name="llm">
        <el-form label-width="140px" style="max-width: 620px">
          <el-form-item label="供应商预设">
            <el-select v-model="form.llm.provider" style="width: 200px" @change="applyPreset">
              <el-option v-for="(p, k) in LLM_PRESETS" :key="k" :label="p.label" :value="k" />
            </el-select>
          </el-form-item>
          <el-form-item label="Base URL">
            <el-input v-model="form.llm.base_url" placeholder="https://api.example.com/v1" class="mono" />
          </el-form-item>
          <el-form-item label="API Key">
            <el-input v-model="form.llm.api_key" type="password" show-password placeholder="sk-..." />
          </el-form-item>
          <el-form-item label="模型">
            <el-input v-model="form.llm.model" placeholder="模型名称" class="mono" />
          </el-form-item>
        </el-form>
      </el-tab-pane>

      <el-tab-pane label="护栏参数" name="guard">
        <el-form label-width="160px" style="max-width: 620px">
          <el-form-item label="投递最小间隔（秒）">
            <el-input-number v-model="form.guard.min_interval_seconds" :min="40" />
            <span class="dim" style="margin-left: 8px">不可低于 40</span>
          </el-form-item>
          <el-form-item label="每日投递上限">
            <el-input-number v-model="form.guard.daily_limit_deliver" :min="1" :max="50" />
            <span class="dim" style="margin-left: 8px">上限 50，只能调低</span>
          </el-form-item>
          <el-form-item label="每日回复上限">
            <el-input-number v-model="form.guard.daily_limit_reply" :min="1" :max="200" />
          </el-form-item>
          <el-form-item label="活跃时段">
            <el-time-select v-model="form.guard.active_start" start="00:00" step="01:00" end="23:00" style="width: 120px" />
            <span class="dim" style="margin: 0 8px">至</span>
            <el-time-select v-model="form.guard.active_end" start="00:00" step="01:00" end="23:59" style="width: 120px" />
          </el-form-item>
        </el-form>
      </el-tab-pane>

      <el-tab-pane label="招呼语模板" name="greeting">
        <el-form label-width="160px" style="max-width: 620px">
          <el-form-item label="轮换策略">
            <el-radio-group v-model="form.greeting.strategy">
              <el-radio value="order">顺序轮换</el-radio>
              <el-radio value="random">随机轮换</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="新增模板">
            <el-input v-model="newTemplate" placeholder="您好，看到这个职位和我的经历很匹配……" style="width: 420px" />
            <el-button style="margin-left: 8px" @click="addTemplate">添加</el-button>
          </el-form-item>
          <el-form-item label="模板列表">
            <div style="width: 100%">
              <div v-for="(t, i) in form.greeting.templates" :key="i" class="tpl-item">
                <span class="dim" style="margin-right: 8px">{{ i + 1 }}.</span>
                <span style="flex: 1">{{ t }}</span>
                <el-button type="danger" plain size="small" @click="removeTemplate(i)">删除</el-button>
              </div>
              <el-empty v-if="!form.greeting.templates.length" description="暂无模板" :image-size="60" />
            </div>
          </el-form-item>
        </el-form>
      </el-tab-pane>
    </el-tabs>

    <div style="margin-top: 16px">
      <el-button type="primary" :loading="saving" @click="saveSettings">保存设置</el-button>
    </div>

    <div class="block">
      <h3 class="block__title">简历上下文</h3>
      <el-input
        v-model="resumeContext"
        type="textarea"
        :rows="10"
        placeholder="结构化简历摘要：技能、经验年限、期望方向、到岗时间等。LLM 回答只基于此处事实。"
      />
      <div style="margin-top: 12px">
        <el-button type="primary" :loading="resumeSaving" @click="saveResume">保存简历上下文</el-button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.tpl-item {
  display: flex;
  align-items: center;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: 6px;
  margin-bottom: var(--space-2);
}
</style>

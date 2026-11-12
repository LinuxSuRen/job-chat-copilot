<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { listRules, createRule, deleteRule } from '../api'

const loading = ref(false)
const rules = ref([])
const formRef = ref(null)
const creating = ref(false)
const form = reactive({ pattern: '', answer: '' })
const rules2 = {
  pattern: [
    { required: true, message: '请输入正则表达式', trigger: 'blur' },
    {
      validator: (_, v, cb) => {
        try {
          new RegExp(v)
          cb()
        } catch {
          cb(new Error('正则表达式语法无效'))
        }
      },
      trigger: 'blur',
    },
  ],
  answer: [{ required: true, message: '请输入预设答案', trigger: 'blur' }],
}

async function load() {
  loading.value = true
  try {
    const resp = await listRules()
    rules.value = Array.isArray(resp) ? resp : Array.isArray(resp?.data) ? resp.data : []
  } catch (err) {
    ElMessage.error(err.message || '规则库加载失败')
  } finally {
    loading.value = false
  }
}

async function add() {
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  creating.value = true
  try {
    await createRule({ pattern: form.pattern, answer: form.answer })
    ElMessage.success('规则已添加')
    form.pattern = ''
    form.answer = ''
    formRef.value.resetFields()
    await load()
  } catch (err) {
    ElMessage.error(err.message || '规则添加失败')
  } finally {
    creating.value = false
  }
}

async function remove(rule) {
  try {
    await ElMessageBox.confirm(`确定删除规则「${rule.pattern}」吗？`, '删除确认', { type: 'warning' })
  } catch {
    return
  }
  try {
    await deleteRule(rule.id)
    ElMessage.success('规则已删除')
    await load()
  } catch (err) {
    ElMessage.error(err.message || '规则删除失败')
  }
}
</script>

<template>
  <div class="block" style="margin-top: 0">
    <h3 class="block__title">新增规则</h3>
    <el-form ref="formRef" :model="form" :rules="rules2" inline>
      <el-form-item label="正则表达式" prop="pattern">
        <el-input v-model="form.pattern" placeholder="如：到岗时间|什么时候入职" style="width: 280px" class="mono" />
      </el-form-item>
      <el-form-item label="预设答案" prop="answer">
        <el-input v-model="form.answer" placeholder="命中后直接使用的回复内容" style="width: 320px" />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="creating" @click="add">添加</el-button>
      </el-form-item>
    </el-form>
  </div>

  <div class="block">
    <h3 class="block__title">规则列表（{{ rules.length }}）</h3>
    <el-table v-loading="loading" :data="rules">
      <el-table-column prop="id" label="ID" width="80" />
      <el-table-column prop="pattern" label="正则表达式" min-width="240">
        <template #default="{ row }">
          <code class="mono">{{ row.pattern }}</code>
        </template>
      </el-table-column>
      <el-table-column prop="answer" label="预设答案" min-width="260" show-overflow-tooltip />
      <el-table-column label="操作" width="100">
        <template #default="{ row }">
          <el-button type="danger" plain size="small" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

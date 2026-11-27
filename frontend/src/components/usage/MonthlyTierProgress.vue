<template>
  <div v-if="items.length || error" class="space-y-3">
    <div v-if="error" class="card p-4 text-sm" role="alert">
      月度优惠进度暂时无法加载。<button class="btn btn-secondary ml-2" @click="load">重试</button>
    </div>
    <section v-for="item in items" :key="item.group_id" class="card space-y-2 p-4">
      <div class="flex items-center justify-between">
        <h3 class="font-medium">{{ item.group_name }} · 本月阶梯优惠</h3>
        <button class="btn btn-secondary" @click="load">刷新</button>
      </div>
      <p>原价累计 {{ format(item.raw_cost) }} / {{ format(item.policy.threshold) }}，剩余优惠额度 {{ format(item.remaining) }}</p>
      <p>当前倍率 {{ item.current_rate }}；超过门槛部分 {{ item.policy.excess_rate }} 倍。本月实际扣款 {{ format(item.charged_cost) }}。</p>
      <p class="text-sm text-gray-500">同一用户所有 Key 合并统计。按北京时间自然月重置，余额保留。下次重置：{{ resetTime(item.resets_at) }}</p>
    </section>
  </div>
</template>
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { apiClient } from '@/api/client'
import type { MonthlyTierPolicy } from '@/types'
interface Progress { group_id: number; group_name: string; raw_cost: number; charged_cost: number; remaining: number; current_rate: number; policy: MonthlyTierPolicy; resets_at: string }
const items = ref<Progress[]>([])
const error = ref(false)
const format = (n: number) => n.toLocaleString('zh-CN', { maximumFractionDigits: 8 })
const resetTime = (value: string) => new Date(value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false })
async function load() {
  try { const { data } = await apiClient.get<Progress[]>('/usage/monthly-tiers'); items.value = data; error.value = false }
  catch { error.value = true }
}
onMounted(load)
</script>

<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
    <label class="flex items-center gap-2 font-medium">
      <input type="checkbox" :checked="modelValue.enabled" @change="update('enabled', ($event.target as HTMLInputElement).checked)" />
      每用户月度阶梯计费
    </label>
    <p class="text-sm text-gray-500">
      按北京时间自然月累计本分组的原价消费，同一用户的所有 API Key 合并统计。
      启用后替代固定分组及用户专属倍率，不重复乘折扣；跨档请求分段扣费，历史账单不追补。
    </p>
    <div v-if="modelValue.enabled" class="grid gap-3 sm:grid-cols-3">
      <label class="space-y-1 text-sm">第一档原价额度
        <input class="input w-full" type="number" min="0.00000001" max="1000000000" step="any" :value="modelValue.threshold" required @input="number('threshold', $event)" />
      </label>
      <label class="space-y-1 text-sm">第一档倍率
        <input class="input w-full" type="number" min="0.0001" max="100" step="0.01" :value="modelValue.first_rate" required @input="number('first_rate', $event)" />
      </label>
      <label class="space-y-1 text-sm">超出部分倍率
        <input class="input w-full" type="number" min="0.0001" max="100" step="0.01" :value="modelValue.excess_rate" required @input="number('excess_rate', $event)" />
      </label>
    </div>
    <p v-if="modelValue.enabled" class="text-sm text-gray-500">
      前 {{ modelValue.threshold }} 个原价额度按 {{ modelValue.first_rate }} 倍，超出部分按 {{ modelValue.excess_rate }} 倍。
      每月重新计算优惠额度，余额不清零。首次启用从保存后开始累计。
    </p>
  </section>
</template>

<script setup lang="ts">
import type { MonthlyTierPolicy } from '@/types'
const props = defineProps<{ modelValue: MonthlyTierPolicy }>()
const emit = defineEmits<{ (e: 'update:modelValue', value: MonthlyTierPolicy): void }>()
function update(key: keyof MonthlyTierPolicy, value: boolean | number) {
  emit('update:modelValue', { ...props.modelValue, [key]: value })
}
function number(key: 'threshold' | 'first_rate' | 'excess_rate', event: Event) {
  update(key, Number((event.target as HTMLInputElement).value))
}
</script>

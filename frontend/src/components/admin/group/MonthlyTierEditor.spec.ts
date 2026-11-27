import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import MonthlyTierEditor from './MonthlyTierEditor.vue'

describe('MonthlyTierEditor', () => {
  it('enables and edits a rule without losing the activation date', async () => {
    const policy = { enabled: false, threshold: 500, first_rate: 0.7, excess_rate: 1.2, starts_at: '2026-11-27T00:00:00Z' }
    const wrapper = mount(MonthlyTierEditor, { props: { modelValue: policy } })
    expect(wrapper.findAll('input[type=number]')).toHaveLength(0)
    await wrapper.get('input[type=checkbox]').setValue(true)
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toEqual({ ...policy, enabled: true })
    await wrapper.setProps({ modelValue: { ...policy, enabled: true } })
    await wrapper.findAll('input[type=number]')[0].setValue('600')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual({ ...policy, enabled: true, threshold: 600 })
    expect(wrapper.text()).toContain('同一用户的所有 API Key 合并统计')
  })
})

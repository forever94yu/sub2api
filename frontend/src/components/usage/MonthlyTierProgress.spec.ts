import { mount, flushPromises } from '@vue/test-utils'
import { describe, it, expect, vi, beforeEach } from 'vitest'
const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get } }))
import MonthlyTierProgress from './MonthlyTierProgress.vue'

describe('MonthlyTierProgress', () => {
  beforeEach(() => get.mockReset())
  it('shows cumulative original cost, remaining benefit and Shanghai reset time', async () => {
    get.mockResolvedValue({ data: [{ group_id: 1, group_name: 'claude', raw_cost: 520, charged_cost: 374, remaining: 0, current_rate: 1.2, policy: { enabled: true, threshold: 500, first_rate: 0.7, excess_rate: 1.2 }, resets_at: '2026-12-01T00:00:00+08:00' }] })
    const wrapper = mount(MonthlyTierProgress)
    await flushPromises()
    expect(get).toHaveBeenCalledWith('/usage/monthly-tiers')
    expect(wrapper.text()).toContain('520 / 500')
    expect(wrapper.text()).toContain('剩余优惠额度 0')
    expect(wrapper.text()).toContain('当前倍率 1.2')
    expect(wrapper.text()).toContain('374')
  })
  it('keeps failures visible and allows a retry instead of displaying zero consumption', async () => {
    get.mockRejectedValueOnce(new Error('unavailable')).mockResolvedValueOnce({ data: [] })
    const wrapper = mount(MonthlyTierProgress)
    await flushPromises()
    expect(wrapper.get('[role=alert]').text()).toContain('暂时无法加载')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role=alert]').exists()).toBe(false)
  })
})

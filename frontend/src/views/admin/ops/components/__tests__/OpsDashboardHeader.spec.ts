import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import OpsDashboardHeader from '../OpsDashboardHeader.vue'
import Select from '@/components/common/Select.vue'

vi.mock('@/api', () => ({ adminAPI: { groups: { getAll: vi.fn().mockResolvedValue([]) } } }))
vi.mock('@/stores', () => ({ useAdminSettingsStore: () => ({ opsRealtimeMonitoringEnabled: false }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllEnvs()
})

describe('OpsDashboardHeader custom time range', () => {
  it('round trips the most recent hour in a non-UTC timezone', async () => {
    vi.stubEnv('TZ', 'Asia/Shanghai')
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-10-07T04:00:00.000Z'))
    const wrapper = shallowMount(OpsDashboardHeader, {
      props: { platform: '', groupId: null, timeRange: '1h', queryMode: 'auto', loading: false, lastUpdated: null },
      global: { stubs: {
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
      } },
    })
    await flushPromises()
    wrapper.findAllComponents(Select)[2].vm.$emit('update:modelValue', 'custom')
    await flushPromises()
    const inputs = wrapper.findAll<HTMLInputElement>('input[type="datetime-local"]')
    expect(inputs[0].element.value).toBe('2026-10-07T11:00')
    expect(inputs[1].element.value).toBe('2026-10-07T12:00')
    const confirm = wrapper.findAll('button').find(button => button.text() === 'common.confirm')!
    await confirm.trigger('click')
    expect(wrapper.emitted('update:customTimeRange')).toEqual([
      ['2026-10-07T03:00:00.000Z', '2026-10-07T04:00:00.000Z'],
    ])
    wrapper.unmount()
  })
})

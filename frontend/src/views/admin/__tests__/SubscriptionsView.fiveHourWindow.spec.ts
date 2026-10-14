import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import type { Group, UserSubscription } from '@/types'
import SubscriptionsView from '../SubscriptionsView.vue'

const { listSubscriptions, getAllGroups, resetQuota, showError, showSuccess } = vi.hoisted(() => ({
  listSubscriptions: vi.fn(),
  getAllGroups: vi.fn(),
  resetQuota: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    subscriptions: {
      list: listSubscriptions,
      resetQuota,
      assign: vi.fn(),
      extend: vi.fn(),
      revoke: vi.fn(),
      restore: vi.fn(),
    },
    groups: {
      getAll: getAllGroups,
    },
    usage: {
      searchUsers: vi.fn().mockResolvedValue([]),
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

const group = {
  id: 3,
  name: '5h group',
  platform: 'anthropic',
  subscription_type: 'subscription',
  rate_multiplier: 1,
  five_hour_limit_usd: 1,
  daily_limit_usd: 50,
  weekly_limit_usd: null,
  monthly_limit_usd: null,
} as unknown as Group

const subscription = (overrides: Partial<UserSubscription> = {}): UserSubscription =>
  ({
    id: 9,
    user_id: 1,
    group_id: 3,
    status: 'active',
    starts_at: new Date(Date.now() - 86400000).toISOString(),
    expires_at: new Date(Date.now() + 30 * 86400000).toISOString(),
    five_hour_usage_usd: 0.86,
    five_hour_window_start: new Date(Date.now() - 2 * 3600000).toISOString(),
    daily_usage_usd: 1.2,
    weekly_usage_usd: 1.2,
    monthly_usage_usd: 1.2,
    daily_window_start: new Date().toISOString(),
    weekly_window_start: null,
    monthly_window_start: null,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    user: { id: 1, email: 'user@example.com' },
    group,
    ...overrides,
  }) as UserSubscription

const DataTableStub = {
  props: ['columns', 'data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" data-test="row">
        <div data-test="usage-cell"><slot name="cell-usage" :row="row" /></div>
        <div data-test="actions-cell"><slot name="cell-actions" :row="row" /></div>
      </div>
    </div>
  `,
}

const ConfirmDialogStub = {
  props: ['show', 'title'],
  emits: ['confirm', 'cancel'],
  template: `<div v-if="show" :data-test="'confirm-' + title"><button data-test="confirm" @click="$emit('confirm')">ok</button></div>`,
}

const mountView = async () => {
  const wrapper = mount(SubscriptionsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
        },
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: true,
        ConfirmDialog: ConfirmDialogStub,
        EmptyState: true,
        Select: true,
        GroupBadge: true,
        GroupOptionItem: true,
        Icon: true,
        teleport: true,
      },
    },
  })
  await flushPromises()
  return wrapper
}

describe('admin SubscriptionsView 5-hour window', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    getAllGroups.mockResolvedValue([group])
    resetQuota.mockResolvedValue(subscription())
  })

  afterEach(() => {
    localStorage.clear()
  })

  it('shows 5-hour usage above the daily row with time until reset', async () => {
    listSubscriptions.mockResolvedValue({ items: [subscription()], total: 1, page: 1, page_size: 20, pages: 1 })

    const wrapper = await mountView()
    const usage = wrapper.get('[data-test="usage-cell"]').text().replace(/\s+/g, ' ')

    expect(usage).toContain('admin.subscriptions.fiveHour')
    expect(usage).toContain('$0.86 / $1.00')
    expect(usage.indexOf('admin.subscriptions.fiveHour')).toBeLessThan(usage.indexOf('admin.subscriptions.daily'))
    expect(usage).not.toContain('admin.subscriptions.unlimited')
  })

  it('reports an inactive 5-hour window before the first request', async () => {
    listSubscriptions.mockResolvedValue({
      items: [subscription({ five_hour_usage_usd: 0, five_hour_window_start: null })],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const wrapper = await mountView()
    const usage = wrapper.get('[data-test="usage-cell"]').text().replace(/\s+/g, ' ')

    expect(usage).toContain('$0.00 / $1.00')
    expect(usage).toContain('admin.subscriptions.windowNotActive')
  })

  it('resets the 5-hour window together with the other quotas', async () => {
    listSubscriptions.mockResolvedValue({ items: [subscription()], total: 1, page: 1, page_size: 20, pages: 1 })

    const wrapper = await mountView()
    const resetButton = wrapper
      .get('[data-test="actions-cell"]')
      .findAll('button')
      .find((button) => button.text().includes('admin.subscriptions.resetQuota'))
    expect(resetButton).toBeTruthy()
    await resetButton!.trigger('click')
    await wrapper.get('[data-test="confirm-admin.subscriptions.resetQuotaTitle"] [data-test="confirm"]').trigger('click')
    await flushPromises()

    expect(resetQuota).toHaveBeenCalledWith(9, { five_hour: true, daily: true, weekly: true, monthly: true })
  })
})

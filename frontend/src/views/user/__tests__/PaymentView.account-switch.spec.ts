import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import PaymentView from '@/views/user/PaymentView.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import { useAuthStore } from '@/stores/auth'
import { decidePaymentLaunch, writePaymentRecoverySnapshot, PAYMENT_RECOVERY_STORAGE_KEY } from '@/components/payment/paymentFlow'

const authAPI = vi.hoisted(() => ({ login: vi.fn(), logout: vi.fn() }))
const getCheckoutInfo = vi.hoisted(() => vi.fn())
vi.mock('@/api', () => ({ authAPI, isTotp2FARequired: () => false }))
vi.mock('@/api/payment', () => ({ paymentAPI: { getCheckoutInfo } }))
vi.mock('vue-router', async () => ({
  ...await vi.importActual('vue-router'),
  useRoute: () => ({ path: '/purchase', query: {} }),
  useRouter: () => ({ replace: vi.fn(), push: vi.fn(), resolve: vi.fn() }),
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('@/stores/subscriptions', () => ({ useSubscriptionStore: () => ({
  activeSubscriptions: [], fetchActiveSubscriptions: vi.fn().mockResolvedValue([]),
}) }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: vi.fn(), showWarning: vi.fn(), showInfo: vi.fn() }) }))

async function login(id: number) {
  const store = useAuthStore()
  authAPI.login.mockResolvedValueOnce({ user: { id, username: `user-${id}`, balance: 10 }, access_token: `access-${id}`, refresh_token: `refresh-${id}`, expires_in: 3600 })
  await store.login({ email: `user-${id}@example.com`, password: 'example-password' })
}

function saveOrder() {
  const decision = decidePaymentLaunch({
    order_id: 101, amount: 100, pay_amount: 100, fee_rate: 0, qr_code: 'https://pay.example.com/alice-order-101',
    expires_at: '2099-01-01T00:00:00Z', payment_type: 'alipay',
    out_trade_no: 'alice-order-101', payment_mode: 'qrcode', resume_token: 'alice-resume-token',
  }, { visibleMethod: 'alipay', orderType: 'balance', isMobile: false })
  writePaymentRecoverySnapshot(localStorage, decision.recovery)
}

function mountView() {
  return shallowMount(PaymentView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Teleport: true } } })
}

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
  vi.useFakeTimers()
  authAPI.logout.mockResolvedValue(undefined)
  getCheckoutInfo.mockResolvedValue({ data: {
    methods: { alipay: { available: true, fee_rate: 0, single_min: 0, single_max: 0 } },
    plans: [], global_min: 0, global_max: 0, balance_disabled: false, balance_recharge_multiplier: 1,
  } })
})
afterEach(() => { vi.clearAllTimers(); vi.useRealTimers() })

it('does not restore another account payment after logout and login', async () => {
  await login(1)
  saveOrder()
  await useAuthStore().logout()
  expect(localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  await login(2)
  const wrapper = mountView()
  await flushPromises()
  expect(wrapper.findComponent(PaymentStatusPanel).exists()).toBe(false)
  wrapper.unmount()
})

it('drops an already rendered QR when the active account changes without remounting', async () => {
  await login(1)
  saveOrder()
  const wrapper = mountView()
  await flushPromises()
  expect(wrapper.findComponent(PaymentStatusPanel).props('orderId')).toBe(101)
  await login(2)
  await flushPromises()
  expect(wrapper.findComponent(PaymentStatusPanel).exists()).toBe(false)
  wrapper.unmount()
})

it('drops the rendered QR when another tab replaces the account', async () => {
  await login(1)
  saveOrder()
  const wrapper = mountView()
  await flushPromises()
  localStorage.setItem('auth_user', JSON.stringify({ id: 2 }))
  window.dispatchEvent(new StorageEvent('storage', { key: 'auth_user' }))
  await flushPromises()
  expect(wrapper.findComponent(PaymentStatusPanel).exists()).toBe(false)
  wrapper.unmount()
})

it('keeps the rendered QR when another tab refreshes the same profile', async () => {
  await login(1)
  saveOrder()
  const wrapper = mountView()
  await flushPromises()
  localStorage.setItem('auth_user', JSON.stringify({ id: 1, balance: 99 }))
  window.dispatchEvent(new StorageEvent('storage', { key: 'auth_user' }))
  await flushPromises()
  expect(wrapper.findComponent(PaymentStatusPanel).exists()).toBe(true)
  wrapper.unmount()
})

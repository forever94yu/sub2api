import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AdminComplianceDialog from '../AdminComplianceDialog.vue'

const mocks = vi.hoisted(() => ({
  accept: vi.fn(),
  fetchAdminSettings: vi.fn(),
  fetchVersion: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/stores', () => ({
  useAdminComplianceStore: () => ({
    accept: mocks.accept,
    expectedPhrase: 'expected confirmation phrase',
    shouldShow: true,
    status: {
      required: true,
      version: 'v2026.06.10',
    },
    submitting: false,
  }),
  useAdminSettingsStore: () => ({
    fetch: mocks.fetchAdminSettings,
  }),
  useAppStore: () => ({
    fetchVersion: mocks.fetchVersion,
    showError: mocks.showError,
    showSuccess: mocks.showSuccess,
  }),
  useAuthStore: () => ({
    isAdmin: true,
    isAuthenticated: true,
    logout: vi.fn(),
  }),
}))

vi.mock('@/i18n', () => ({
  getLocale: () => 'zh',
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

describe('AdminComplianceDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.accept.mockResolvedValue({ required: false })
    mocks.fetchAdminSettings.mockResolvedValue(undefined)
    mocks.fetchVersion.mockResolvedValue(null)
  })

  it('refreshes protected admin state after the acknowledgement is accepted', async () => {
    const wrapper = mount(AdminComplianceDialog, {
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>',
          },
          Icon: true,
          Input: {
            props: ['modelValue'],
            emits: ['update:modelValue'],
            template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
          },
        },
      },
    })

    await wrapper.get('input').setValue('expected confirmation phrase')
    await wrapper.findAll('button').at(-1)!.trigger('click')
    await flushPromises()

    expect(mocks.accept).toHaveBeenCalledWith('expected confirmation phrase')
    expect(mocks.fetchAdminSettings).toHaveBeenCalledWith(true)
    expect(mocks.fetchVersion).toHaveBeenCalledWith(true)
    expect(mocks.showSuccess).toHaveBeenCalledWith('adminCompliance.accepted')
  })
})

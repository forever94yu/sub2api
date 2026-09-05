import { beforeEach, describe, expect, it } from 'vitest'
import { decidePaymentLaunch, readPaymentRecoverySnapshot, writePaymentRecoverySnapshot, PAYMENT_RECOVERY_STORAGE_KEY } from '../paymentFlow'

function snapshot() {
  return decidePaymentLaunch({
    order_id: 101, amount: 100, pay_amount: 100, fee_rate: 0,
    qr_code: 'https://pay.example.com/alice-order-101', expires_at: '2099-01-01T00:00:00Z',
    payment_type: 'alipay', resume_token: 'alice-resume',
  }, { visibleMethod: 'alipay', orderType: 'balance', isMobile: false }).recovery
}

beforeEach(() => localStorage.clear())

describe('payment recovery ownership', () => {
  it('records the signed-in account when persisting an order', () => {
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    writePaymentRecoverySnapshot(localStorage, snapshot())
    expect(JSON.parse(localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)!).userId).toBe(1)
  })

  it('automatically restores only the account that owns the order', () => {
    const raw = JSON.stringify({ ...snapshot(), userId: 1 })
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    expect(readPaymentRecoverySnapshot(raw)?.orderId).toBe(101)
    localStorage.setItem('auth_user', JSON.stringify({ id: 2 }))
    expect(readPaymentRecoverySnapshot(raw)).toBeNull()
    expect(readPaymentRecoverySnapshot(raw, { resumeToken: 'alice-resume' })).toBeNull()
  })

  it('requires an explicit matching resume token for an anonymous callback', () => {
    const raw = JSON.stringify({ ...snapshot(), userId: 1 })
    expect(readPaymentRecoverySnapshot(raw)).toBeNull()
    expect(readPaymentRecoverySnapshot(raw, { resumeToken: 'wrong' })).toBeNull()
    expect(readPaymentRecoverySnapshot(raw, { resumeToken: 'alice-resume' })?.orderId).toBe(101)
  })

  it('does not automatically adopt legacy snapshots without an owner', () => {
    const raw = JSON.stringify(snapshot())
    expect(readPaymentRecoverySnapshot(raw)).toBeNull()
    localStorage.setItem('auth_user', JSON.stringify({ id: 2 }))
    expect(readPaymentRecoverySnapshot(raw)).toBeNull()
    localStorage.removeItem('auth_user')
    expect(readPaymentRecoverySnapshot(raw, { resumeToken: 'alice-resume' })?.orderId).toBe(101)
  })
})

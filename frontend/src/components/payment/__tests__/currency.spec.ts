import { describe, expect, it } from 'vitest'
import { currencySymbol, formatPaymentAmount, paymentAmountFractionDigits } from '../currency'

describe('paymentAmountFractionDigits', () => {
  it.each([
    { currency: 'CNY', digits: 2 },
    { currency: 'USD', digits: 2 },
    { currency: 'AFN', digits: 2 },
    { currency: 'ALL', digits: 2 },
    { currency: 'IQD', digits: 3 },
    { currency: ' kwd ', digits: 3 },
    { currency: 'BHD', digits: 3 },
    { currency: 'JPY', digits: 0 },
    { currency: 'MGA', digits: 0 },
    { currency: 'ISK', digits: 0 },
    { currency: 'UGX', digits: 0 },
    { currency: 'XYZ', digits: 2 },
    { currency: 'invalid', digits: 2 },
    { currency: undefined, digits: 2 },
  ])('uses $digits payment fraction digits for $currency', ({ currency, digits }) => {
    expect(paymentAmountFractionDigits(currency)).toBe(digits)
  })
})

describe('formatPaymentAmount', () => {
  it.each([
    { currency: 'AFN', fee: 0.11, total: 10.61 },
    { currency: 'ALL', fee: 0.11, total: 10.61 },
    { currency: 'IQD', fee: 0.105, total: 10.605 },
  ])('preserves payment precision when displaying $currency amounts', ({ currency, fee, total }) => {
    expect(formatPaymentAmount(fee, currency, 'en-US')).toContain(String(fee))
    expect(formatPaymentAmount(total, currency, 'en-US')).toContain(String(total))
  })

  it('uses the currency default fraction digits', () => {
    expect(formatPaymentAmount(100, 'JPY', 'en-US')).not.toContain('.00')
    expect(formatPaymentAmount(100, 'KRW', 'en-US')).not.toContain('.00')
    expect(formatPaymentAmount(100, 'HKD', 'en-US')).toContain('.00')
  })
})

describe('currencySymbol', () => {
  it('maps common payment currencies and falls back safely', () => {
    expect(currencySymbol('USD')).toBe('$')
    expect(currencySymbol('cny')).toBe('¥')
    expect(currencySymbol('EUR')).toBe('€')
    expect(currencySymbol('')).toBe('¥')
    expect(currencySymbol('XYZ')).toBe('XYZ')
  })
})

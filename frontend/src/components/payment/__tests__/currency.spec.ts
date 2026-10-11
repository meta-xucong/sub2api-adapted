import { describe, expect, it } from 'vitest'
import { currencySymbol, formatPaymentAmount } from '../currency'

describe('formatPaymentAmount', () => {
  it('uses the currency default fraction digits', () => {
    expect(formatPaymentAmount(100, 'JPY', 'en-US')).not.toContain('.00')
    expect(formatPaymentAmount(100, 'KRW', 'en-US')).not.toContain('.00')
    expect(formatPaymentAmount(100, 'HKD', 'en-US')).toContain('.00')
  })

  it('labels external payment amounts with an ISO code instead of a currency glyph', () => {
    const usd = formatPaymentAmount(100, 'USD', 'en-US')
    expect(usd).toContain('USD')
    expect(usd).toContain('100.00')
    expect(usd).not.toContain('$')
  })
})

describe('currencySymbol', () => {
  it('returns ISO currency codes and falls back safely', () => {
    expect(currencySymbol('USD')).toBe('USD')
    expect(currencySymbol('cny')).toBe('CNY')
    expect(currencySymbol('EUR')).toBe('EUR')
    expect(currencySymbol('')).toBe('CNY')
    expect(currencySymbol('XYZ')).toBe('XYZ')
  })
})

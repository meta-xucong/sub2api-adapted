import { describe, expect, it } from 'vitest'
import { formatOfficialUsdScaled, formatScaled } from '../pricing'

describe('pricing amount display units', () => {
  it('formats local prices as stars without changing the value', () => {
    expect(formatScaled(0.000003, 1_000_000, 2)).toBe('⭐️3.00')
  })

  it('labels official reference prices as USD', () => {
    expect(formatOfficialUsdScaled(0.000003, 1_000_000, 2)).toBe('USD 3.00')
  })

  it('keeps missing prices blank in both display contexts', () => {
    expect(formatScaled(null, 1_000_000)).toBe('-')
    expect(formatOfficialUsdScaled(null, 1_000_000)).toBe('-')
  })
})

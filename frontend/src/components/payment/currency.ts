export const DEFAULT_PAYMENT_CURRENCY = 'CNY'
export const LOCAL_POINTS_SYMBOL = '⭐️'

export function normalizePaymentCurrency(currency?: string | null): string {
  const normalized = String(currency || '').trim().toUpperCase()
  return /^[A-Z]{3}$/.test(normalized) ? normalized : DEFAULT_PAYMENT_CURRENCY
}

export function currencySymbol(currency?: string | null): string {
  return normalizePaymentCurrency(currency)
}

function paymentCurrencyFractionDigits(currency: string): number {
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency,
    }).resolvedOptions().maximumFractionDigits ?? 2
  } catch {
    return 2
  }
}

export function formatPaymentAmount(amount: number, currency?: string | null, locale?: string): string {
  const normalized = normalizePaymentCurrency(currency)
  const fractionDigits = paymentCurrencyFractionDigits(normalized)
  try {
    return new Intl.NumberFormat(locale || undefined, {
      style: 'currency',
      currency: normalized,
      currencyDisplay: 'code',
      minimumFractionDigits: fractionDigits,
      maximumFractionDigits: fractionDigits,
    }).format(Number.isFinite(amount) ? amount : 0)
  } catch {
    return `${normalized} ${(Number.isFinite(amount) ? amount : 0).toFixed(fractionDigits)}`
  }
}

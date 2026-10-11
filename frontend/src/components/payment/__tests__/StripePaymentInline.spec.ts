import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const resolve = vi.hoisted(() => vi.fn(() => ({ href: '/payment/stripe-popup' })))
const loadStripe = vi.hoisted(() => vi.fn())
const paymentElement = vi.hoisted(() => ({
  mount: vi.fn(),
  on: vi.fn((event: string, callback: (value?: unknown) => void) => {
    if (event === 'ready') callback()
    if (event === 'change') callback({ value: { type: 'alipay' } })
  }),
}))
const stripe = vi.hoisted(() => ({
  elements: vi.fn(() => ({ create: vi.fn(() => paymentElement) })),
  confirmPayment: vi.fn(),
}))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return { ...actual, useRouter: () => ({ resolve }) }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

vi.mock('@stripe/stripe-js/pure', () => ({ loadStripe }))
vi.mock('@/api/payment', () => ({ paymentAPI: { cancelOrder: vi.fn() } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('@/components/payment/providerConfig', () => ({ getPaymentPopupFeatures: () => '' }))

import StripePaymentInline from '../StripePaymentInline.vue'

describe('StripePaymentInline popup currency handoff', () => {
  beforeEach(() => {
    resolve.mockClear().mockReturnValue({ href: '/payment/stripe-popup' })
    loadStripe.mockReset().mockResolvedValue(stripe)
    paymentElement.mount.mockReset()
    paymentElement.on.mockClear()
    stripe.elements.mockClear()
    stripe.confirmPayment.mockReset()
  })

  it.each(['USD', 'CNY'])('passes the %s payment currency to the popup route', async (currency) => {
    const popup = { postMessage: vi.fn() }
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    const wrapper = mount(StripePaymentInline, {
      props: {
        orderId: 42,
        amount: 10,
        clientSecret: 'pi_secret',
        publishableKey: 'pk_test',
        payAmount: 12.5,
        currency,
      },
    })

    await flushPromises()
    await wrapper.find('button.btn-stripe').trigger('click')

    expect(resolve).toHaveBeenCalledWith({
      path: '/payment/stripe-popup',
      query: {
        order_id: '42',
        method: 'alipay',
        amount: '12.5',
        currency,
      },
    })
    expect(openSpy).toHaveBeenCalled()

    wrapper.unmount()
    openSpy.mockRestore()
  })
})

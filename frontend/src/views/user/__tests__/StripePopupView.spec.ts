import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

const routeState = vi.hoisted(() => ({ query: {} as Record<string, unknown> }))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return { ...actual, useRoute: () => routeState }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

import StripePopupView from '../StripePopupView.vue'

describe('StripePopupView payment currency', () => {
  let wrapper: ReturnType<typeof mount> | undefined

  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
  })

  it.each(['USD', 'CNY'])('displays the payment amount with the %s ISO currency code', (currency) => {
    routeState.query = {
      order_id: '42',
      method: 'alipay',
      amount: '12.50',
      currency,
    }

    wrapper = mount(StripePopupView)

    expect(wrapper.text()).toContain(`${currency} 12.50`)
    expect(wrapper.text()).not.toContain('¥')
  })
})

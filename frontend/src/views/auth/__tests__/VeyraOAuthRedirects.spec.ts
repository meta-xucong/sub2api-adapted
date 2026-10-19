import type { Component } from 'vue'
import { createPinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import OAuthCallbackView from '../OAuthCallbackView.vue'
import LinuxDoCallbackView from '../LinuxDoCallbackView.vue'
import OidcCallbackView from '../OidcCallbackView.vue'
import WechatCallbackView from '../WechatCallbackView.vue'
import DingTalkCallbackView from '../DingTalkCallbackView.vue'
import DingTalkEmailCompletionView from '../DingTalkEmailCompletionView.vue'
import EmailVerifyView from '../EmailVerifyView.vue'

const mocks = vi.hoisted(() => ({
  route: { path: '/auth/oauth/callback', query: {} as Record<string, unknown> },
  replace: vi.fn(), push: vi.fn(), hardReplace: vi.fn(), setToken: vi.fn(),
  exchange: vi.fn(), login2FA: vi.fn(), post: vi.fn(), settings: vi.fn(),
  sendCode: vi.fn(), showError: vi.fn(), showSuccess: vi.fn()
}))

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => ({ replace: mocks.replace, push: mocks.push })
}))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }) }
})
vi.mock('@/stores', () => ({
  useAuthStore: () => ({
    setToken: mocks.setToken, pendingAuthSession: null,
    setPendingAuthSession: vi.fn(), clearPendingAuthSession: vi.fn()
  }),
  useAppStore: () => ({
    showError: mocks.showError, showSuccess: mocks.showSuccess, showInfo: vi.fn(),
    cachedPublicSettings: null, publicSettingsLoaded: true, fetchPublicSettings: mocks.settings
  })
}))
vi.mock('@/api/client', () => ({ apiClient: { post: mocks.post } }))
vi.mock('@/api/auth', async () => {
  const actual = await vi.importActual<typeof import('@/api/auth')>('@/api/auth')
  return {
    ...actual, exchangePendingOAuthCompletion: mocks.exchange,
    login2FA: mocks.login2FA, getPublicSettings: mocks.settings,
    sendVerifyCode: mocks.sendCode, sendPendingOAuthVerifyCode: mocks.sendCode
  }
})

const target = '/_veyra/return?target=video'
const originalLocation = Object.getOwnPropertyDescriptor(window, 'location')!
const wrappers: VueWrapper[] = []
const callbacks = [
  { name: 'Google/GitHub', component: OAuthCallbackView },
  { name: 'LinuxDo', component: LinuxDoCallbackView },
  { name: 'OIDC', component: OidcCallbackView },
  { name: 'WeChat', component: WechatCallbackView },
  { name: 'DingTalk', component: DingTalkCallbackView }
]
const stubs = {
  AuthLayout: { template: '<div><slot /><slot name="footer" /></div>' },
  Icon: true, RouterLink: { template: '<a><slot /></a>' }, transition: false,
  TurnstileWidget: true, PendingOAuthCreateAccountForm: true
}
function mountCallback(component: Component) {
  const wrapper = mount(component, { global: { stubs, plugins: [createPinia()] } })
  wrappers.push(wrapper)
  return wrapper
}
function expectVideoNavigation() {
  expect(mocks.setToken).toHaveBeenCalled()
  expect(mocks.hardReplace).toHaveBeenCalledTimes(1)
  expect(mocks.hardReplace).toHaveBeenCalledWith(target)
  expect(mocks.replace).not.toHaveBeenCalled()
  expect(mocks.push).not.toHaveBeenCalled()
  expect(mocks.showError).not.toHaveBeenCalled()
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  sessionStorage.clear()
  mocks.route.path = '/auth/oauth/callback'
  mocks.route.query = {}
  mocks.setToken.mockResolvedValue(undefined)
  mocks.exchange.mockResolvedValue({ access_token: 'synthetic-token', redirect: target })
  mocks.login2FA.mockResolvedValue({ access_token: 'synthetic-2fa-token' })
  mocks.post.mockResolvedValue({ data: { access_token: 'synthetic-token', redirect: target } })
  mocks.settings.mockResolvedValue({ turnstile_enabled: false, site_name: 'Test', oidc_oauth_provider_name: 'Test' })
  mocks.sendCode.mockResolvedValue({ success: true, cooldown_seconds: 60 })
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: { href: 'http://localhost/auth/oauth/callback', hash: '', search: '',
      pathname: '/auth/oauth/callback', origin: 'http://localhost', replace: mocks.hardReplace }
  })
})
afterEach(() => {
  for (const wrapper of wrappers.splice(0)) wrapper.unmount()
  Object.defineProperty(window, 'location', originalLocation)
})

describe('Veyra return after OAuth completion', () => {
  it.each(callbacks)('$name pending exchange preserves Video', async ({ component }) => {
    mountCallback(component)
    await flushPromises()
    expectVideoNavigation()
  })

  it.each(callbacks)('$name legacy fragment preserves Video', async ({ component }) => {
    window.location.hash = `#access_token=synthetic-token&redirect=${encodeURIComponent(target)}`
    mountCallback(component)
    await flushPromises()
    expectVideoNavigation()
    expect(mocks.exchange).not.toHaveBeenCalled()
  })

  it.each(callbacks)('$name ordinary destinations stay in the SPA', async ({ component }) => {
    mocks.exchange.mockResolvedValue({ access_token: 'synthetic-token', redirect: '/dashboard' })
    mountCallback(component)
    await flushPromises()
    expect(mocks.replace).toHaveBeenCalledWith('/dashboard')
    expect(mocks.hardReplace).not.toHaveBeenCalled()
  })

  it.each(callbacks.slice(1))('$name OAuth 2FA preserves Video', async ({ component }) => {
    mocks.exchange.mockResolvedValue({ requires_2fa: true, temp_token: 'synthetic-temp', redirect: target })
    const wrapper = mountCallback(component)
    await flushPromises()
    await wrapper.get('input[maxlength="6"]').setValue('123456')
    await wrapper.get('button[data-testid$="totp-submit"]').trigger('click')
    await flushPromises()
    expectVideoNavigation()
  })

  it('DingTalk email completion preserves Video', async () => {
    mocks.route.query = { redirect: target }
    const wrapper = mountCallback(DingTalkEmailCompletionView)
    wrapper.findComponent({ name: 'PendingOAuthCreateAccountForm' }).vm.$emit('submit', {
      email: 'fixture@example.test', password: 'synthetic-password', verifyCode: '123456'
    })
    await flushPromises()
    expectVideoNavigation()
  })

  it('pending OAuth email verification preserves Video', async () => {
    sessionStorage.setItem('register_data', JSON.stringify({
      email: 'fixture@example.test', password: 'synthetic-password',
      pending_auth_token: 'synthetic-pending', pending_provider: 'oidc', pending_redirect: target
    }))
    const wrapper = mountCallback(EmailVerifyView)
    await flushPromises()
    await wrapper.get('#code').setValue('123456')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()
    expectVideoNavigation()
  })
})

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AxiosError } from 'axios'
import type { InternalAxiosRequestConfig } from 'axios'

vi.mock('@/i18n', () => ({ getLocale: () => 'en' }))
const { refreshAuthTokens } = vi.hoisted(() => ({ refreshAuthTokens: vi.fn() }))
vi.mock('../tokenRefresh', () => ({ refreshAuthTokens }))

describe('web search admin diagnostics', () => {
  beforeEach(() => {
    vi.resetModules()
    refreshAuthTokens.mockReset()
    localStorage.clear()
    sessionStorage.clear()
    window.history.replaceState({}, '', '/admin/settings')
  })

  it('sends a card target while retaining the legacy no-target request shape', async () => {
    const { apiClient } = await import('../client')
    const { testWebSearchEmulation } = await import('../admin/settings')
    const adapter = vi.fn().mockImplementation(async (config) => ({
      status: 200, statusText: 'OK', headers: {}, config,
      data: { code: 0, data: { provider: 'tavily', results: [], query: 'test' } },
    }))
    apiClient.defaults.adapter = adapter
    const result = await testWebSearchEmulation('test', 'tavily')
    await testWebSearchEmulation('test')
    expect(JSON.parse(adapter.mock.calls[0][0].data)).toEqual({ query: 'test', provider_type: 'tavily' })
    expect(JSON.parse(adapter.mock.calls[1][0].data)).toEqual({ query: 'test' })
    expect(result.provider).toBe('tavily')
  })

  it.each([
    [502, 'WEB_SEARCH_AUTHENTICATION_FAILED', 'tavily: status 401 (authentication_failed)'],
    [502, 'WEB_SEARCH_RATE_LIMITED', 'tavily: status 429 (rate_limited)'],
    [504, 'WEB_SEARCH_TIMEOUT', 'tavily: timeout'],
  ])('keeps local credentials for safe diagnostic HTTP %s (%s)', async (status, reason, message) => {
    localStorage.setItem('auth_token', 'fake-local-auth-token')
    localStorage.setItem('refresh_token', 'fake-local-refresh-token')
    const { apiClient } = await import('../client')
    const { testWebSearchEmulation } = await import('../admin/settings')
    const adapter = vi.fn().mockImplementation(async (config: InternalAxiosRequestConfig) => {
      throw new AxiosError('Request failed', 'ERR_BAD_RESPONSE', config, undefined, {
        status, statusText: 'Bad Gateway', headers: {}, config,
        data: { code: status, reason, message },
      })
    })
    apiClient.defaults.adapter = adapter
    await expect(testWebSearchEmulation('test', 'tavily')).rejects.toMatchObject({ status, reason, message })
    expect(adapter).toHaveBeenCalledTimes(1)
    expect(refreshAuthTokens).not.toHaveBeenCalled()
    expect(localStorage.getItem('auth_token')).toBe('fake-local-auth-token')
    expect(localStorage.getItem('refresh_token')).toBe('fake-local-refresh-token')
    expect(window.location.pathname).toBe('/admin/settings')
  })
})

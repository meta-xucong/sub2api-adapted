import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AuthLayout from '@/components/layout/AuthLayout.vue'

const authLayoutSource = readFileSync(
  resolve(dirname(fileURLToPath(import.meta.url)), '../AuthLayout.vue'),
  'utf8'
)

const { fetchPublicSettingsMock } = vi.hoisted(() => ({
  fetchPublicSettingsMock: vi.fn()
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    siteName: 'Configured Workspace',
    siteLogo: '/configured-logo.svg',
    cachedPublicSettings: {
      site_subtitle: 'Private model workspace'
    },
    fetchPublicSettings: fetchPublicSettingsMock
  })
}))

describe('AuthLayout', () => {
  beforeEach(() => {
    fetchPublicSettingsMock.mockReset()
  })

  it('renders dynamic branding and preserves the default and footer slots', () => {
    const wrapper = mount(AuthLayout, {
      slots: {
        default: '<div data-testid="auth-content">Sign-in form</div>',
        footer: '<a data-testid="auth-footer-link" href="/register">Register</a>'
      }
    })

    expect(wrapper.get('[data-testid="auth-layout"]').text()).toContain('Configured Workspace')
    expect(wrapper.text()).toContain('Private model workspace')
    expect(wrapper.get('[data-testid="auth-content"]').text()).toBe('Sign-in form')
    expect(wrapper.get('[data-testid="auth-footer-link"]').attributes('href')).toBe('/register')
    expect(wrapper.get('img').attributes('src')).toBe('/configured-logo.svg')
    expect(wrapper.get('img').attributes('alt')).toBe('Configured Workspace logo')
    expect(fetchPublicSettingsMock).toHaveBeenCalledTimes(1)
  })

  it('keeps the Veyra-aligned visual contract in the component source', async () => {
    expect(authLayoutSource).toContain('--auth-paper: #fbfaf7')
    expect(authLayoutSource).toContain('--auth-sage: #828f79')
    expect(authLayoutSource).toContain('prefers-reduced-motion: reduce')
    expect(authLayoutSource).toContain('<slot name="footer" />')
    expect(authLayoutSource).toContain('sanitizeUrl')
  })
})

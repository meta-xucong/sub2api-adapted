import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'

// Vitest and the production Vite config resolve this to the runtime ESM build.
// Locale modules are raw strings loaded after startup, not precompiled messages.
describe('runtime locale loading after the Vue I18n security upgrade', () => {
  it('compiles dynamically loaded messages and preserves composition locale switching', () => {
    const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: {} })
    i18n.global.setLocaleMessage('en', {
      welcome: 'Hello {name}', count: 'no items | one item | {count} items', fallback: 'Fallback'
    })
    i18n.global.setLocaleMessage('zh', { welcome: '你好 {name}' })
    expect(i18n.global.t('welcome', { name: 'Alice' })).toBe('Hello Alice')
    expect(i18n.global.t('count', 2)).toBe('2 items')
    i18n.global.locale.value = 'zh'
    expect(i18n.global.t('welcome', { name: 'Alice' })).toBe('你好 Alice')
    expect(i18n.global.t('fallback')).toBe('Fallback')
  })
})

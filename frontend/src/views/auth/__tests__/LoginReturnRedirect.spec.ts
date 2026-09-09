import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const loginViewSource = readFileSync(
  resolve(dirname(fileURLToPath(import.meta.url)), '../LoginView.vue'),
  'utf8'
)

describe('LoginView Veyra return redirect', () => {
  it('uses a document navigation for backend-served Veyra return targets', () => {
    expect(loginViewSource).toContain("if (redirectTo.startsWith('/_veyra/'))")
    expect(loginViewSource).toContain('window.location.assign(redirectTo)')
    expect(loginViewSource.match(/await navigateAfterLogin\(redirectTo\)/g)).toHaveLength(3)
  })
})

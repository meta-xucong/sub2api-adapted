import { describe, expect, it, vi } from 'vitest'
import { completeLoginRedirect, resolveVeyraReturnPath } from '../veyraReturn'

describe('Veyra login return navigation', () => {
  it('normalizes the Alchemy portal callback for a full-page navigation', async () => {
    const routerPush = vi.fn().mockResolvedValue(undefined)
    const hardNavigate = vi.fn()

    await completeLoginRedirect('/_veyra/return?target=alchemy', routerPush, hardNavigate)

    expect(hardNavigate).toHaveBeenCalledWith('/_veyra/return?target=alchemy')
    expect(routerPush).not.toHaveBeenCalled()
  })

  it('keeps ordinary in-app redirects inside Vue Router', async () => {
    const routerPush = vi.fn().mockResolvedValue(undefined)
    const hardNavigate = vi.fn()

    await completeLoginRedirect('/profile', routerPush, hardNavigate)

    expect(routerPush).toHaveBeenCalledWith('/profile')
    expect(hardNavigate).not.toHaveBeenCalled()
  })

  it.each([
    '//evil.example/_veyra/return?target=alchemy',
    'https://evil.example/_veyra/return?target=alchemy',
    'https://sub2api.invalid/_veyra/return?target=alchemy',
    '/foo/../_veyra/return?target=alchemy',
    '/_veyra/return?target=unknown',
    '/_veyra/return?target=alchemy&target=home',
    '/_veyra/return?target=alchemy&extra=1',
    '/dashboard?target=alchemy',
  ])('does not treat an untrusted or malformed redirect as a Veyra callback: %s', (value) => {
    expect(resolveVeyraReturnPath(value)).toBeNull()
  })
})

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { runInNewContext } from 'node:vm'
import { describe, expect, it, vi } from 'vitest'

const mobileRedirect = readFileSync(resolve(process.cwd(), '../extensions/veyra/portal/mobile-redirect.js'), 'utf8')

function navigate(pathname: string, search = '', hash = '', userAgent = 'iPhone', compact = false) {
  const replace = vi.fn()
  runInNewContext(mobileRedirect, {
    URLSearchParams,
    navigator: { userAgent },
    window: { location: { pathname, search, hash, replace }, matchMedia: () => ({ matches: compact }) }
  })
  return replace
}

describe('Veyra mobile surface navigation', () => {
  it('keeps query and fragment while moving a mobile root visit', () => {
    expect(navigate('/', '?example=1', '#section')).toHaveBeenCalledWith('/_veyra/mobile.html?example=1#section')
  })

  it('respects the explicit desktop surface', () => {
    expect(navigate('/', '?desktop=1&example=1')).not.toHaveBeenCalled()
  })

  it('detects compact touch devices without a mobile user agent', () => {
    expect(navigate('/', '', '', 'Desktop', true)).toHaveBeenCalledWith('/_veyra/mobile.html')
    expect(navigate('/', '', '', 'Desktop', false)).not.toHaveBeenCalled()
  })

  it.each(['home', 'alchemy', 'alchemy-mobile', 'video', 'sub2api-console'])(
    'preserves the exact callback path for the %s login intent', (target) => {
      expect(navigate('/_veyra/return', `?target=${target}`, '#fragment')).not.toHaveBeenCalled()
    }
  )

  it('keeps source and embedded redirect assets byte-identical', () => {
    const embedded = readFileSync(resolve(process.cwd(), '../backend/internal/veyra/portal_dist/mobile-redirect.js'), 'utf8')
    expect(embedded).toBe(mobileRedirect)
  })
})

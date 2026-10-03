const VEYRA_RETURN_PATH = '/_veyra/return'
const ALLOWED_VEYRA_TARGETS = new Set(['home', 'alchemy', 'alchemy-mobile', 'sub2api-console'])

/**
 * Veyra return URLs are served by the backend portal middleware, not by the
 * Vue router. Accept only its same-site relative callback and known targets.
 */
export function resolveVeyraReturnPath(redirectTo: unknown): string | null {
  if (typeof redirectTo !== 'string') return null
  const match = /^\/_veyra\/return\?target=(home|alchemy|alchemy-mobile|sub2api-console)$/.exec(redirectTo)
  if (!match || !ALLOWED_VEYRA_TARGETS.has(match[1])) return null
  return `${VEYRA_RETURN_PATH}?target=${encodeURIComponent(match[1])}`
}

export async function completeLoginRedirect(
  redirectTo: unknown,
  routerPush: (path: string) => Promise<unknown>,
  hardNavigate: (path: string) => void
): Promise<void> {
  const veyraReturnPath = resolveVeyraReturnPath(redirectTo)
  if (veyraReturnPath) {
    hardNavigate(veyraReturnPath)
    return
  }

  await routerPush(typeof redirectTo === 'string' && redirectTo ? redirectTo : '/dashboard')
}

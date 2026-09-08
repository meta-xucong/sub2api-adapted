/**
 * Public gateway URL helpers used by the user-facing setup guides.
 *
 * The site setting historically stored only the origin (for example
 * https://example.com), while OpenAI-compatible clients need the versioned
 * gateway base (https://example.com/v1). Keep the stored value untouched and
 * normalize only the value shown/exported to clients.
 */

export function trimGatewayBaseUrl(value: string): string {
  return String(value || '').trim().replace(/\/+$/, '')
}

export function withGatewayVersion(
  value: string,
  version: '/v1' | '/v1beta' = '/v1',
): string {
  const base = trimGatewayBaseUrl(value)
  if (!base) return version
  if (/\/v1(?:beta)?$/i.test(base)) return base
  return `${base}${version}`
}

export function withOpenAICompatibleVersion(value: string): string {
  return withGatewayVersion(value, '/v1')
}

export function withGeminiVersion(value: string): string {
  return withGatewayVersion(value, '/v1beta')
}

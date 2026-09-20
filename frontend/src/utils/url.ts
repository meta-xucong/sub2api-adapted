/**
 * 验证并规范化 URL
 * 默认只接受绝对 URL（以 http:// 或 https:// 开头），可按需允许相对路径
 * @param value 用户输入的 URL
 * @returns 规范化后的 URL，如果无效则返回空字符串
 */
type SanitizeOptions = {
  allowRelative?: boolean
  allowDataUrl?: boolean
}

export function sanitizeUrl(value: string, options: SanitizeOptions = {}): string {
  const trimmed = value.trim()
  if (!trimmed) {
    return ''
  }

  if (options.allowRelative && trimmed.startsWith('/') && !trimmed.startsWith('//')) {
    return trimmed
  }

  // 允许 data:image/ 开头的 data URL（仅限图片类型）
  if (options.allowDataUrl && trimmed.startsWith('data:image/')) {
    return trimmed
  }

  // 只接受绝对 URL，不使用 base URL 来避免相对路径被解析为当前域名
  // 检查是否以 http:// 或 https:// 开头
  if (!trimmed.match(/^https?:\/\//i)) {
    return ''
  }

  try {
    const parsed = new URL(trimmed)
    const protocol = parsed.protocol.toLowerCase()
    if (protocol !== 'http:' && protocol !== 'https:') {
      return ''
    }
    return parsed.toString()
  } catch {
    return ''
  }
}

// OpenAI-compatible providers conventionally expose their API below /v1.
// Keep custom paths intact because relays may use a provider-specific prefix.
export function ensureOpenAIBaseUrl(value: string): string {
  const trimmed = value.trim()
  if (!trimmed) {
    return ''
  }

  if (trimmed.startsWith('/') && !trimmed.startsWith('//')) {
    return trimmed
  }

  try {
    const parsed = new URL(trimmed)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return trimmed
    }
    const pathname = parsed.pathname.replace(/\/+$/, '')
    parsed.pathname = pathname === '' ? '/v1' : pathname
    return parsed.toString()
  } catch {
    return trimmed
  }
}

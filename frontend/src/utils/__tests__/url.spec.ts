import { describe, expect, it } from 'vitest'
import { ensureOpenAIBaseUrl } from '@/utils/url'

describe('ensureOpenAIBaseUrl', () => {
  it.each([
    ['https://example.com', 'https://example.com/v1'],
    ['https://example.com/', 'https://example.com/v1'],
    ['https://example.com/v1', 'https://example.com/v1'],
    ['https://example.com/v1/', 'https://example.com/v1'],
    ['https://example.com/api/v1', 'https://example.com/api/v1'],
    ['https://example.com/api/v1/', 'https://example.com/api/v1'],
    ['https://example.com/custom', 'https://example.com/custom'],
    ['/custom', '/custom'],
    ['', ''],
  ])('resolves %s to %s', (input, expected) => {
    expect(ensureOpenAIBaseUrl(input)).toBe(expected)
  })
})

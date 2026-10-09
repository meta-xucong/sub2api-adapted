import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put, post } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, put, post },
}))

import {
  getUnifiedGatewayRoutePricing,
  manualizeUnifiedGatewayWokeyPrice,
  syncUnifiedGatewayWokeyPrices,
  updateUnifiedGatewayRoutePricing,
} from '@/api/admin/unifiedGatewayRoutePricing'

const endpoint = '/admin/settings/unified-gateway-route-pricing'

const state = {
  saved: {
    target_group_id: 7,
    revision: 4,
    entries: [{ account_id: 25, model: 'glm-5.2', kind: 'token' as const, multiplier: 0.4 }],
  },
  active_revision: 3,
  restart_needed: true,
  groups: [{ id: 7, name: 'unified-api-internal' }],
  accounts: [{ id: 25, name: 'YeToken-guomo-text' }],
}

describe('Unified Gateway route pricing API', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
    post.mockReset()
  })

  it('loads route pricing from the admin settings endpoint and returns response data', async () => {
    get.mockResolvedValueOnce({ data: state })

    await expect(getUnifiedGatewayRoutePricing()).resolves.toEqual(state)

    expect(get).toHaveBeenCalledOnce()
    expect(get).toHaveBeenCalledWith(endpoint)
  })

  it('saves the target group, expected revision, and complete entries to the same endpoint', async () => {
    const input = {
      expected_revision: 4,
      target_group_id: 7,
      entries: [
        {
          account_id: 25,
          model: 'glm-5.2',
          kind: 'token' as const,
          multiplier: 0.4,
          token_base_price: {
            input_per_million: 8,
            output_per_million: 28,
            cache_read_per_million: 2,
            cache_write_per_million: 8,
            cache_write_5m_per_million: 8,
            cache_write_1h_per_million: 8,
          },
        },
        {
          account_id: 26,
          model: 'gpt-image-2',
          kind: 'image' as const,
          image_size: '1K',
          image_quality: 'high',
          unit_price: 0.02,
        },
      ],
    }
    put.mockResolvedValueOnce({ data: state })

    await expect(updateUnifiedGatewayRoutePricing(input)).resolves.toEqual(state)

    expect(put).toHaveBeenCalledOnce()
    expect(put).toHaveBeenCalledWith(endpoint, input)
  })

  it('round-trips a single managed time-of-day token card without flattening either tier', async () => {
    const dynamicEntry = {
      account_id: 27,
      model: 'deepseek-flash',
      kind: 'token' as const,
      source: 'wokey_catalog',
      source_fx: '6.9',
      time_of_day_token_price: {
        current_tier: 'off_peak' as const,
        peak_windows_utc: [{ start_hour: 1, end_hour: 4 }, { start_hour: 6, end_hour: 10 }],
        peak: {
          token_base_price: { input_per_million: 1.5456, output_per_million: 6.1824, cache_read_per_million: 0.030912 },
          source_prices_usd: { input_tokens: '0.224', output_tokens: '0.896', cache_read_tokens: '0.004480' },
        },
        off_peak: {
          token_base_price: { input_per_million: 0.7728, output_per_million: 3.0912, cache_read_per_million: 0.015456 },
          source_prices_usd: { input_tokens: '0.112', output_tokens: '0.448', cache_read_tokens: '0.002240' },
        },
      },
    }
    const input = { expected_revision: 4, target_group_id: 7, entries: [dynamicEntry] }
    put.mockResolvedValueOnce({ data: { ...state, saved: { ...state.saved, entries: [dynamicEntry] } } })

    await expect(updateUnifiedGatewayRoutePricing(input)).resolves.toMatchObject({ saved: { entries: [dynamicEntry] } })

    expect(put).toHaveBeenCalledWith(endpoint, input)
  })

  it('posts a manual refresh without accepting a custom URL or prices', async () => {
    post.mockResolvedValueOnce({ data: state })

    await expect(syncUnifiedGatewayWokeyPrices()).resolves.toEqual(state)

    expect(post).toHaveBeenCalledOnce()
    expect(post).toHaveBeenCalledWith(`${endpoint}/wokey-sync`)
  })

  it('manualizes one exact managed route key with revision protection', async () => {
    const input = {
      expected_revision: 4,
      key: { account_id: 28, model: 'grok-imagine-video-1.5', kind: 'video' as const, video_resolution: '720p', video_duration_seconds: 5 },
    }
    post.mockResolvedValueOnce({ data: state })

    await expect(manualizeUnifiedGatewayWokeyPrice(input)).resolves.toEqual(state)

    expect(post).toHaveBeenCalledWith(`${endpoint}/wokey-sync/manualize`, input)
  })
})

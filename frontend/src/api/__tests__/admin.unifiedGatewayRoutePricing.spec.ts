import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, put },
}))

import {
  getUnifiedGatewayRoutePricing,
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
})

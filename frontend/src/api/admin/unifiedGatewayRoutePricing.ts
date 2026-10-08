import { apiClient } from '../client'

export type RoutePricingKind = 'token' | 'image' | 'video'

export interface RouteTokenBasePrice {
  input_per_million: number
  output_per_million: number
  cache_read_per_million: number
  cache_write_per_million: number
  cache_write_5m_per_million: number
  cache_write_1h_per_million: number
}

export interface RoutePricingEntry {
  account_id: number
  model: string
  kind: RoutePricingKind
  multiplier?: number
  token_base_price?: RouteTokenBasePrice
  unit_price?: number
  image_size?: string
  image_quality?: string
  video_resolution?: string
  video_duration_seconds?: number
}

export interface RoutePricingGroupOption {
  id: number
  name: string
}

export interface RoutePricingAccountOption {
  id: number
  name: string
}

export interface UnifiedGatewayRoutePricingState {
  saved: {
    target_group_id: number
    revision: number
    entries: RoutePricingEntry[]
  }
  active_revision: number
  restart_needed: boolean
  groups: RoutePricingGroupOption[]
  accounts: RoutePricingAccountOption[]
}

export async function getUnifiedGatewayRoutePricing(): Promise<UnifiedGatewayRoutePricingState> {
  const { data } = await apiClient.get<UnifiedGatewayRoutePricingState>(
    '/admin/settings/unified-gateway-route-pricing'
  )
  return data
}

export async function updateUnifiedGatewayRoutePricing(input: {
  expected_revision: number
  target_group_id: number
  entries: RoutePricingEntry[]
}): Promise<UnifiedGatewayRoutePricingState> {
  const { data } = await apiClient.put<UnifiedGatewayRoutePricingState>(
    '/admin/settings/unified-gateway-route-pricing',
    input
  )
  return data
}

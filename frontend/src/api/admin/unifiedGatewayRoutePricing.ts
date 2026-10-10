import { apiClient } from '../client'

export type RoutePricingKind = 'token' | 'image' | 'video'
export type RouteImagePricingMode = 'flat_per_image'
export type WokeySyncCardState = 'current' | 'stale' | 'unsupported' | 'not_returned'

export interface WokeySourceSKU {
  sku_id: string
  meter: string
  unit: string
  quantity: string
  price_usd: string
}

export interface WokeySyncStatus {
  last_attempt_at?: string
  last_success_at?: string
  last_error_code?: string
  last_http_status?: number
  catalog_sha256?: string
  managed_card_count: number
  manual_conflict_count: number
  unsupported_count: number
  not_returned_count: number
  reasons?: string[]
}

export interface WokeySyncConfig {
  enabled: boolean
  account_ids: number[]
  fx: string
  interval_minutes: number
  status?: WokeySyncStatus
}

export interface RouteTokenBasePrice {
  input_per_million: number
  output_per_million: number
  cache_read_per_million: number
  cache_write_per_million?: number | null
  cache_write_5m_per_million?: number | null
  cache_write_1h_per_million?: number | null
}

export type WokeyTokenSourceMeter =
  | 'input_tokens'
  | 'output_tokens'
  | 'cache_read_tokens'
  | 'cache_write_tokens'
  | 'cache_write_5m_tokens'
  | 'cache_write_1h_tokens'

export interface WokeyTimeOfDayTokenTier {
  token_base_price: RouteTokenBasePrice
  source_prices_usd: Partial<Record<WokeyTokenSourceMeter, string>>
}

export interface WokeyTimeOfDayTokenPrice {
  current_tier: 'peak' | 'off_peak'
  peak_windows_utc: Array<{ start_hour: number; end_hour: number }>
  peak: WokeyTimeOfDayTokenTier
  off_peak: WokeyTimeOfDayTokenTier
}

export interface RoutePricingEntry {
  account_id: number
  model: string
  kind: RoutePricingKind
  multiplier?: number
  token_base_price?: RouteTokenBasePrice
  time_of_day_token_price?: WokeyTimeOfDayTokenPrice
  long_context_token_base_price?: RouteTokenBasePrice
  unit_price?: number
  image_pricing_mode?: RouteImagePricingMode
  image_size?: string
  image_quality?: string
  video_resolution?: string
  video_duration_seconds?: number
  source?: string
  source_fx?: string
  source_fetched_at?: string
  source_catalog_sha256?: string
  source_skus?: WokeySourceSKU[]
  sync_state?: WokeySyncCardState
  sync_reason?: string
}

export interface RoutePricingGroupOption {
  id: number
  name: string
}

export interface RoutePricingAccountOption {
  id: number
  name: string
  wokey_eligible: boolean
}

export interface UnifiedGatewayRoutePricingState {
  saved: {
    target_group_id: number
    revision: number
    entries: RoutePricingEntry[]
    wokey_sync?: WokeySyncConfig
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
  wokey_sync?: Omit<WokeySyncConfig, 'status'>
}): Promise<UnifiedGatewayRoutePricingState> {
  const { data } = await apiClient.put<UnifiedGatewayRoutePricingState>(
    '/admin/settings/unified-gateway-route-pricing',
    input
  )
  return data
}

export async function syncUnifiedGatewayWokeyPrices(): Promise<UnifiedGatewayRoutePricingState> {
  const { data } = await apiClient.post<UnifiedGatewayRoutePricingState>(
    '/admin/settings/unified-gateway-route-pricing/wokey-sync'
  )
  return data
}

export async function manualizeUnifiedGatewayWokeyPrice(input: {
  expected_revision: number
  key: Pick<RoutePricingEntry, 'account_id' | 'model' | 'kind' | 'image_pricing_mode' | 'image_size' | 'image_quality' | 'video_resolution' | 'video_duration_seconds'>
}): Promise<UnifiedGatewayRoutePricingState> {
  const { data } = await apiClient.post<UnifiedGatewayRoutePricingState>(
    '/admin/settings/unified-gateway-route-pricing/wokey-sync/manualize',
    input
  )
  return data
}

export type UnifiedGatewayEndpoint = 'chat_completions' | 'responses' | 'images_generations' | 'videos'
export type UnifiedGatewayBillingMode = 'token' | 'per_request' | 'image' | 'video'
export type UnifiedGatewayRateMode = 'probe_preferred' | 'manual_only' | 'probe_only'
export type UnifiedGatewayRateBasis = 'token' | 'per_request' | 'image' | 'video' | 'provider_specific'
export type UnifiedGatewayLifecycle = 'draft' | 'published' | 'disabled' | 'archived'

export interface UnifiedGatewayIssue {
  code: string
  message: string
  path?: string
}

export interface UnifiedGatewayProbe {
  status: string
  basis: string
  resolved_rate_multiplier?: string
  snapshot_ref?: string
  received_at?: string
  fresh_until?: string
}

export interface UnifiedGatewayManualPricingRules {
  formula_id: 'flat_unit_price'
  unit: 'request' | 'image' | 'video_task'
  unit_price?: string
}

export interface UnifiedGatewayPricingProfile {
  id: string
  version: string
  pricing_model: string
  billing_mode: UnifiedGatewayBillingMode
  rate_mode: UnifiedGatewayRateMode
  rate_basis: UnifiedGatewayRateBasis
  pricing_schema_id: string
  currency: 'USD'
  base_price_semantics: 'provider_base' | 'final_user_price'
  provider_base_unit_price: string | null
  manual_base_unit_price: string | null
  manual_upstream_multiplier: string | null
  user_markup_multiplier: string | null
  final_user_unit_price: string | null
  fixed_fee: string | null
  minimum_charge: string | null
  rounding_mode: 'half_up'
  precision: number
  fallback_reason: string | null
  manual_pricing_rules: UnifiedGatewayManualPricingRules | null
  charge_trigger: 'success_delivery'
  failure_charge: 'zero'
}

export interface UnifiedGatewayAccountBinding {
  id: string
  account_id: string
  display_name?: string
  schedulable: boolean
  eligibility: string
  probe?: UnifiedGatewayProbe
  priority: number
  enabled: boolean
  revision: number
  capabilities?: string[]
}

export interface UnifiedGatewayRouteTarget {
  id: string
  provider_identity: string
  upstream_model: string
  endpoint: UnifiedGatewayEndpoint
  priority: number
  bindings: UnifiedGatewayAccountBinding[]
}

export interface UnifiedGatewayBillingLane {
  id: string
  code: string
  name: string
  selection_strategy: 'fixed_priority'
  pricing_source_group_id?: string
  pricing_source_revision?: string
  profile: UnifiedGatewayPricingProfile
  targets: UnifiedGatewayRouteTarget[]
}

export interface UnifiedGatewayConfig {
  id?: string
  access_group_id: string
  public_model: string
  endpoint: UnifiedGatewayEndpoint
  lifecycle?: UnifiedGatewayLifecycle
  revision?: number
  lanes: UnifiedGatewayBillingLane[]
  readiness?: 'ready' | 'blocked'
  blockers?: UnifiedGatewayIssue[]
  warnings?: UnifiedGatewayIssue[]
  runtime_effective?: boolean
}

export interface UnifiedGatewayDraft {
  id: string
  config_id: string
  revision: number
  document: UnifiedGatewayConfig
  created_at?: string
  updated_at?: string
}

export interface UnifiedGatewayRevision {
  revision: number
  lifecycle: UnifiedGatewayLifecycle
  digest: string
  actor_id?: string
  reason?: string
  created_at: string
  document: UnifiedGatewayConfig
}

export interface UnifiedGatewaySnapshotView {
  id: string
  request_id: string
  attempt_id: string
  access_group_id: string
  public_model: string
  billing_lane_id: string
  account_id: string
  provider_identity: string
  status: string
  rate_source?: string
  policy_version?: string
  measured_units: string
  user_charge: string
  currency: string
  created_at: string
  finalized_at?: string
}

export interface UnifiedGatewayValidationResult {
  valid: boolean
  blockers: UnifiedGatewayIssue[]
  warnings: UnifiedGatewayIssue[]
  field_errors: Record<string, string>
  validation_token?: string
  server_revision: number
}

export interface UnifiedGatewayPreviewRequest {
  lane_id: string
  target_id: string
  binding_id: string
  input_tokens?: string
  output_tokens?: string
  units?: string
  delivery_state: 'success' | 'failed'
}

export interface UnifiedGatewayPreviewResult {
  valid: boolean
  preview_digest: string
  quote_id: string
  persisted: boolean
  selection_status: string
  billing_mode: string
  rate_mode: string
  resolved_rate_source: string
  probe_status?: string
  upstream_declared_rate?: string
  manual_upstream_multiplier?: string
  user_markup_multiplier?: string
  effective_multiplier?: string
  billable_units: Record<string, string>
  estimated_charge: string
  currency: string
  rounding_mode: string
  precision: number
  fallback_reason?: string | null
  policy_version: string
  profile_id: string
  probe_snapshot_ref?: string
  charge_trigger: string
  failure_charge: string
}

export interface UnifiedGatewayPricingImportResult {
  draft?: UnifiedGatewayDraft
  lane_id: string
  source_group_id: string
  source_revision: string
  profile: UnifiedGatewayPricingProfile
  import_digest: string
}

export interface UnifiedGatewayMeta {
  contract_rev: string
  server_time: string
  admin_ui_enabled: boolean
  runtime_enabled: boolean
  migration_ready: boolean
  schema_version: string
  capabilities: Record<string, boolean>
  supported_endpoints: UnifiedGatewayEndpoint[]
  supported_billing_modes: UnifiedGatewayBillingMode[]
  supported_rate_bases: UnifiedGatewayRateBasis[]
  supported_rate_modes: UnifiedGatewayRateMode[]
  supported_pricing_models: string[]
  blockers?: UnifiedGatewayIssue[]
}

export interface UnifiedGatewayOption {
  id: string
  name: string
  platform?: string
  status?: string
  schedulable?: boolean
  capabilities?: string[]
}

export interface UnifiedGatewayOptions {
  access_groups: UnifiedGatewayOption[]
  pricing_source_groups: UnifiedGatewayOption[]
  accounts: UnifiedGatewayOption[]
}

export interface UnifiedGatewayOptionsPage {
  items: UnifiedGatewayOptions
  total: number
  page: number
  page_size: number
}

import { apiClient } from '../client'
import type {
  UnifiedGatewayConfig,
  UnifiedGatewayDraft,
  UnifiedGatewayMeta,
  UnifiedGatewayOptionsPage,
  UnifiedGatewayPreviewRequest,
  UnifiedGatewayPreviewResult,
  UnifiedGatewayPricingImportResult,
  UnifiedGatewayRevision,
  UnifiedGatewaySnapshotView,
  UnifiedGatewayValidationResult,
} from '@/types/unifiedGateway'

const basePath = '/admin/unified-gateway'

function idempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  return `ug-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

function writeHeaders(key?: string, revision?: number): Record<string, string> {
  return {
    ...(key ? { 'Idempotency-Key': key } : {}),
    ...(revision !== undefined ? { 'If-Match': String(revision) } : {}),
  }
}

export async function getMeta(): Promise<UnifiedGatewayMeta> {
  const { data } = await apiClient.get<UnifiedGatewayMeta>(`${basePath}/meta`)
  return data
}

export async function getOptions(page = 1, pageSize = 100): Promise<UnifiedGatewayOptionsPage> {
  const { data } = await apiClient.get<UnifiedGatewayOptionsPage>(`${basePath}/options`, { params: { page, page_size: pageSize } })
  return data
}

export async function listConfigs(status = ''): Promise<{ items: UnifiedGatewayConfig[]; total: number; page: number; page_size: number }> {
  const { data } = await apiClient.get<{ items: UnifiedGatewayConfig[]; total: number; page: number; page_size: number }>(`${basePath}/configs`, { params: { status } })
  return data
}

export async function getConfig(id: string): Promise<UnifiedGatewayConfig> {
  const { data } = await apiClient.get<UnifiedGatewayConfig>(`${basePath}/configs/${encodeURIComponent(id)}`)
  return data
}

export async function createDraft(document: UnifiedGatewayConfig, key = idempotencyKey()): Promise<UnifiedGatewayDraft> {
  const { data } = await apiClient.post<UnifiedGatewayDraft>(`${basePath}/drafts`, { document }, { headers: writeHeaders(key) })
  return data
}

export async function createDraftFromConfig(configId: string, key = idempotencyKey()): Promise<UnifiedGatewayDraft> {
  const { data } = await apiClient.post<UnifiedGatewayDraft>(`${basePath}/configs/${encodeURIComponent(configId)}/draft`, {}, { headers: writeHeaders(key) })
  return data
}

export async function getDraft(id: string): Promise<UnifiedGatewayDraft> {
  const { data } = await apiClient.get<UnifiedGatewayDraft>(`${basePath}/drafts/${encodeURIComponent(id)}`)
  return data
}

export async function updateDraft(id: string, document: UnifiedGatewayConfig, revision: number, key = idempotencyKey()): Promise<UnifiedGatewayDraft> {
  const { data } = await apiClient.put<UnifiedGatewayDraft>(`${basePath}/drafts/${encodeURIComponent(id)}`, { document }, { headers: writeHeaders(key, revision) })
  return data
}

export async function validateDraft(id: string, document: UnifiedGatewayConfig | undefined, revision: number): Promise<UnifiedGatewayValidationResult> {
  const { data } = await apiClient.post<UnifiedGatewayValidationResult>(`${basePath}/drafts/${encodeURIComponent(id)}/validate`, document ? { document } : {}, { headers: writeHeaders(undefined, revision) })
  return data
}

export async function listRevisions(configId: string): Promise<UnifiedGatewayRevision[]> {
  const { data } = await apiClient.get<UnifiedGatewayRevision[]>(`${basePath}/configs/${encodeURIComponent(configId)}/revisions`)
  return data
}

export async function listSnapshots(params: Record<string, string | number> = {}): Promise<{ items: UnifiedGatewaySnapshotView[]; total: number; page: number; page_size: number; pages?: number }> {
  const { data } = await apiClient.get<{ items: UnifiedGatewaySnapshotView[]; total: number; page: number; page_size: number; pages?: number }>(`${basePath}/snapshots`, { params })
  return data
}

export async function previewDraft(id: string, preview: UnifiedGatewayPreviewRequest, document?: UnifiedGatewayConfig): Promise<UnifiedGatewayPreviewResult> {
  const { data } = await apiClient.post<UnifiedGatewayPreviewResult>(`${basePath}/drafts/${encodeURIComponent(id)}/preview`, { ...(document ? { document } : {}), preview })
  return data
}

export async function pricingImportPreview(id: string, laneId: string, sourceGroupId: string, document?: UnifiedGatewayConfig): Promise<UnifiedGatewayPricingImportResult> {
  const { data } = await apiClient.post<UnifiedGatewayPricingImportResult>(`${basePath}/drafts/${encodeURIComponent(id)}/pricing-import/preview`, { lane_id: laneId, source_group_id: sourceGroupId, ...(document ? { document } : {}) })
  return data
}

export async function pricingImportApply(id: string, laneId: string, sourceGroupId: string, revision: number, importDigest: string, key = idempotencyKey()): Promise<UnifiedGatewayPricingImportResult> {
  const { data } = await apiClient.post<UnifiedGatewayPricingImportResult>(`${basePath}/drafts/${encodeURIComponent(id)}/pricing-import/apply`, { lane_id: laneId, source_group_id: sourceGroupId, import_digest: importDigest }, { headers: writeHeaders(key, revision) })
  return data
}

export async function publishDraft(id: string, revision: number, validationToken: string, reason = '', key = idempotencyKey()): Promise<UnifiedGatewayConfig> {
  const { data } = await apiClient.post<UnifiedGatewayConfig>(`${basePath}/drafts/${encodeURIComponent(id)}/publish`, { validation_token: validationToken, reason }, { headers: writeHeaders(key, revision) })
  return data
}

export async function disableConfig(id: string, revision: number, reason: string, key = idempotencyKey()): Promise<UnifiedGatewayConfig> {
  const { data } = await apiClient.post<UnifiedGatewayConfig>(`${basePath}/configs/${encodeURIComponent(id)}/disable`, { reason }, { headers: writeHeaders(key, revision) })
  return data
}

export async function restoreConfig(id: string, sourceRevision: number, revision: number, reason = '', key = idempotencyKey()): Promise<UnifiedGatewayConfig> {
  const { data } = await apiClient.post<UnifiedGatewayConfig>(`${basePath}/configs/${encodeURIComponent(id)}/revisions/${sourceRevision}/restore`, { reason }, { headers: writeHeaders(key, revision) })
  return data
}

export async function probeBinding(bindingId: string, key = idempotencyKey()): Promise<Record<string, unknown>> {
  const { data } = await apiClient.post<Record<string, unknown>>(`${basePath}/bindings/${encodeURIComponent(bindingId)}/probe`, {}, { headers: writeHeaders(key) })
  return data
}

export const unifiedGatewayAPI = {
  getMeta,
  getOptions,
  listConfigs,
  getConfig,
  createDraft,
  createDraftFromConfig,
  getDraft,
  updateDraft,
  validateDraft,
  listRevisions,
  listSnapshots,
  previewDraft,
  pricingImportPreview,
  pricingImportApply,
  publishDraft,
  disableConfig,
  restoreConfig,
  probeBinding,
}

export default unifiedGatewayAPI

import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, put } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
}))

vi.mock('@/api/client', () => ({ apiClient: { get, post, put } }))

import { createDraft, createDraftFromConfig, pricingImportApply, publishDraft, restoreConfig, validateDraft } from '@/api/admin/unifiedGateway'

describe('unified gateway admin API contract', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    put.mockReset()
    post.mockResolvedValue({ data: { id: 'draft_1' } })
  })

  it('creates a draft with an idempotency key and the aggregate document', async () => {
    const document = { access_group_id: 'ag_7', public_model: 'gpt-5.5', endpoint: 'chat_completions', lanes: [] }
    await createDraft(document, 'create-key')

    expect(post).toHaveBeenCalledWith('/admin/unified-gateway/drafts', { document }, { headers: { 'Idempotency-Key': 'create-key' } })
  })

  it('publishes with If-Match and the validation token', async () => {
    await publishDraft('draft_1', 3, 'sha256:validation', 'publish now', 'publish-key')

    expect(post).toHaveBeenCalledWith('/admin/unified-gateway/drafts/draft_1/publish', { validation_token: 'sha256:validation', reason: 'publish now' }, {
      headers: { 'Idempotency-Key': 'publish-key', 'If-Match': '3' },
    })
  })

  it('validates against the draft revision with If-Match', async () => {
    await validateDraft('draft_1', { access_group_id: 'ag_7', public_model: 'gpt-5.5', endpoint: 'chat_completions', lanes: [] }, 2)

    expect(post).toHaveBeenCalledWith('/admin/unified-gateway/drafts/draft_1/validate', {
      document: { access_group_id: 'ag_7', public_model: 'gpt-5.5', endpoint: 'chat_completions', lanes: [] },
    }, { headers: { 'If-Match': '2' } })
  })

  it('opens an existing config draft with an idempotency key', async () => {
    await createDraftFromConfig('mc_1', 'edit-key')

    expect(post).toHaveBeenCalledWith('/admin/unified-gateway/configs/mc_1/draft', {}, { headers: { 'Idempotency-Key': 'edit-key' } })
  })

  it('uses the revision in the restore path, not a mutable body owner field', async () => {
    await restoreConfig('mc_1', 4, 6, 'restore', 'restore-key')

    expect(post).toHaveBeenCalledWith('/admin/unified-gateway/configs/mc_1/revisions/4/restore', { reason: 'restore' }, {
      headers: { 'Idempotency-Key': 'restore-key', 'If-Match': '6' },
    })
  })

  it('sends the confirmed pricing import digest with the draft revision and key', async () => {
    await pricingImportApply('draft_1', 'lane_1', 'ag_7', 4, 'sha256:preview', 'import-key')

    expect(post).toHaveBeenCalledWith('/admin/unified-gateway/drafts/draft_1/pricing-import/apply', {
      lane_id: 'lane_1', source_group_id: 'ag_7', import_digest: 'sha256:preview',
    }, { headers: { 'Idempotency-Key': 'import-key', 'If-Match': '4' } })
  })
})

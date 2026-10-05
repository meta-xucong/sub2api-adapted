import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post, put } }))

import * as gateway from '@/api/admin/unifiedGateway'

describe('unified gateway admin API', () => {
  beforeEach(() => {
    get.mockReset().mockResolvedValue({ data: {} })
    post.mockReset().mockResolvedValue({ data: {} })
    put.mockReset().mockResolvedValue({ data: {} })
  })

  it('reads admin metadata from the management endpoint', async () => {
    await gateway.getMeta()
    expect(get).toHaveBeenCalledWith('/admin/unified-gateway/meta')
  })

  it('creates and updates drafts with idempotency and revision preconditions', async () => {
    const document = { access_group_id: 'group-1', public_model: 'gpt-5.5', endpoint: 'responses', lanes: [] }
    await gateway.createDraft(document, 'create-key')
    expect(post).toHaveBeenCalledWith('/admin/unified-gateway/drafts', { document }, { headers: { 'Idempotency-Key': 'create-key' } })

    await gateway.updateDraft('draft/1', document, 7, 'update-key')
    expect(put).toHaveBeenCalledWith('/admin/unified-gateway/drafts/draft%2F1', { document }, { headers: { 'Idempotency-Key': 'update-key', 'If-Match': '7' } })
  })

  it('keeps validation and publish bound to the draft/config revision', async () => {
    await gateway.validateDraft('draft-1', undefined, 4)
    expect(post).toHaveBeenCalledWith('/admin/unified-gateway/drafts/draft-1/validate', {}, { headers: { 'If-Match': '4' } })

    await gateway.publishDraft('draft-1', 9, 'validation-token', 'reviewed', 'publish-key')
    expect(post).toHaveBeenLastCalledWith('/admin/unified-gateway/drafts/draft-1/publish', { validation_token: 'validation-token', reason: 'reviewed' }, { headers: { 'Idempotency-Key': 'publish-key', 'If-Match': '9' } })
  })

  it('applies pricing import only with its preview digest', async () => {
    await gateway.pricingImportApply('draft-1', 'lane-1', 'group-2', 5, 'digest-1', 'import-key')
    expect(post).toHaveBeenCalledWith('/admin/unified-gateway/drafts/draft-1/pricing-import/apply', { lane_id: 'lane-1', source_group_id: 'group-2', import_digest: 'digest-1' }, { headers: { 'Idempotency-Key': 'import-key', 'If-Match': '5' } })
  })

  it('does not expose snapshot or provider-probe operations in the admin API', () => {
    expect(gateway.unifiedGatewayAPI).not.toHaveProperty('listSnapshots')
    expect(gateway.unifiedGatewayAPI).not.toHaveProperty('probeBinding')
  })
})

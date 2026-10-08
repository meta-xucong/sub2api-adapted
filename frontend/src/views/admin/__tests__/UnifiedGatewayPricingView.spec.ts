import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import UnifiedGatewayPricingView from '../UnifiedGatewayPricingView.vue'

const api = vi.hoisted(() => ({
  get: vi.fn(),
  update: vi.fn(),
}))

vi.mock('@/api/admin/unifiedGatewayRoutePricing', () => ({
  getUnifiedGatewayRoutePricing: api.get,
  updateUnifiedGatewayRoutePricing: api.update,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const AppLayoutStub = defineComponent({ template: '<div><slot /></div>' })

const initialState = () => ({
  saved: {
    target_group_id: 7,
    revision: 3,
    entries: [],
  },
  active_revision: 2,
  restart_needed: true,
  groups: [{ id: 7, name: 'unified-api-internal' }],
  accounts: [{ id: 42, name: 'YeToken' }, { id: 43, name: 'Wokey' }],
})

function mountPage() {
  return mount(UnifiedGatewayPricingView, { global: { stubs: { AppLayout: AppLayoutStub } } })
}

describe('Unified Gateway route pricing page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.get.mockResolvedValue(initialState())
    api.update.mockImplementation(async (input) => ({
      ...initialState(),
      saved: { target_group_id: input.target_group_id, revision: 4, entries: input.entries },
      active_revision: 2,
      restart_needed: true,
    }))
  })

  it('loads, edits, and saves an account/model token multiplier using revision protection', async () => {
    const wrapper = mountPage()
    await flushPromises()

    expect(api.get).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('unified-api-internal')
    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingRestartRequired')
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.get('[data-testid="route-pricing-account"]').setValue('42')
    await wrapper.get('[data-testid="route-pricing-model"]').setValue('glm-5.2')
    await wrapper.get('[data-testid="route-pricing-multiplier"]').setValue('0.4')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).toHaveBeenCalledWith({
      expected_revision: 3,
      target_group_id: 7,
      entries: [{ account_id: 42, model: 'glm-5.2', kind: 'token', multiplier: 0.4 }],
    })
    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingSaved')
    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingSavedRevision')
  })

  it('shows the base-card unit and precedence, and saves a complete card alongside its legacy fallback', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.get('[data-testid="route-pricing-account"]').setValue('42')
    await wrapper.get('[data-testid="route-pricing-model"]').setValue('glm-5.2')
    await wrapper.get('[data-testid="route-pricing-multiplier"]').setValue('0.4')
    await wrapper.get('[data-testid="add-route-base-price"]').trigger('click')

    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingBasePriceUnit')
    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingBasePricePrecedence')
    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingBaseCacheWrite1hZeroUnsupported')

    const values = [8, 28, 2, 8, 8, 8]
    const fields = [
      'route-base-input',
      'route-base-output',
      'route-base-cache-read',
      'route-base-cache-write',
      'route-base-cache-write-5m',
      'route-base-cache-write-1h',
    ]
    for (const [index, field] of fields.entries()) {
      await wrapper.get(`[data-testid="${field}"]`).setValue(String(values[index]))
    }
    expect(wrapper.get('[data-testid="route-pricing-source"]').text()).toContain('pricingConfiguredSource')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).toHaveBeenCalledWith({
      expected_revision: 3,
      target_group_id: 7,
      entries: [{
        account_id: 42,
        model: 'glm-5.2',
        kind: 'token',
        multiplier: 0.4,
        token_base_price: {
          input_per_million: 8,
          output_per_million: 28,
          cache_read_per_million: 2,
          cache_write_per_million: 8,
          cache_write_5m_per_million: 8,
          cache_write_1h_per_million: 8,
        },
      }],
    })
  })

  it('keeps every existing route entry when adding a base-price card', async () => {
    const existingMultiplier = { account_id: 88, model: 'legacy-model', kind: 'token' as const, multiplier: 0.6 }
    const existingMedia = { account_id: 89, model: 'grok-imagine-video-1.5', kind: 'video' as const, video_resolution: '720p', video_duration_seconds: 5, unit_price: 0.049 }
    api.get.mockResolvedValueOnce({
      ...initialState(),
      saved: { target_group_id: 7, revision: 3, entries: [existingMultiplier, existingMedia] },
    })
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.findAll('[data-testid="route-pricing-account"]')[2].setValue('42')
    await wrapper.findAll('[data-testid="route-pricing-model"]')[2].setValue('glm-5.2')
    await wrapper.findAll('[data-testid="add-route-base-price"]')[1].trigger('click')
    for (const field of [
      'route-base-input', 'route-base-output', 'route-base-cache-read',
      'route-base-cache-write', 'route-base-cache-write-5m', 'route-base-cache-write-1h',
    ]) {
      await wrapper.get(`[data-testid="${field}"]`).setValue('0')
    }
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    const savedEntries = api.update.mock.calls[0][0].entries
    expect(savedEntries[0]).toEqual(existingMultiplier)
    expect(savedEntries[1]).toEqual(existingMedia)
    expect(savedEntries).toHaveLength(3)
  })

  it('rejects an incomplete base-price card and permits a card-only zero-price entry', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.get('[data-testid="route-pricing-model"]').setValue('glm-5.2')
    await wrapper.get('[data-testid="add-route-base-price"]').trigger('click')
    await wrapper.get('[data-testid="route-base-input"]').setValue('0')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingBasePriceInvalid')

    const fields = [
      'route-base-input',
      'route-base-output',
      'route-base-cache-read',
      'route-base-cache-write',
      'route-base-cache-write-5m',
      'route-base-cache-write-1h',
    ]
    for (const field of fields) {
      await wrapper.get(`[data-testid="${field}"]`).setValue('0')
    }
    await wrapper.get('[data-testid="route-pricing-multiplier"]').setValue('')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).toHaveBeenCalledWith({
      expected_revision: 3,
      target_group_id: 7,
      entries: [{
        account_id: 42,
        model: 'glm-5.2',
        kind: 'token',
        token_base_price: {
          input_per_million: 0,
          output_per_million: 0,
          cache_read_per_million: 0,
          cache_write_per_million: 0,
          cache_write_5m_per_million: 0,
          cache_write_1h_per_million: 0,
        },
      }],
    })
  })

  it('keeps the model input mounted while its editable value changes', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    const modelInput = wrapper.get('[data-testid="route-pricing-model"]')
    const originalElement = modelInput.element

    await modelInput.setValue('glm-5.2')

    expect(wrapper.get('[data-testid="route-pricing-model"]').element).toBe(originalElement)
  })

  it('disables adding rows while the save is pending', async () => {
    let completeUpdate: ((state: ReturnType<typeof initialState>) => void) | undefined
    api.update.mockImplementationOnce(() => new Promise((resolve) => {
      completeUpdate = resolve
    }))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.get('[data-testid="route-pricing-account"]').setValue('42')
    await wrapper.get('[data-testid="route-pricing-model"]').setValue('glm-5.2')

    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')

    expect(wrapper.get('[data-testid="add-route-pricing"]').attributes('disabled')).toBeDefined()
    completeUpdate?.({
      ...initialState(),
      saved: { target_group_id: 7, revision: 4, entries: [{ account_id: 42, model: 'glm-5.2', kind: 'token', multiplier: 1 }] },
    })
    await flushPromises()
    expect(wrapper.get('[data-testid="add-route-pricing"]').attributes('disabled')).toBeUndefined()
  })

  it('shows the revision conflict message for normalized HTTP 409 errors', async () => {
    api.update.mockRejectedValueOnce({ status: 409, code: 'REVISION_CONFLICT', message: 'settings revision changed' })
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.get('[data-testid="route-pricing-model"]').setValue('glm-5.2')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingConflict')
    expect(wrapper.text()).not.toContain('admin.unifiedGateway.pricingSaveFailed')
  })

  it('distinguishes an initial load failure from an empty group list and retries the request', async () => {
    api.get.mockRejectedValueOnce(new Error('network unavailable')).mockResolvedValueOnce(initialState())
    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingLoadFailed')
    expect(wrapper.text()).not.toContain('admin.unifiedGateway.pricingNoCompositeGroup')
    await wrapper.get('[data-testid="retry-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.get).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('unified-api-internal')
    expect(wrapper.find('[data-testid="retry-route-pricing"]').exists()).toBe(false)
  })

  it('shows the no-group message only after a successful empty response', async () => {
    api.get.mockResolvedValueOnce({ ...initialState(), groups: [] })
    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingNoCompositeGroup')
    expect(wrapper.find('[data-testid="retry-route-pricing"]').exists()).toBe(false)
  })

  it('filters visible routes without removing entries from the saved payload', async () => {
    const entries = [
      { account_id: 42, model: 'glm-5.2', kind: 'token' as const, multiplier: 0.4 },
      { account_id: 43, model: 'gpt-image-2', kind: 'image' as const, image_size: '1K', image_quality: 'high', unit_price: 0.02 },
    ]
    api.get.mockResolvedValueOnce({
      ...initialState(),
      saved: { target_group_id: 7, revision: 3, entries },
    })
    const wrapper = mountPage()
    await flushPromises()

    await wrapper.get('[data-testid="route-pricing-search"]').setValue('gpt-image-2')
    expect(wrapper.findAll('[data-testid="route-pricing-row"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="route-pricing-search-count"]').text()).toContain('pricingSearchSummary')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).toHaveBeenCalledWith({ expected_revision: 3, target_group_id: 7, entries })
  })

  it('removes the matching original entry when deleting a filtered route', async () => {
    const entries = [
      { account_id: 42, model: 'glm-5.2', kind: 'token' as const, multiplier: 0.4 },
      { account_id: 43, model: 'gpt-image-2', kind: 'image' as const, image_size: '1K', image_quality: 'high', unit_price: 0.02 },
      { account_id: 43, model: 'claude-sonnet', kind: 'token' as const, multiplier: 0.8 },
    ]
    api.get.mockResolvedValueOnce({
      ...initialState(),
      saved: { target_group_id: 7, revision: 3, entries },
    })
    const wrapper = mountPage()
    await flushPromises()

    await wrapper.get('[data-testid="route-pricing-search"]').setValue('gpt-image-2')
    await wrapper.get('[data-testid="route-pricing-row"] button').trigger('click')
    await wrapper.get('[data-testid="clear-route-pricing-search"]').trigger('click')

    const remainingModels = await wrapper.findAll('[data-testid="route-pricing-model"]')
    expect(remainingModels.map((input) => (input.element as HTMLInputElement).value)).toEqual(['glm-5.2', 'claude-sonnet'])
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).toHaveBeenCalledWith({ expected_revision: 3, target_group_id: 7, entries: [entries[0], entries[2]] })
  })

  it('clears an active search when adding a route so the new row is visible', async () => {
    const entries = [{ account_id: 42, model: 'glm-5.2', kind: 'token' as const, multiplier: 0.4 }]
    api.get.mockResolvedValueOnce({
      ...initialState(),
      saved: { target_group_id: 7, revision: 3, entries },
    })
    const wrapper = mountPage()
    await flushPromises()

    await wrapper.get('[data-testid="route-pricing-search"]').setValue('glm-5.2')
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')

    expect((wrapper.get('[data-testid="route-pricing-search"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.findAll('[data-testid="route-pricing-row"]')).toHaveLength(2)
  })

  it('rejects duplicate token routes even when their multipliers differ', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.get('[data-testid="route-pricing-model"]').setValue('glm-5.2')
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.findAll('[data-testid="route-pricing-model"]')[1].setValue('glm-5.2')
    await wrapper.findAll('[data-testid="route-pricing-multiplier"]')[1].setValue('0.4')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingDuplicate')
  })

  it('rejects duplicate image routes after normalizing quality case', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.get('[data-testid="route-pricing-model"]').setValue('gpt-image-2')
    await wrapper.get('[data-testid="route-pricing-kind"]').setValue('image')
    await wrapper.get('[data-testid="route-pricing-image-size"]').setValue('2K')
    await wrapper.get('[data-testid="route-pricing-image-quality"]').setValue('HIGH')
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.findAll('[data-testid="route-pricing-model"]')[1].setValue('gpt-image-2')
    await wrapper.findAll('[data-testid="route-pricing-kind"]')[1].setValue('image')
    await wrapper.findAll('[data-testid="route-pricing-image-size"]')[1].setValue('2K')
    await wrapper.findAll('[data-testid="route-pricing-image-quality"]')[1].setValue('high')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingDuplicate')
  })

  it('rejects duplicate video routes when resolution aliases map to the same tier', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.get('[data-testid="route-pricing-model"]').setValue('grok-imagine-video-1.5')
    await wrapper.get('[data-testid="route-pricing-kind"]').setValue('video')
    await wrapper.get('[data-testid="route-pricing-video-resolution"]').setValue('HD')
    await wrapper.get('[data-testid="route-pricing-video-duration"]').setValue('5')
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.findAll('[data-testid="route-pricing-model"]')[1].setValue('grok-imagine-video-1.5')
    await wrapper.findAll('[data-testid="route-pricing-kind"]')[1].setValue('video')
    await wrapper.findAll('[data-testid="route-pricing-video-resolution"]')[1].setValue('720p')
    await wrapper.findAll('[data-testid="route-pricing-video-duration"]')[1].setValue('5')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingDuplicate')
  })

  it('saves exact image specs with a fixed per-image user price', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.get('[data-testid="route-pricing-model"]').setValue('gpt-image-2')
    await wrapper.get('[data-testid="route-pricing-kind"]').setValue('image')
    await wrapper.get('[data-testid="route-pricing-image-size"]').setValue('1K')
    await wrapper.get('[data-testid="route-pricing-image-quality"]').setValue('high')
    await wrapper.get('[data-testid="route-pricing-unit-price"]').setValue('0.07')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).toHaveBeenCalledWith({
      expected_revision: 3,
      target_group_id: 7,
      entries: [{
        account_id: 42,
        model: 'gpt-image-2',
        kind: 'image',
        image_size: '1K',
        image_quality: 'high',
        unit_price: 0.07,
      }],
    })
  })

  it('saves exact video resolution and duration with a fixed per-video price', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.get('[data-testid="route-pricing-account"]').setValue('43')
    await wrapper.get('[data-testid="route-pricing-model"]').setValue('grok-imagine-video-1.5')
    await wrapper.get('[data-testid="route-pricing-kind"]').setValue('video')
    await wrapper.get('[data-testid="route-pricing-video-resolution"]').setValue('720p')
    await wrapper.get('[data-testid="route-pricing-video-duration"]').setValue('5')
    await wrapper.get('[data-testid="route-pricing-video-unit-price"]').setValue('0.049')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).toHaveBeenCalledWith({
      expected_revision: 3,
      target_group_id: 7,
      entries: [{
        account_id: 43,
        model: 'grok-imagine-video-1.5',
        kind: 'video',
        video_resolution: '720p',
        video_duration_seconds: 5,
        unit_price: 0.049,
      }],
    })
  })

  it.each([0, 16, 1.5])('rejects video duration %s before sending the update', async (duration) => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.get('[data-testid="route-pricing-model"]').setValue('grok-imagine-video-1.5')
    await wrapper.get('[data-testid="route-pricing-kind"]').setValue('video')
    await wrapper.get('[data-testid="route-pricing-video-resolution"]').setValue('720p')
    await wrapper.get('[data-testid="route-pricing-video-duration"]').setValue(String(duration))
    await wrapper.get('[data-testid="route-pricing-video-unit-price"]').setValue('0.049')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingVideoDurationRange')
  })

  it('does not send an incomplete tariff and requires a valid key field', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="add-route-pricing"]').trigger('click')
    await wrapper.get('[data-testid="save-route-pricing"]').trigger('click')
    await flushPromises()

    expect(api.update).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.unifiedGateway.pricingRequired')
  })
})

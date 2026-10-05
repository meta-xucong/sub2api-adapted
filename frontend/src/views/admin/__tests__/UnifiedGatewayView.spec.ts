import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import UnifiedGatewayView from '../UnifiedGatewayView.vue'

const api = vi.hoisted(() => ({
  getMeta: vi.fn(), getOptions: vi.fn(), listModelCandidates: vi.fn(), listConfigs: vi.fn(),
  getConfig: vi.fn(), createDraft: vi.fn(), createDraftFromConfig: vi.fn(), getDraft: vi.fn(), updateDraft: vi.fn(),
  validateDraft: vi.fn(), listRevisions: vi.fn(), previewDraft: vi.fn(), pricingImportPreview: vi.fn(),
  pricingImportApply: vi.fn(), publishDraft: vi.fn(), disableConfig: vi.fn(), restoreConfig: vi.fn(),
}))

vi.mock('@/api/admin/unifiedGateway', () => api)
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('@/composables/useStepUp', () => ({
  isStepUpBlocked: () => false, isStepUpCancelled: () => false, stepUpBlockReason: () => '',
  useStepUp: () => ({ run: (action: () => Promise<unknown>) => action() }),
}))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const AppLayoutStub = defineComponent({ template: '<div><slot /></div>' })
const meta = {
  contract_rev: 'v1', server_time: '2026-10-05T00:00:00Z', admin_ui_enabled: true, runtime_enabled: false,
  runtime_effective: false, migration_ready: true, schema_version: '1', capabilities: { read: true, draft: true, publish: true },
  supported_endpoints: ['chat_completions', 'responses'], supported_billing_modes: ['token', 'per_request', 'image', 'video'],
  supported_rate_bases: ['token', 'per_request', 'image', 'video', 'provider_specific'], supported_rate_modes: ['manual_only', 'probe_preferred', 'probe_only'],
  supported_pricing_models: ['provider_metered'],
}

function mountPage() {
  return mount(UnifiedGatewayView, { global: { stubs: { AppLayout: AppLayoutStub, TotpStepUpDialog: true } } })
}

describe('Unified Gateway admin page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.getMeta.mockResolvedValue(meta)
    api.getOptions.mockResolvedValue({ items: { access_groups: [{ id: 'group-1', name: 'Unified' }], pricing_source_groups: [], accounts: [{ id: 'account-1', name: 'YeToken', platform: 'openai', status: 'active', schedulable: true }] }, total: 1, page: 1, page_size: 100 })
    api.listModelCandidates.mockResolvedValue({ items: [{ public_model: 'deepseek-v4', upstream_model: 'deepseek-v4-flash', provider_identity: 'yetoken', endpoint: 'chat_completions', account_id: 'account-1', account_name: 'YeToken', account_status: 'active', schedulable: true, runtime_eligible: true }] })
    api.listConfigs.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    api.createDraft.mockResolvedValue({ id: 'draft-1', revision: 2, document: { access_group_id: 'group-1', public_model: 'deepseek-v4', endpoint: 'chat_completions', lanes: [] } })
    api.updateDraft.mockResolvedValue({ id: 'draft-1', revision: 3, document: { access_group_id: 'group-1', public_model: 'deepseek-v4', endpoint: 'chat_completions', lanes: [] } })
    api.validateDraft.mockResolvedValue({ valid: true, blockers: [], warnings: [], field_errors: {}, validation_token: 'valid-1', server_revision: 2 })
    api.publishDraft.mockResolvedValue({ id: 'config-1', access_group_id: 'group-1', public_model: 'deepseek-v4', endpoint: 'chat_completions', lifecycle: 'published', revision: 3, lanes: [], runtime_effective: false })
  })

  it('keeps runtime state read-only and removes snapshot/probe actions', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[data-testid="status-runtime-enabled"]').text()).toContain('disabledStatus')
    expect(wrapper.get('[data-testid="runtime-notice"]').text()).toContain('runtimeDisabled')
    expect(wrapper.find('[data-testid="gateway-runtime-toggle"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="gateway-probe"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="gateway-snapshots"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('listSnapshots')
    expect(wrapper.text()).not.toContain('probeBinding')
  })

  it('does not claim runtime is effective when only the server flag is enabled', async () => {
    api.getMeta.mockResolvedValue({ ...meta, runtime_enabled: true, runtime_effective: false })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[data-testid="status-runtime-enabled"]').text()).toContain('enabledStatus')
    expect(wrapper.get('[data-testid="status-runtime-effective"]').text()).toContain('disabledStatus')
    expect(wrapper.get('[data-testid="runtime-notice"]').text()).toContain('runtimeEnabled')
  })

  it('fails closed when admin UI metadata is disabled', async () => {
    api.getMeta.mockResolvedValue({ ...meta, admin_ui_enabled: false })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[data-testid="gateway-gate"]').exists()).toBe(true)
    expect(api.getOptions).not.toHaveBeenCalled()
    expect(api.listConfigs).not.toHaveBeenCalled()
  })

  it('prefills the editor from an eligible candidate without enabling runtime', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="gateway-configure-model"]').trigger('click')
    expect((wrapper.get('[data-testid="gateway-public-model"]').element as HTMLInputElement).value).toBe('deepseek-v4')
    expect(wrapper.find('[data-testid="gateway-lane-0"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="runtime-notice"]').text()).toContain('runtimeDisabled')
  })

  it('saves a draft, validates, and publishes through the step-up wrapper', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="gateway-configure-model"]').trigger('click')
    await wrapper.get('[data-testid="gateway-save"]').trigger('click')
    await flushPromises()
    expect(api.createDraft).toHaveBeenCalledOnce()
    await wrapper.get('[data-testid="gateway-publish"]').trigger('click')
    await flushPromises()
    expect(api.validateDraft).toHaveBeenCalled()
    expect(api.publishDraft).toHaveBeenCalledWith('draft-1', 0, 'valid-1', '')
  })

  it('shows full pricing import profile and source revision before applying it', async () => {
    api.getOptions.mockResolvedValue({ items: { access_groups: [{ id: 'group-1', name: 'Unified' }], pricing_source_groups: [{ id: 'ag_8', name: 'Pricing source' }], accounts: [{ id: 'account-1', name: 'YeToken', platform: 'openai', status: 'active', schedulable: true }] }, total: 1, page: 1, page_size: 100 })
    api.createDraft.mockImplementation(async (document: any) => ({ id: 'draft-1', revision: 0, document }))
    api.pricingImportPreview.mockResolvedValue({
      lane_id: 'lane-preview', source_group_id: 'ag_8', source_revision: 'source-revision-8', import_digest: 'digest-8',
      profile: { pricing_model: 'provider_metered', billing_mode: 'token', rate_mode: 'manual_only', rate_basis: 'token', pricing_schema_id: 'token_v1', base_price_semantics: 'provider_base', provider_base_unit_price: '0.00001', manual_upstream_multiplier: '1.500000', user_markup_multiplier: '1.200000', final_user_unit_price: null },
    })
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="gateway-configure-model"]').trigger('click')
    await wrapper.get('[data-testid="gateway-save"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="gateway-pricing-source"]').setValue('ag_8')
    await wrapper.get('[data-testid="gateway-pricing-source"]').trigger('change')
    const previewButton = wrapper.findAll('button').find(button => button.text() === 'admin.unifiedGateway.previewImport')
    expect(previewButton).toBeDefined()
    await previewButton!.trigger('click')
    await flushPromises()

    const preview = wrapper.get('[data-testid="gateway-pricing-import-preview"]').text()
    expect(preview).toContain('provider_metered')
    expect(preview).toContain('token_v1')
    expect(preview).toContain('0.00001')
    expect(preview).toContain('1.500000')
    expect(preview).toContain('1.200000')
    expect(preview).toContain('source-revision-8')
    expect(preview).toContain('digest-8')
    expect(api.pricingImportApply).not.toHaveBeenCalled()
  })
})

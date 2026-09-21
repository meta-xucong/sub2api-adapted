import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { UnifiedGatewayConfig, UnifiedGatewayMeta } from '@/types/unifiedGateway'
import UnifiedGatewayView from '../UnifiedGatewayView.vue'

const {
  getMeta,
  getOptions,
  listModelCandidates,
  listConfigs,
  probeBinding,
} = vi.hoisted(() => ({
  getMeta: vi.fn(),
  getOptions: vi.fn(),
  listModelCandidates: vi.fn(),
  listConfigs: vi.fn(),
  probeBinding: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    unifiedGateway: {
      getMeta,
      getOptions,
      listModelCandidates,
      listConfigs,
      getConfig: vi.fn(),
      createDraft: vi.fn(),
      createDraftFromConfig: vi.fn(),
      getDraft: vi.fn(),
      updateDraft: vi.fn(),
      validateDraft: vi.fn(),
      listRevisions: vi.fn().mockResolvedValue([]),
      listSnapshots: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 10 }),
      previewDraft: vi.fn(),
      pricingImportPreview: vi.fn(),
      pricingImportApply: vi.fn(),
      publishDraft: vi.fn(),
      disableConfig: vi.fn(),
      restoreConfig: vi.fn(),
      probeBinding,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }),
}))

vi.mock('@/composables/useStepUp', () => ({
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => '',
  useStepUp: () => ({ run: (action: () => Promise<unknown>) => action() }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

const AppLayoutStub = defineComponent({
  template: '<main><slot /></main>',
})

function makeMeta(overrides: Partial<UnifiedGatewayMeta> = {}): UnifiedGatewayMeta {
  return {
    contract_rev: 'v1.3.2-route-guard',
    server_time: '2026-09-18T00:00:00Z',
    admin_ui_enabled: true,
    runtime_enabled: false,
    migration_ready: true,
    schema_version: '1',
    capabilities: { read: true, draft: true, publish: true, probe: false },
    supported_endpoints: ['chat_completions', 'responses', 'images_generations', 'images_edits', 'videos'],
    supported_billing_modes: ['token', 'per_request', 'image', 'video'],
    supported_rate_bases: ['token', 'per_request', 'image', 'video', 'provider_specific'],
    supported_rate_modes: ['probe_preferred', 'manual_only', 'probe_only'],
    supported_pricing_models: ['provider_metered'],
    ...overrides,
  }
}

function makeConfig(overrides: Partial<UnifiedGatewayConfig> = {}): UnifiedGatewayConfig {
  return {
    id: 'config_1',
    access_group_id: 'group_1',
    public_model: 'public-model',
    endpoint: 'responses',
    lifecycle: 'published',
    revision: 3,
    lanes: [],
    readiness: 'ready',
    runtime_effective: false,
    ...overrides,
  }
}

function mountView() {
  return mount(UnifiedGatewayView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TotpStepUpDialog: true,
      },
    },
  })
}

describe('admin UnifiedGatewayView availability state', () => {
  beforeEach(() => {
    getMeta.mockReset()
    getOptions.mockReset()
    listModelCandidates.mockReset()
    listConfigs.mockReset()
    probeBinding.mockReset()

    getMeta.mockResolvedValue(makeMeta())
    getOptions.mockResolvedValue({ items: { access_groups: [], pricing_source_groups: [], accounts: [] }, total: 0, page: 1, page_size: 100 })
    listModelCandidates.mockResolvedValue({ items: [] })
    listConfigs.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
  })

  it('keeps management available when runtime is disabled and preserves endpoint/effective-state reporting', async () => {
    listConfigs.mockResolvedValue({ items: [makeConfig()], total: 1, page: 1, page_size: 20 })

    const wrapper = mountView()
    await flushPromises()

    expect(getOptions).toHaveBeenCalledOnce()
    expect(listConfigs).toHaveBeenCalledWith('')
    expect(wrapper.get('[data-testid="status-management"]').text()).toBe('admin.unifiedGateway.statusManagementAvailable')
    expect(wrapper.get('[data-testid="status-admin-ui"]').text()).toBe('admin.unifiedGateway.statusEnabled')
    expect(wrapper.get('[data-testid="status-runtime-enabled"]').text()).toBe('admin.unifiedGateway.statusDisabled')
    expect(wrapper.get('[data-testid="status-runtime-effective"]').text()).toBe('admin.unifiedGateway.runtimeEffectiveDisabled')
    expect(wrapper.get('[data-testid="status-write"]').text()).toBe('admin.unifiedGateway.statusAvailable')
    expect(wrapper.get('[data-testid="gateway-editor"]').exists()).toBe(true)
    expect(wrapper.get('fieldset').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="gateway-endpoint"]').text()).toBe('admin.unifiedGateway.endpointChatCompletions')
    expect(wrapper.get('[data-testid="status-last-checked"]').text()).toBe('2026-09-18T00:00:00Z')
    expect(wrapper.get('[data-testid="gateway-api-usage"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="gateway-model-list-path"]').text()).toBe('/models')
    expect(wrapper.text()).toContain('admin.unifiedGateway.statusRuntimeEffective')
    expect(wrapper.text()).toContain('admin.unifiedGateway.runtimeEffectiveDisabled')
    expect(wrapper.text()).toContain('admin.unifiedGateway.runtimeDisabled')
    expect(wrapper.text()).not.toContain('admin.unifiedGateway.runtimeOff')
  })

  it('keeps all unconfirmed price inputs explicit', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.findAll('input[placeholder="0.000010000000"]').every(input => input.element.value === '')).toBe(true)
    expect(wrapper.get('input[placeholder="1.000000"]').element.value).toBe('')
    expect(wrapper.get('input[placeholder="1.200000"]').element.value).toBe('')
  })

  it('fails closed when the migration is not ready', async () => {
    getMeta.mockResolvedValue(makeMeta({ migration_ready: false, runtime_enabled: true }))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="gateway-gate"]').text()).toContain('admin.unifiedGateway.migrationNotReady')
    expect(wrapper.find('[data-testid="gateway-editor"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="status-migration"]').text()).toBe('admin.unifiedGateway.statusNotReady')
    expect(wrapper.get('[data-testid="status-runtime-enabled"]').text()).toBe('admin.unifiedGateway.statusEnabled')
    expect(getOptions).not.toHaveBeenCalled()
    expect(listConfigs).not.toHaveBeenCalled()
  })

  it('fails closed when the admin UI gate is disabled', async () => {
    getMeta.mockResolvedValue(makeMeta({ admin_ui_enabled: false, runtime_enabled: true }))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="gateway-gate"]').text()).toContain('admin.unifiedGateway.uiDisabled')
    expect(wrapper.get('[data-testid="status-admin-ui"]').text()).toBe('admin.unifiedGateway.statusDisabled')
    expect(wrapper.find('[data-testid="gateway-editor"]').exists()).toBe(false)
    expect(getOptions).not.toHaveBeenCalled()
    expect(listConfigs).not.toHaveBeenCalled()
  })

  it('shows read-only capability state and disables management writes', async () => {
    getMeta.mockResolvedValue(makeMeta({ capabilities: { read: true, draft: false, publish: false, write: false, probe: false } }))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="status-management"]').text()).toBe('admin.unifiedGateway.statusReadOnly')
    expect(wrapper.get('[data-testid="status-read"]').text()).toBe('admin.unifiedGateway.statusAvailable')
    expect(wrapper.get('[data-testid="status-write"]').text()).toBe('admin.unifiedGateway.statusUnavailable')
    expect(wrapper.get('fieldset').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="gateway-publish"]').attributes('disabled')).toBeDefined()
  })

  it('does not expose an unsupported network probe action', async () => {
    getOptions.mockResolvedValue({
      items: {
        access_groups: [],
        pricing_source_groups: [],
        accounts: [{ id: 'account_1', name: 'Account 1', platform: 'openai', status: 'active', schedulable: true, capabilities: ['responses'] }],
      },
      total: 1,
      page: 1,
      page_size: 100,
    })

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="gateway-probe"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="gateway-probe-unsupported"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="status-probe"]').text()).toBe('admin.unifiedGateway.statusUnavailable')
    expect(probeBinding).not.toHaveBeenCalled()
  })

  it('groups candidate routes and prefills a simple configuration from an eligible route', async () => {
    getOptions.mockResolvedValue({
      items: {
        access_groups: [{ id: 'group_1', name: 'Unified API' }],
        pricing_source_groups: [],
        accounts: [],
      },
      total: 1,
      page: 1,
      page_size: 100,
    })
    listModelCandidates.mockResolvedValue({
      items: [
        {
          public_model: 'deepseek-v4',
          upstream_model: 'deepseek-v4-flash',
          provider_identity: 'openai-compatible',
          endpoint: 'chat_completions',
          account_id: 'account_1',
          account_name: 'YeToken',
          account_status: 'active',
          schedulable: true,
          runtime_eligible: true,
        },
        {
          public_model: 'deepseek-v4',
          upstream_model: 'deepseek-v4-flash',
          provider_identity: 'anthropic-api-key',
          endpoint: 'chat_completions',
          account_id: 'account_2',
          account_name: 'Claude route',
          account_status: 'inactive',
          schedulable: false,
          runtime_eligible: false,
          blockers: [{ code: 'account_inactive', message: 'inactive' }],
        },
      ],
    })

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.findAll('[data-testid="gateway-configure-model"]')).toHaveLength(1)
    expect(wrapper.findAll('tr')).toHaveLength(3)
    expect(wrapper.get('[data-testid="gateway-advanced-editor"]').attributes('style')).toContain('display: none')

    await wrapper.get('[data-testid="gateway-configure-model"]').trigger('click')

    expect(wrapper.get('[data-testid="gateway-unified-group"]').text()).toBe('Unified API')
    expect(wrapper.get('[data-testid="gateway-selected-model"]').text()).toBe('deepseek-v4')
    expect(wrapper.get('[data-testid="gateway-basic-pricing"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="gateway-advanced-editor"]').attributes('style')).toContain('display: none')
  })

  it('keeps simple pricing modes aligned with the selected billing unit', async () => {
    getOptions.mockResolvedValue({
      items: {
        access_groups: [{ id: 'group_1', name: 'Unified API' }],
        pricing_source_groups: [],
        accounts: [],
      },
      total: 1,
      page: 1,
      page_size: 100,
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="gateway-advanced-toggle"]').trigger('click')
    await wrapper.get('[data-testid="gateway-advanced-billing-mode"]').setValue('image')
    await wrapper.get('[data-testid="gateway-basic-pricing-method"]').setValue('flat_unit_price')

    expect(wrapper.find('[data-testid="gateway-basic-flat-price"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="gateway-basic-final-price"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="gateway-advanced-editor"]').attributes('style') || '').not.toContain('display: none')
  })

  it('automatically adds eligible routes as primary and backup lanes', async () => {
    getOptions.mockResolvedValue({
      items: {
        access_groups: [{ id: 'group_1', name: 'Unified API' }],
        pricing_source_groups: [],
        accounts: [],
      },
      total: 1,
      page: 1,
      page_size: 100,
    })
    listModelCandidates.mockResolvedValue({
      items: [
        {
          public_model: 'gpt-6-astra',
          upstream_model: 'gpt-6-astra',
          provider_identity: 'openai-chatgpt',
          endpoint: 'responses',
          account_id: 'account_1',
          account_name: 'OpenAI Pro',
          account_status: 'active',
          schedulable: true,
          runtime_eligible: true,
        },
        {
          public_model: 'gpt-6-astra',
          upstream_model: 'gpt-6-astra',
          provider_identity: 'openai-compatible',
          endpoint: 'responses',
          account_id: 'account_2',
          account_name: 'Fallback route',
          account_status: 'active',
          schedulable: true,
          runtime_eligible: true,
        },
      ],
    })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="gateway-configure-model"]').trigger('click')

    expect(wrapper.get('[data-testid="gateway-selected-route"]').text()).toContain('openai-chatgpt')
    expect(wrapper.get('[data-testid="gateway-backup-route-hint"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid^="gateway-basic-lane-"]')).toHaveLength(2)
    expect(wrapper.get('[data-testid="gateway-basic-provider-base-1"]').element.value).toBe('')
    await wrapper.get('[data-testid="gateway-advanced-toggle"]').trigger('click')
    expect(wrapper.findAll('[data-testid="gateway-advanced-billing-mode"]')).toHaveLength(2)
  })
})

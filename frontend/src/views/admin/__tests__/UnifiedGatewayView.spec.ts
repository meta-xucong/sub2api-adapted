import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import UnifiedGatewayView from '../UnifiedGatewayView.vue'

const api = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  getAccountBillingBreakdown: vi.fn(),
  searchApiKeys: vi.fn(),
}))

vi.mock('@/api/admin/accounts', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/admin/accounts')>(),
  list: api.listAccounts,
}))
vi.mock('@/api/admin/usage', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/admin/usage')>(),
  getAccountBillingBreakdown: api.getAccountBillingBreakdown,
  searchApiKeys: api.searchApiKeys,
}))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const AppLayoutStub = defineComponent({ template: '<div><slot /></div>' })

function mountPage() {
  return mount(UnifiedGatewayView, { global: { stubs: { AppLayout: AppLayoutStub } } })
}

async function loadSelectedKey() {
  const wrapper = mountPage()
  await wrapper.get('[data-testid="gateway-key-search"]').setValue('unified-customer-key')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  await wrapper.get('[data-testid="gateway-api-key"]').setValue('22')
  await wrapper.get('[data-testid="gateway-start-date"]').setValue('2026-10-01')
  await wrapper.get('[data-testid="gateway-end-date"]').setValue('2026-10-06')
  await wrapper.get('[data-testid="gateway-refresh"]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('Unified Gateway read-only usage dashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.searchApiKeys.mockResolvedValue([{ id: 22, name: 'unified-customer-key', user_id: 4 }])
    api.listAccounts.mockResolvedValue({
      items: [
        { id: 7, name: 'Line One' },
        { id: 8, name: 'Line Two' },
      ],
      total: 2,
      page: 1,
      page_size: 100,
      pages: 1,
    })
    api.getAccountBillingBreakdown.mockResolvedValue({
      accounts: [
        { account_id: 7, billing_type: 0, actual_cost: 1.25, account_cost: 0.8 },
        { account_id: 7, billing_type: 1, actual_cost: 0.3, account_cost: 0.1 },
        { account_id: 8, billing_type: 0, actual_cost: 3.5, account_cost: 1.5 },
        { account_id: 99, billing_type: 0, actual_cost: 2, account_cost: 1 },
      ],
    })
  })

  it('groups native balance and subscription amounts by actual account and calculates estimated spreads', async () => {
    const wrapper = await loadSelectedKey()

    expect(api.searchApiKeys).toHaveBeenCalledWith(undefined, 'unified-customer-key')
    expect(api.listAccounts).toHaveBeenCalledWith(1, 100, { lite: '1' })
    expect(api.getAccountBillingBreakdown).toHaveBeenCalledTimes(1)
    expect(api.getAccountBillingBreakdown).toHaveBeenCalledWith(expect.objectContaining({
      api_key_id: 22,
      start_date: '2026-10-01',
      end_date: '2026-10-06',
      timezone: expect.any(String),
    }))
    expect(wrapper.findAll('[data-testid="gateway-account-row"]')).toHaveLength(3)
    expect(wrapper.text()).toContain('Line One (#7)')
    expect(wrapper.text()).toContain('Line Two (#8)')
    expect(wrapper.text()).toContain('admin.unifiedGateway.unknownAccount (#99)')
    expect(wrapper.text()).toContain('1.25')
    expect(wrapper.text()).toContain('0.8')
    expect(wrapper.text()).toContain('0.45')
    expect(wrapper.text()).toContain('0.3')
    expect(wrapper.text()).toContain('0.1')
    expect(wrapper.text()).toContain('0.2')
    expect(wrapper.get('[data-testid="gateway-total-row"]').text()).toContain('6.75')
    expect(wrapper.get('[data-testid="gateway-total-row"]').text()).toContain('3.3')
    expect(wrapper.get('[data-testid="gateway-total-row"]').text()).toContain('3.45')
  })

  it('exports the same loaded rows and totals as a browser CSV without another request', async () => {
    api.listAccounts.mockResolvedValue({
      items: [
        { id: 7, name: '=1+1' },
        { id: 8, name: 'Line Two' },
      ],
      total: 2,
      page: 1,
      page_size: 100,
      pages: 1,
    })
    const wrapper = await loadSelectedKey()
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)

    await wrapper.get('[data-testid="gateway-export"]').trigger('click')

    expect(click).toHaveBeenCalledOnce()
    expect(api.getAccountBillingBreakdown).toHaveBeenCalledTimes(1)
    const link = click.mock.instances[0] as HTMLAnchorElement
    expect(link.download).toBe('unified-gateway-billing-22-2026-10-01-to-2026-10-06.csv')
    const encodedCsv = link.getAttribute('href')?.split(',')[1] || ''
    const csv = decodeURIComponent(encodedCsv)
    expect(csv).toContain("'=1+1 (#7),1.25,0.8,0.45,0.3,0.1,0.2")
    expect(csv).toContain('admin.unifiedGateway.total,6.75,3.3,3.45,0.3,0.1,0.2')

    click.mockRestore()
  })

  it('clears displayed rows and disables export when the selected date range changes', async () => {
    const wrapper = await loadSelectedKey()
    expect(wrapper.findAll('[data-testid="gateway-account-row"]')).toHaveLength(3)

    await wrapper.get('[data-testid="gateway-start-date"]').setValue('2026-10-02')
    await flushPromises()

    expect(wrapper.findAll('[data-testid="gateway-account-row"]')).toHaveLength(0)
    expect(wrapper.get('[data-testid="gateway-export"]').attributes('disabled')).toBeDefined()
    expect(api.getAccountBillingBreakdown).toHaveBeenCalledTimes(1)
  })

  it('does not request usage or create any total before an API key is selected', async () => {
    const wrapper = mountPage()
    expect(wrapper.get('[data-testid="gateway-refresh"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="gateway-export"]').attributes('disabled')).toBeDefined()
    expect(api.listAccounts).not.toHaveBeenCalled()
    expect(api.getAccountBillingBreakdown).not.toHaveBeenCalled()
  })

  it('keeps sub-micro USD amounts visible in the table and CSV', async () => {
    api.getAccountBillingBreakdown.mockResolvedValueOnce({
      accounts: [
        { account_id: 7, billing_type: 0, actual_cost: 0.0000001, account_cost: 0.00000001 },
      ],
    })
    const wrapper = await loadSelectedKey()

    expect(wrapper.text()).toContain('0.0000001')
    expect(wrapper.text()).toContain('0.00000001')
    expect(wrapper.text()).toContain('0.00000009')

    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    await wrapper.get('[data-testid="gateway-export"]').trigger('click')
    const link = click.mock.instances[0] as HTMLAnchorElement
    const csv = decodeURIComponent(link.getAttribute('href')?.split(',')[1] || '')
    expect(csv).toContain('0.0000001,0.00000001,0.00000009')
    click.mockRestore()
  })
})

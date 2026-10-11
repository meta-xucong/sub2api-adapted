import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import UnifiedGatewayView from '../UnifiedGatewayView.vue'
import zhUnifiedGateway from '@/i18n/locales/zh/admin/unifiedGateway'
import enUnifiedGateway from '@/i18n/locales/en/admin/unifiedGateway'

const api = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  getUnifiedGatewayUserBillingBreakdown: vi.fn(),
}))

vi.mock('@/api/admin/accounts', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/admin/accounts')>(),
  list: api.listAccounts,
}))
vi.mock('@/api/admin/usage', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/admin/usage')>(),
  getUnifiedGatewayUserBillingBreakdown: api.getUnifiedGatewayUserBillingBreakdown,
}))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const AppLayoutStub = defineComponent({ template: '<div><slot /></div>' })

async function mountPage() {
  const wrapper = mount(UnifiedGatewayView, { global: { stubs: { AppLayout: AppLayoutStub } } })
  await flushPromises()
  return wrapper
}

describe('Unified Gateway read-only usage dashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
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
    api.getUnifiedGatewayUserBillingBreakdown.mockResolvedValue({
      rows: [
        { user_id: 4, username: 'alice', email: 'alice@example.com', account_id: 7, billing_type: 0, actual_cost: 1.25, account_cost: 0.8 },
        { user_id: 4, username: 'alice', email: 'alice@example.com', account_id: 7, billing_type: 1, actual_cost: 0.3, account_cost: 0.1 },
        { user_id: 4, username: 'alice', email: 'alice@example.com', account_id: 8, billing_type: 0, actual_cost: 3.5, account_cost: 1.5 },
        { user_id: 5, username: 'bob', email: 'bob@example.com', account_id: 7, billing_type: 0, actual_cost: 2, account_cost: 1 },
        { user_id: 9, username: '', email: '', account_id: 8, billing_type: 1, actual_cost: 0.5, account_cost: 0.2 },
      ],
    })
  })

  it('labels the read-only ledger in the owner-defined star unit without converting values', () => {
    const zh = zhUnifiedGateway.unifiedGateway
    const en = enUnifiedGateway.unifiedGateway
    expect(zh.description).toContain('1 星 = 1 元')
    expect(zh.balanceCharge).toContain('星')
    expect(zh.subscriptionUsage).toContain('星')
    expect(en.description).toContain('1 star = CNY 1')
    expect(en.balanceCharge).toContain('stars')
    expect(en.subscriptionLineCost).toContain('stars')
  })

  it('loads all users once and groups native balance and subscription amounts by actual account', async () => {
    const wrapper = await mountPage()

    expect(api.getUnifiedGatewayUserBillingBreakdown).toHaveBeenCalledTimes(1)
    expect(api.getUnifiedGatewayUserBillingBreakdown).toHaveBeenCalledWith(expect.objectContaining({
      start_date: expect.any(String),
      end_date: expect.any(String),
      timezone: expect.any(String),
    }))
    expect(api.listAccounts).toHaveBeenCalledTimes(1)
    expect(wrapper.findAll('[data-testid^="gateway-user-row-"]')).toHaveLength(3)
    expect(wrapper.text()).toContain('alice')
    expect(wrapper.text()).toContain('bob')
    expect(wrapper.text()).toContain('#9')
    expect(wrapper.text()).toContain('Line One (#7)')
    expect(wrapper.text()).toContain('Line Two (#8)')
    expect(wrapper.get('[data-testid="gateway-total-row"]').text()).toContain('6.75')
    expect(wrapper.get('[data-testid="gateway-total-row"]').text()).toContain('3.3')
    expect(wrapper.get('[data-testid="gateway-total-row"]').text()).toContain('3.45')
  })

  it('keeps the usage report when optional account-name enrichment fails', async () => {
    api.listAccounts.mockRejectedValueOnce(new Error('account list unavailable'))

    const wrapper = await mountPage()

    expect(api.getUnifiedGatewayUserBillingBreakdown).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('alice')
    expect(wrapper.text()).toContain('admin.unifiedGateway.unknownAccount (#7)')
    expect(wrapper.get('[data-testid="gateway-total-row"]').text()).toContain('6.75')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('uses username, email, or user ID as a local filter without another request', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-testid="gateway-user-filter"]').setValue('BOB')

    expect(wrapper.findAll('[data-testid^="gateway-user-row-"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('bob')
    expect(wrapper.text()).not.toContain('alice@example.com')
    expect(wrapper.get('[data-testid="gateway-total-row"]').text()).toContain('2')
    expect(api.getUnifiedGatewayUserBillingBreakdown).toHaveBeenCalledTimes(1)
    expect(api.listAccounts).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-testid="gateway-user-filter"]').setValue('9')
    expect(wrapper.findAll('[data-testid^="gateway-user-row-"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('#9')
    expect(api.getUnifiedGatewayUserBillingBreakdown).toHaveBeenCalledTimes(1)
  })

  it('exports the currently filtered users and totals without another request', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-testid="gateway-user-filter"]').setValue('alice')
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)

    await wrapper.get('[data-testid="gateway-export"]').trigger('click')

    expect(click).toHaveBeenCalledOnce()
    expect(api.getUnifiedGatewayUserBillingBreakdown).toHaveBeenCalledTimes(1)
    const link = click.mock.instances[0] as HTMLAnchorElement
    expect(link.download).toContain('unified-gateway-billing-')
    const csv = decodeURIComponent(link.getAttribute('href')?.split(',')[1] || '')
    expect(csv).toContain('alice,alice@example.com,4,Line One (#7),1.25,0.8,0.45,0.3,0.1,0.2')
    expect(csv).toContain('admin.unifiedGateway.total,,,,')
    expect(csv).not.toContain('bob@example.com')

    click.mockRestore()
  })

  it('clears displayed rows when the date range changes and reloads on refresh', async () => {
    const wrapper = await mountPage()
    expect(wrapper.findAll('[data-testid^="gateway-user-row-"]')).toHaveLength(3)

    await wrapper.get('[data-testid="gateway-start-date"]').setValue('2026-10-02')
    await flushPromises()

    expect(wrapper.findAll('[data-testid^="gateway-user-row-"]')).toHaveLength(0)
    expect(api.getUnifiedGatewayUserBillingBreakdown).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-testid="gateway-refresh"]').trigger('click')
    await flushPromises()
    expect(api.getUnifiedGatewayUserBillingBreakdown).toHaveBeenCalledTimes(2)
  })

  it('does not expose API-key search or selection controls', async () => {
    const wrapper = await mountPage()
    expect(wrapper.find('[data-testid="gateway-key-search"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="gateway-api-key"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="gateway-user-filter"]').exists()).toBe(true)
  })
})

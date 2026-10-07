<template>
  <AppLayout>
    <div class="mx-auto max-w-7xl space-y-6 p-6">
      <header>
        <h1 class="text-2xl font-bold">{{ t('admin.unifiedGateway.title') }}</h1>
        <p class="mt-1 text-sm text-gray-500">{{ t('admin.unifiedGateway.description') }}</p>
      </header>

      <section class="card space-y-4 p-5">
        <form class="flex flex-wrap items-end gap-3" @submit.prevent="searchKeys">
          <label class="min-w-64 flex-1">
            <span class="mb-1 block text-sm">{{ t('admin.unifiedGateway.apiKeySearch') }}</span>
            <input v-model.trim="keyQuery" data-testid="gateway-key-search" class="input w-full" autocomplete="off" />
          </label>
          <button class="btn btn-secondary" type="submit" :disabled="searchingKeys">
            {{ t('admin.unifiedGateway.search') }}
          </button>
          <label class="min-w-56 flex-1">
            <span class="mb-1 block text-sm">{{ t('admin.unifiedGateway.selectApiKey') }}</span>
            <select v-model.number="selectedApiKeyId" data-testid="gateway-api-key" class="input w-full" :disabled="loading">
              <option :value="0">—</option>
              <option v-for="key in apiKeys" :key="key.id" :value="key.id">{{ key.name }} (#{{ key.id }})</option>
            </select>
          </label>
          <label>
            <span class="mb-1 block text-sm">{{ t('admin.unifiedGateway.startDate') }}</span>
            <input v-model="startDate" data-testid="gateway-start-date" class="input" type="date" :disabled="loading" />
          </label>
          <label>
            <span class="mb-1 block text-sm">{{ t('admin.unifiedGateway.endDate') }}</span>
            <input v-model="endDate" data-testid="gateway-end-date" class="input" type="date" :disabled="loading" />
          </label>
          <button data-testid="gateway-refresh" class="btn btn-primary" type="button" :disabled="loading || !selectedApiKeyId || !startDate || !endDate" @click="loadUsage">
            {{ t('admin.unifiedGateway.refresh') }}
          </button>
          <button data-testid="gateway-export" class="btn btn-secondary" type="button" :disabled="loading || !rows.length" @click="exportCsv">
            {{ t('admin.unifiedGateway.exportCsv') }}
          </button>
        </form>
        <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      </section>

      <section class="card overflow-hidden">
        <p v-if="loading" class="p-5 text-sm text-gray-500">{{ t('admin.unifiedGateway.loading') }}</p>
        <p v-else-if="!rows.length" class="p-5 text-sm text-gray-500">
          {{ selectedApiKeyId ? t('admin.unifiedGateway.noUsage') : t('admin.unifiedGateway.empty') }}
        </p>
        <div v-else class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead class="bg-gray-50 text-gray-600 dark:bg-gray-800 dark:text-gray-300">
              <tr>
                <th class="px-4 py-3 font-medium">{{ t('admin.unifiedGateway.account') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('admin.unifiedGateway.balanceCharge') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('admin.unifiedGateway.balanceLineCost') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('admin.unifiedGateway.balanceSpread') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('admin.unifiedGateway.subscriptionUsage') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('admin.unifiedGateway.subscriptionLineCost') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('admin.unifiedGateway.subscriptionSpread') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in rows" :key="row.accountId" data-testid="gateway-account-row" class="border-t border-gray-100 dark:border-gray-700">
                <td class="px-4 py-3">{{ row.accountName }} <span class="text-gray-500">(#{{ row.accountId }})</span></td>
                <td class="px-4 py-3 text-right tabular-nums">{{ formatAmount(row.balanceCharge) }}</td>
                <td class="px-4 py-3 text-right tabular-nums">{{ formatAmount(row.balanceLineCost) }}</td>
                <td class="px-4 py-3 text-right tabular-nums">{{ formatAmount(row.balanceSpread) }}</td>
                <td class="px-4 py-3 text-right tabular-nums">{{ formatAmount(row.subscriptionUsage) }}</td>
                <td class="px-4 py-3 text-right tabular-nums">{{ formatAmount(row.subscriptionLineCost) }}</td>
                <td class="px-4 py-3 text-right tabular-nums">{{ formatAmount(row.subscriptionSpread) }}</td>
              </tr>
            </tbody>
            <tfoot class="border-t-2 border-gray-200 bg-gray-50 font-semibold dark:border-gray-700 dark:bg-gray-800">
              <tr data-testid="gateway-total-row">
                <th class="px-4 py-3 text-left">{{ t('admin.unifiedGateway.total') }}</th>
                <td class="px-4 py-3 text-right tabular-nums">{{ formatAmount(totals.balanceCharge) }}</td>
                <td class="px-4 py-3 text-right tabular-nums">{{ formatAmount(totals.balanceLineCost) }}</td>
                <td class="px-4 py-3 text-right tabular-nums">{{ formatAmount(totals.balanceSpread) }}</td>
                <td class="px-4 py-3 text-right tabular-nums">{{ formatAmount(totals.subscriptionUsage) }}</td>
                <td class="px-4 py-3 text-right tabular-nums">{{ formatAmount(totals.subscriptionLineCost) }}</td>
                <td class="px-4 py-3 text-right tabular-nums">{{ formatAmount(totals.subscriptionSpread) }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { list as listAccounts } from '@/api/admin/accounts'
import { getAccountBillingBreakdown, searchApiKeys } from '@/api/admin/usage'
import type { SimpleApiKey } from '@/api/admin/usage'
import type { AccountListItem } from '@/types'

interface GatewayBillingRow {
  accountId: number
  accountName: string
  balanceCharge: number
  balanceLineCost: number
  balanceSpread: number
  subscriptionUsage: number
  subscriptionLineCost: number
  subscriptionSpread: number
}

interface GatewayBillingTotals {
  balanceCharge: number
  balanceLineCost: number
  balanceSpread: number
  subscriptionUsage: number
  subscriptionLineCost: number
  subscriptionSpread: number
}

const { t } = useI18n()
const keyQuery = ref('')
const apiKeys = ref<SimpleApiKey[]>([])
const selectedApiKeyId = ref(0)
const startDate = ref(dateInputValue(new Date(Date.now() - 29 * 24 * 60 * 60 * 1000)))
const endDate = ref(dateInputValue(new Date()))
const rows = ref<GatewayBillingRow[]>([])
const loading = ref(false)
const searchingKeys = ref(false)
const error = ref('')
watch([selectedApiKeyId, startDate, endDate], () => {
  rows.value = []
  error.value = ''
})
const totals = computed<GatewayBillingTotals>(() => rows.value.reduce((sum, row) => ({
  balanceCharge: sum.balanceCharge + row.balanceCharge,
  balanceLineCost: sum.balanceLineCost + row.balanceLineCost,
  balanceSpread: sum.balanceSpread + row.balanceSpread,
  subscriptionUsage: sum.subscriptionUsage + row.subscriptionUsage,
  subscriptionLineCost: sum.subscriptionLineCost + row.subscriptionLineCost,
  subscriptionSpread: sum.subscriptionSpread + row.subscriptionSpread,
}), {
  balanceCharge: 0,
  balanceLineCost: 0,
  balanceSpread: 0,
  subscriptionUsage: 0,
  subscriptionLineCost: 0,
  subscriptionSpread: 0,
}))

function dateInputValue(date: Date): string {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return year + '-' + month + '-' + day
}

function formatAmount(value: number): string {
	return value.toFixed(10).replace(/\.?0+$/, '')
}

async function searchKeys() {
  searchingKeys.value = true
  error.value = ''
  try {
    apiKeys.value = await searchApiKeys(undefined, keyQuery.value)
  } catch {
    error.value = t('admin.unifiedGateway.searchFailed')
  } finally {
    searchingKeys.value = false
  }
}

async function loadUsage() {
  if (!selectedApiKeyId.value || !startDate.value || !endDate.value) return
  loading.value = true
  error.value = ''
  try {
    const firstPage = await listAccounts(1, 100, { lite: '1' })
    const accounts: AccountListItem[] = [...firstPage.items]
    for (let page = 2; page <= firstPage.pages; page += 1) {
      const result = await listAccounts(page, 100, { lite: '1' })
      accounts.push(...result.items)
    }

    const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
    const breakdown = await getAccountBillingBreakdown({
      api_key_id: selectedApiKeyId.value,
      start_date: startDate.value,
      end_date: endDate.value,
      timezone,
    })
    const accountNames = new Map(accounts.map((account) => [account.id, account.name]))
    const grouped = new Map<number, GatewayBillingRow>()

    for (const item of breakdown.accounts) {
      if (item.billing_type !== 0 && item.billing_type !== 1) continue
      let row = grouped.get(item.account_id)
      if (!row) {
        row = {
          accountId: item.account_id,
          accountName: accountNames.get(item.account_id) || t('admin.unifiedGateway.unknownAccount'),
          balanceCharge: 0,
          balanceLineCost: 0,
          balanceSpread: 0,
          subscriptionUsage: 0,
          subscriptionLineCost: 0,
          subscriptionSpread: 0,
        }
        grouped.set(item.account_id, row)
      }

      if (item.billing_type === 0) {
        row.balanceCharge += item.actual_cost
        row.balanceLineCost += item.account_cost
      } else {
        row.subscriptionUsage += item.actual_cost
        row.subscriptionLineCost += item.account_cost
      }
    }

    rows.value = [...grouped.values()].map((row) => ({
      ...row,
      balanceSpread: row.balanceCharge - row.balanceLineCost,
      subscriptionSpread: row.subscriptionUsage - row.subscriptionLineCost,
    }))
  } catch {
    rows.value = []
    error.value = t('admin.unifiedGateway.requestFailed')
  } finally {
    loading.value = false
  }
}

function exportCsv() {
  if (!rows.value.length || !selectedApiKeyId.value) return
  const headers = [
    t('admin.unifiedGateway.account'),
    t('admin.unifiedGateway.balanceCharge'),
    t('admin.unifiedGateway.balanceLineCost'),
    t('admin.unifiedGateway.balanceSpread'),
    t('admin.unifiedGateway.subscriptionUsage'),
    t('admin.unifiedGateway.subscriptionLineCost'),
    t('admin.unifiedGateway.subscriptionSpread'),
  ]
  const csvCell = (value: string | number) => {
    let text = typeof value === 'number' ? formatAmount(value) : value
    if (typeof value === 'string' && /^[\s]*[=+\-@]/.test(text)) {
      text = "'" + text
    }
    return /[",\r\n]/.test(text) ? '"' + text.replace(/"/g, '""') + '"' : text
  }
  const csvRows = [
    headers,
    ...rows.value.map((row) => [
      row.accountName + ' (#' + row.accountId + ')',
      row.balanceCharge,
      row.balanceLineCost,
      row.balanceSpread,
      row.subscriptionUsage,
      row.subscriptionLineCost,
      row.subscriptionSpread,
    ]),
    [
      t('admin.unifiedGateway.total'),
      totals.value.balanceCharge,
      totals.value.balanceLineCost,
      totals.value.balanceSpread,
      totals.value.subscriptionUsage,
      totals.value.subscriptionLineCost,
      totals.value.subscriptionSpread,
    ],
  ]
  const content = String.fromCharCode(0xFEFF) + csvRows.map((row) => row.map(csvCell).join(',')).join('\r\n')
  const link = document.createElement('a')
  link.href = 'data:text/csv;charset=utf-8,' + encodeURIComponent(content)
  link.download = 'unified-gateway-billing-' + selectedApiKeyId.value + '-' + startDate.value + '-to-' + endDate.value + '.csv'
  document.body.appendChild(link)
  link.click()
  link.remove()
}
</script>

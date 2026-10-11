<template>
  <AppLayout>
    <div class="mx-auto max-w-7xl space-y-6 p-6">
      <header>
        <h1 class="text-2xl font-bold">{{ t('admin.unifiedGateway.title') }}</h1>
        <p class="mt-1 text-sm text-gray-500">{{ t('admin.unifiedGateway.description') }}</p>
      </header>

      <section class="card space-y-4 p-5">
        <form class="flex flex-wrap items-end gap-3" @submit.prevent>
          <label class="min-w-64 flex-1">
            <span class="mb-1 block text-sm">{{ t('admin.unifiedGateway.usernameFilter') }}</span>
            <input v-model.trim="userQuery" data-testid="gateway-user-filter" class="input w-full" type="search" autocomplete="off" />
          </label>
          <label>
            <span class="mb-1 block text-sm">{{ t('admin.unifiedGateway.startDate') }}</span>
            <input v-model="startDate" data-testid="gateway-start-date" class="input" type="date" :disabled="loading" />
          </label>
          <label>
            <span class="mb-1 block text-sm">{{ t('admin.unifiedGateway.endDate') }}</span>
            <input v-model="endDate" data-testid="gateway-end-date" class="input" type="date" :disabled="loading" />
          </label>
          <button data-testid="gateway-refresh" class="btn btn-primary" type="button" :disabled="loading || !startDate || !endDate" @click="loadUsage">
            {{ t('admin.unifiedGateway.refresh') }}
          </button>
          <button data-testid="gateway-export" class="btn btn-secondary" type="button" :disabled="loading || !filteredUsers.length" @click="exportCsv">
            {{ t('admin.unifiedGateway.exportCsv') }}
          </button>
        </form>
        <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      </section>

      <section class="card overflow-hidden">
        <p v-if="loading" class="p-5 text-sm text-gray-500">{{ t('admin.unifiedGateway.loading') }}</p>
        <p v-else-if="!filteredUsers.length" class="p-5 text-sm text-gray-500">
          {{ userQuery ? t('admin.unifiedGateway.noFilteredUsers') : t('admin.unifiedGateway.noUsage') }}
        </p>
        <div v-else class="overflow-x-auto">
          <table class="w-full min-w-[1050px] text-left text-sm">
            <thead class="bg-gray-50 text-gray-600 dark:bg-gray-800 dark:text-gray-300">
              <tr>
                <th class="px-4 py-3 font-medium">{{ t('admin.unifiedGateway.user') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('admin.unifiedGateway.balanceCharge') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('admin.unifiedGateway.balanceLineCost') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('admin.unifiedGateway.balanceSpread') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('admin.unifiedGateway.subscriptionUsage') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('admin.unifiedGateway.subscriptionLineCost') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('admin.unifiedGateway.subscriptionSpread') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="user in filteredUsers" :key="user.userId" class="border-t border-gray-100 dark:border-gray-700">
                <td colspan="7" class="p-0">
                  <details :data-testid="'gateway-user-row-' + user.userId">
                    <summary class="grid cursor-pointer grid-cols-7 items-center gap-3 px-4 py-3">
                      <span class="min-w-0">
                        <span class="block truncate font-medium">{{ user.displayName }}</span>
                        <span class="block truncate text-xs text-gray-500">{{ user.email || ('#' + user.userId) }}</span>
                      </span>
                      <span class="text-right tabular-nums">{{ formatAmount(user.balanceCharge) }}</span>
                      <span class="text-right tabular-nums">{{ formatAmount(user.balanceLineCost) }}</span>
                      <span class="text-right tabular-nums">{{ formatAmount(user.balanceSpread) }}</span>
                      <span class="text-right tabular-nums">{{ formatAmount(user.subscriptionUsage) }}</span>
                      <span class="text-right tabular-nums">{{ formatAmount(user.subscriptionLineCost) }}</span>
                      <span class="text-right tabular-nums">{{ formatAmount(user.subscriptionSpread) }}</span>
                    </summary>
                    <div class="overflow-x-auto border-t border-gray-100 px-4 py-3 dark:border-gray-700">
                      <table class="w-full min-w-[850px] text-left text-xs">
                        <thead class="text-gray-500">
                          <tr>
                            <th class="py-2 font-medium">{{ t('admin.unifiedGateway.account') }}</th>
                            <th class="py-2 text-right font-medium">{{ t('admin.unifiedGateway.balanceCharge') }}</th>
                            <th class="py-2 text-right font-medium">{{ t('admin.unifiedGateway.balanceLineCost') }}</th>
                            <th class="py-2 text-right font-medium">{{ t('admin.unifiedGateway.balanceSpread') }}</th>
                            <th class="py-2 text-right font-medium">{{ t('admin.unifiedGateway.subscriptionUsage') }}</th>
                            <th class="py-2 text-right font-medium">{{ t('admin.unifiedGateway.subscriptionLineCost') }}</th>
                            <th class="py-2 text-right font-medium">{{ t('admin.unifiedGateway.subscriptionSpread') }}</th>
                          </tr>
                        </thead>
                        <tbody>
                          <tr v-for="line in user.lines" :key="line.accountId" data-testid="gateway-account-row" class="border-t border-gray-100 dark:border-gray-700">
                            <td class="py-2">{{ line.accountName }} <span class="text-gray-500">(#{{ line.accountId }})</span></td>
                            <td class="py-2 text-right tabular-nums">{{ formatAmount(line.balanceCharge) }}</td>
                            <td class="py-2 text-right tabular-nums">{{ formatAmount(line.balanceLineCost) }}</td>
                            <td class="py-2 text-right tabular-nums">{{ formatAmount(line.balanceSpread) }}</td>
                            <td class="py-2 text-right tabular-nums">{{ formatAmount(line.subscriptionUsage) }}</td>
                            <td class="py-2 text-right tabular-nums">{{ formatAmount(line.subscriptionLineCost) }}</td>
                            <td class="py-2 text-right tabular-nums">{{ formatAmount(line.subscriptionSpread) }}</td>
                          </tr>
                        </tbody>
                      </table>
                    </div>
                  </details>
                </td>
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
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { list as listAccounts } from '@/api/admin/accounts'
import { getUnifiedGatewayUserBillingBreakdown } from '@/api/admin/usage'
import type { AccountListItem } from '@/types'

interface GatewayBillingLine {
  accountId: number
  accountName: string
  balanceCharge: number
  balanceLineCost: number
  balanceSpread: number
  subscriptionUsage: number
  subscriptionLineCost: number
  subscriptionSpread: number
}

interface GatewayUserSummary extends GatewayBillingLine {
  userId: number
  username: string
  email: string
  displayName: string
  lines: GatewayBillingLine[]
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
const userQuery = ref('')
const startDate = ref(dateInputValue(new Date(Date.now() - 29 * 24 * 60 * 60 * 1000)))
const endDate = ref(dateInputValue(new Date()))
const users = ref<GatewayUserSummary[]>([])
const loading = ref(false)
const error = ref('')
watch([startDate, endDate], () => {
  users.value = []
  error.value = ''
})
const filteredUsers = computed(() => {
  const query = userQuery.value.trim().toLocaleLowerCase()
  if (!query) return users.value
  return users.value.filter((user) =>
    [user.username, user.email, user.userId, user.displayName].join(' ').toLocaleLowerCase().includes(query)
  )
})
const totals = computed<GatewayBillingTotals>(() => filteredUsers.value.reduce((sum, user) => ({
  balanceCharge: sum.balanceCharge + user.balanceCharge,
  balanceLineCost: sum.balanceLineCost + user.balanceLineCost,
  balanceSpread: sum.balanceSpread + user.balanceSpread,
  subscriptionUsage: sum.subscriptionUsage + user.subscriptionUsage,
  subscriptionLineCost: sum.subscriptionLineCost + user.subscriptionLineCost,
  subscriptionSpread: sum.subscriptionSpread + user.subscriptionSpread,
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

function emptyLine(accountId: number, accountName: string): GatewayBillingLine {
  return {
    accountId,
    accountName,
    balanceCharge: 0,
    balanceLineCost: 0,
    balanceSpread: 0,
    subscriptionUsage: 0,
    subscriptionLineCost: 0,
    subscriptionSpread: 0,
  }
}

function formatAmount(value: number): string {
  return value.toFixed(10).replace(/\.?0+$/, '')
}

async function loadUsage() {
  if (!startDate.value || !endDate.value) return
  loading.value = true
  users.value = []
  error.value = ''
  try {
    const accountNames = new Map<number, string>()
    try {
      const firstPage = await listAccounts(1, 100, { lite: '1' })
      const accounts: AccountListItem[] = [...firstPage.items]
      for (let page = 2; page <= firstPage.pages; page += 1) {
        const result = await listAccounts(page, 100, { lite: '1' })
        accounts.push(...result.items)
      }
      for (const account of accounts) accountNames.set(account.id, account.name)
    } catch {
      // Account names are optional enrichment; keep the usage report authoritative.
    }

    const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
    const response = await getUnifiedGatewayUserBillingBreakdown({
      start_date: startDate.value,
      end_date: endDate.value,
      timezone,
    })
    const byUser = new Map<number, GatewayUserSummary>()

    for (const item of response.rows) {
      if (item.billing_type !== 0 && item.billing_type !== 1) continue
      let user = byUser.get(item.user_id)
      if (!user) {
        const username = item.username || ''
        const email = item.email || ''
        user = {
          ...emptyLine(0, ''),
          userId: item.user_id,
          username,
          email,
          displayName: username || email || ('#' + item.user_id),
          lines: [],
        }
        byUser.set(item.user_id, user)
      }
      let line = user.lines.find((candidate) => candidate.accountId === item.account_id)
      if (!line) {
        line = emptyLine(item.account_id, accountNames.get(item.account_id) || t('admin.unifiedGateway.unknownAccount'))
        user.lines.push(line)
      }
      if (item.billing_type === 0) {
        line.balanceCharge += item.actual_cost
        line.balanceLineCost += item.account_cost
      } else {
        line.subscriptionUsage += item.actual_cost
        line.subscriptionLineCost += item.account_cost
      }
    }

    users.value = [...byUser.values()].map((user) => {
      for (const line of user.lines) {
        line.balanceSpread = line.balanceCharge - line.balanceLineCost
        line.subscriptionSpread = line.subscriptionUsage - line.subscriptionLineCost
        user.balanceCharge += line.balanceCharge
        user.balanceLineCost += line.balanceLineCost
        user.subscriptionUsage += line.subscriptionUsage
        user.subscriptionLineCost += line.subscriptionLineCost
      }
      user.balanceSpread = user.balanceCharge - user.balanceLineCost
      user.subscriptionSpread = user.subscriptionUsage - user.subscriptionLineCost
      user.lines.sort((a, b) => a.accountId - b.accountId)
      return user
    }).sort((a, b) => a.displayName.localeCompare(b.displayName) || a.userId - b.userId)
  } catch {
    users.value = []
    error.value = t('admin.unifiedGateway.requestFailed')
  } finally {
    loading.value = false
  }
}

function exportCsv() {
  if (!filteredUsers.value.length) return
  const headers = [
    t('admin.unifiedGateway.user'),
    t('admin.unifiedGateway.userEmail'),
    t('admin.unifiedGateway.userId'),
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
    if (typeof value === 'string' && /^[\s]*[=+\-@]/.test(text)) text = "'" + text
    return /[",\r\n]/.test(text) ? '"' + text.replace(/"/g, '""') + '"' : text
  }
  const csvRows: Array<Array<string | number>> = [
    headers,
    ...filteredUsers.value.flatMap((user) => user.lines.map((line) => [
      user.displayName,
      user.email,
      user.userId,
      line.accountName + ' (#' + line.accountId + ')',
      line.balanceCharge,
      line.balanceLineCost,
      line.balanceSpread,
      line.subscriptionUsage,
      line.subscriptionLineCost,
      line.subscriptionSpread,
    ])),
    [
      t('admin.unifiedGateway.total'),
      '',
      '',
      '',
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
  link.download = 'unified-gateway-billing-' + startDate.value + '-to-' + endDate.value + '.csv'
  document.body.appendChild(link)
  link.click()
  link.remove()
}

onMounted(() => {
  void loadUsage()
})
</script>

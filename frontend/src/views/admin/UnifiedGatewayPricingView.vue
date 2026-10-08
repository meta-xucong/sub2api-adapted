<template>
  <AppLayout>
    <div class="mx-auto max-w-7xl space-y-6 p-6">
      <header>
        <h1 class="text-2xl font-bold">{{ t('admin.unifiedGateway.pricingTitle') }}</h1>
        <p class="mt-1 text-sm text-gray-500">{{ t('admin.unifiedGateway.pricingDescription') }}</p>
      </header>

      <section class="card space-y-4 p-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="text-sm text-gray-600 dark:text-gray-300">
            <span>{{ t('admin.unifiedGateway.pricingGroup') }}:</span>
            <strong class="ml-1">{{ groupName || `#${form.target_group_id || '—'}` }}</strong>
          </div>
          <div class="flex flex-wrap items-center gap-3 text-sm">
            <span>{{ t('admin.unifiedGateway.pricingSavedRevision', { revision: state?.saved.revision ?? 0 }) }}</span>
            <span>{{ t('admin.unifiedGateway.pricingActiveRevision', { revision: state?.active_revision ?? 0 }) }}</span>
            <span v-if="state?.restart_needed" class="rounded bg-amber-100 px-2 py-1 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200">
              {{ t('admin.unifiedGateway.pricingRestartRequired') }}
            </span>
          </div>
        </div>
        <p class="text-sm text-gray-500">{{ t('admin.unifiedGateway.pricingFallback') }}</p>
        <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
        <p v-if="success" role="status" class="text-sm text-green-700">{{ success }}</p>
      </section>

      <section class="card overflow-hidden">
        <p v-if="loading" class="p-5 text-sm text-gray-500">{{ t('admin.unifiedGateway.pricingLoading') }}</p>
        <div v-else-if="!state" class="flex justify-end p-5">
          <button class="btn btn-secondary" type="button" data-testid="retry-route-pricing" :disabled="loading" @click="load">
            {{ t('admin.unifiedGateway.pricingRetry') }}
          </button>
        </div>
        <div v-else-if="!state.groups.length" class="p-5 text-sm text-amber-700">
          {{ t('admin.unifiedGateway.pricingNoCompositeGroup') }}
        </div>
        <div v-else class="space-y-4 p-5">
          <div class="flex justify-end">
            <button class="btn btn-secondary" type="button" data-testid="add-route-pricing" :disabled="saving" @click="addEntry">
              {{ t('admin.unifiedGateway.pricingAdd') }}
            </button>
          </div>
          <div v-if="form.entries.length" class="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
            <label class="block w-full sm:max-w-xl">
              <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingSearchLabel') }}</span>
              <div class="flex gap-2">
                <input
                  v-model.trim="searchQuery"
                  data-testid="route-pricing-search"
                  class="input w-full"
                  type="search"
                  :placeholder="t('admin.unifiedGateway.pricingSearchPlaceholder')"
                  :disabled="saving"
                />
                <button v-if="searchQuery" class="btn btn-secondary shrink-0" type="button" data-testid="clear-route-pricing-search" :disabled="saving" @click="searchQuery = ''">
                  {{ t('admin.unifiedGateway.pricingSearchClear') }}
                </button>
              </div>
            </label>
            <span class="text-xs text-gray-500" data-testid="route-pricing-search-count">
              {{ t('admin.unifiedGateway.pricingSearchSummary', { visible: filteredEntries.length, total: form.entries.length }) }}
            </span>
          </div>
          <div v-if="!form.entries.length" class="rounded border border-dashed border-gray-300 p-6 text-center text-sm text-gray-500 dark:border-gray-700">
            {{ t('admin.unifiedGateway.pricingEmpty') }}
          </div>
          <div v-else-if="!filteredEntries.length" class="rounded border border-dashed border-gray-300 p-6 text-center text-sm text-gray-500 dark:border-gray-700">
            {{ t('admin.unifiedGateway.pricingSearchNoResults') }}
          </div>
          <div v-for="({ entry, index }) in filteredEntries" :key="index" data-testid="route-pricing-row" class="rounded-lg border border-gray-200 p-4 dark:border-gray-700">
            <div class="grid grid-cols-1 gap-3 md:grid-cols-12">
              <label class="md:col-span-3">
                <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingAccount') }}</span>
                <select v-model.number="entry.account_id" data-testid="route-pricing-account" class="input w-full" :disabled="saving">
                  <option :value="0">—</option>
                  <option v-for="account in state.accounts" :key="account.id" :value="account.id">{{ account.name }} (#{{ account.id }})</option>
                </select>
              </label>
              <label class="md:col-span-3">
                <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingModel') }}</span>
                <input v-model.trim="entry.model" data-testid="route-pricing-model" class="input w-full" autocomplete="off" :disabled="saving" />
                <span class="mt-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingModelHint') }}</span>
              </label>
              <label class="md:col-span-2">
                <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingType') }}</span>
                <select v-model="entry.kind" data-testid="route-pricing-kind" class="input w-full" :disabled="saving" @change="changeKind(entry)">
                  <option value="token">{{ t('admin.unifiedGateway.pricingToken') }}</option>
                  <option value="image">{{ t('admin.unifiedGateway.pricingImage') }}</option>
                  <option value="video">{{ t('admin.unifiedGateway.pricingVideo') }}</option>
                </select>
              </label>
              <label v-if="entry.kind === 'token'" class="md:col-span-2">
                <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingMultiplier') }}</span>
                <input v-model.number="entry.multiplier" data-testid="route-pricing-multiplier" class="input w-full" type="number" min="0" step="0.01" :disabled="saving" />
              </label>
              <template v-else-if="entry.kind === 'image'">
                <label class="md:col-span-2">
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingImageSize') }}</span>
                  <select v-model="entry.image_size" data-testid="route-pricing-image-size" class="input w-full" :disabled="saving">
                    <option value="1K">1K</option>
                    <option value="2K">2K</option>
                    <option value="4K">4K</option>
                  </select>
                </label>
                <label class="md:col-span-2">
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingImageQuality') }}</span>
                  <input v-model.trim="entry.image_quality" data-testid="route-pricing-image-quality" class="input w-full" placeholder="high" :disabled="saving" />
                </label>
                <label class="md:col-span-2">
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingUnitPrice') }}</span>
                  <input v-model.number="entry.unit_price" data-testid="route-pricing-unit-price" class="input w-full" type="number" min="0" step="0.001" :disabled="saving" />
                </label>
              </template>
              <template v-else>
                <label class="md:col-span-2">
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingVideoResolution') }}</span>
                  <input v-model.trim="entry.video_resolution" data-testid="route-pricing-video-resolution" class="input w-full" placeholder="720p" :disabled="saving" />
                </label>
                <label class="md:col-span-2">
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingVideoDuration') }}</span>
                  <input v-model.number="entry.video_duration_seconds" data-testid="route-pricing-video-duration" class="input w-full" type="number" min="1" step="1" :disabled="saving" />
                </label>
                <label class="md:col-span-2">
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingUnitPrice') }}</span>
                  <input v-model.number="entry.unit_price" data-testid="route-pricing-video-unit-price" class="input w-full" type="number" min="0" step="0.001" :disabled="saving" />
                </label>
              </template>
              <div class="flex items-end justify-end md:col-span-1">
                <button class="btn btn-secondary text-red-600" type="button" :aria-label="t('admin.unifiedGateway.pricingRemove')" :disabled="saving" @click="removeEntry(index)">
                  {{ t('admin.unifiedGateway.pricingRemove') }}
                </button>
              </div>
            </div>
            <p v-if="entry.kind === 'token'" class="mt-3 text-xs text-gray-500" data-testid="route-pricing-source">
              {{ t('admin.unifiedGateway.pricingConfiguredSource', { source: configuredSource(entry) }) }}
            </p>
            <div v-if="entry.kind === 'token'" class="mt-4 border-t border-gray-200 pt-4 dark:border-gray-700">
              <div class="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <p class="text-sm font-medium">{{ t('admin.unifiedGateway.pricingBasePriceTitle') }}</p>
                  <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBasePricePrecedence') }}</p>
                  <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBasePriceUnit') }}</p>
                </div>
                <button
                  v-if="!entry.token_base_price"
                  class="btn btn-secondary"
                  type="button"
                  data-testid="add-route-base-price"
                  :disabled="saving"
                  @click="addBasePriceCard(entry)"
                >
                  {{ t('admin.unifiedGateway.pricingBasePriceAdd') }}
                </button>
                <button
                  v-else
                  class="btn btn-secondary text-red-600"
                  type="button"
                  data-testid="remove-route-base-price"
                  :disabled="saving"
                  @click="removeBasePriceCard(entry)"
                >
                  {{ t('admin.unifiedGateway.pricingBasePriceRemove') }}
                </button>
              </div>
              <div v-if="entry.token_base_price" class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
                <label>
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseInput') }}</span>
                  <input v-model.number="entry.token_base_price.input_per_million" data-testid="route-base-input" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                </label>
                <label>
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseOutput') }}</span>
                  <input v-model.number="entry.token_base_price.output_per_million" data-testid="route-base-output" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                </label>
                <label>
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheRead') }}</span>
                  <input v-model.number="entry.token_base_price.cache_read_per_million" data-testid="route-base-cache-read" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                </label>
                <label>
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheWrite') }}</span>
                  <input v-model.number="entry.token_base_price.cache_write_per_million" data-testid="route-base-cache-write" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                </label>
                <label>
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheWrite5m') }}</span>
                  <input v-model.number="entry.token_base_price.cache_write_5m_per_million" data-testid="route-base-cache-write-5m" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                </label>
                <label>
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheWrite1h') }}</span>
                  <input v-model.number="entry.token_base_price.cache_write_1h_per_million" data-testid="route-base-cache-write-1h" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                  <span class="mt-1 block text-xs text-amber-700 dark:text-amber-300">{{ t('admin.unifiedGateway.pricingBaseCacheWrite1hZeroUnsupported') }}</span>
                </label>
              </div>
            </div>
          </div>
          <div class="flex justify-end">
            <button class="btn btn-primary" type="button" data-testid="save-route-pricing" :disabled="saving || loading || !form.target_group_id" @click="save">
              {{ saving ? t('admin.unifiedGateway.pricingSaving') : t('admin.unifiedGateway.pricingSave') }}
            </button>
          </div>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import {
  getUnifiedGatewayRoutePricing,
  updateUnifiedGatewayRoutePricing,
  type RoutePricingEntry,
  type RoutePricingKind,
  type RouteTokenBasePrice,
  type UnifiedGatewayRoutePricingState
} from '@/api/admin/unifiedGatewayRoutePricing'

const { t } = useI18n()
const state = ref<UnifiedGatewayRoutePricingState | null>(null)
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const success = ref('')
const searchQuery = ref('')
type RouteTokenBasePriceForm = { [K in keyof RouteTokenBasePrice]: number | '' | undefined }
type RoutePricingFormEntry = Omit<RoutePricingEntry, 'multiplier' | 'token_base_price'> & {
  multiplier?: number | ''
  token_base_price?: RouteTokenBasePriceForm
}
const form = reactive({ target_group_id: 0, entries: [] as RoutePricingFormEntry[] })

const groupName = computed(() => state.value?.groups.find((group) => group.id === form.target_group_id)?.name || '')
const filteredEntries = computed(() => {
  const query = searchQuery.value.trim().toLocaleLowerCase()
  return form.entries.flatMap((entry, index) => {
    const account = state.value?.accounts.find((candidate) => candidate.id === entry.account_id)
    const kindKey = `admin.unifiedGateway.pricing${entry.kind[0].toUpperCase()}${entry.kind.slice(1)}`
    const searchText = [account?.name || '', entry.account_id, entry.model, t(kindKey)].join(' ').toLocaleLowerCase()
    return !query || searchText.includes(query) ? [{ entry, index }] : []
  })
})

function configuredSource(entry: RoutePricingFormEntry): string {
  if (entry.token_base_price) return t('admin.unifiedGateway.pricingSourceBasePrice')
  if (typeof entry.multiplier === 'number') return t('admin.unifiedGateway.pricingSourceLegacyMultiplier')
  return t('admin.unifiedGateway.pricingSourceNative')
}

function cloneEntries(entries: RoutePricingEntry[]): RoutePricingFormEntry[] {
  return entries.map((entry) => ({
    ...entry,
    token_base_price: entry.token_base_price ? { ...entry.token_base_price } : undefined
  }))
}

function newEntry(kind: RoutePricingKind = 'token'): RoutePricingFormEntry {
  const entry: RoutePricingFormEntry = { account_id: state.value?.accounts[0]?.id || 0, model: '', kind }
  if (kind === 'token') entry.multiplier = 1
  if (kind === 'image') Object.assign(entry, { image_size: '', image_quality: '', unit_price: 0 })
  if (kind === 'video') Object.assign(entry, { video_resolution: '', video_duration_seconds: 0, unit_price: 0 })
  return entry
}

function addEntry() {
  searchQuery.value = ''
  form.entries.push(newEntry())
  success.value = ''
}

function changeKind(entry: RoutePricingFormEntry) {
  delete entry.multiplier
  delete entry.token_base_price
  delete entry.unit_price
  delete entry.image_size
  delete entry.image_quality
  delete entry.video_resolution
  delete entry.video_duration_seconds
  if (entry.kind === 'token') entry.multiplier = 1
  if (entry.kind === 'image') Object.assign(entry, { image_size: '', image_quality: '', unit_price: 0 })
  if (entry.kind === 'video') Object.assign(entry, { video_resolution: '', video_duration_seconds: 0, unit_price: 0 })
}

function addBasePriceCard(entry: RoutePricingFormEntry) {
  entry.token_base_price = {
    input_per_million: '',
    output_per_million: '',
    cache_read_per_million: '',
    cache_write_per_million: '',
    cache_write_5m_per_million: '',
    cache_write_1h_per_million: ''
  }
  success.value = ''
}

function removeBasePriceCard(entry: RoutePricingFormEntry) {
  delete entry.token_base_price
  success.value = ''
}

function removeEntry(index: number) {
  form.entries.splice(index, 1)
  success.value = ''
}

function normalizedImageTier(value: string | undefined): string {
  const normalized = (value || '').trim().toLowerCase()
  if (['1k', '2k', '4k'].includes(normalized)) return normalized
  const dimensions = normalized.split('x')
  if (dimensions.length !== 2) return normalized
  const width = Number(dimensions[0].trim())
  const height = Number(dimensions[1].trim())
  if (!Number.isInteger(width) || !Number.isInteger(height) || width <= 0 || height <= 0) return normalized
  const maxEdge = Math.max(width, height)
  if (maxEdge <= 1024) return '1k'
  if (maxEdge <= 2048) return '2k'
  return '4k'
}

function normalizedVideoResolution(value: string | undefined): string {
  const normalized = (value || '').trim().toLowerCase()
  if (['480', '480p', 'sd'].includes(normalized)) return '480p'
  if (['720', '720p', 'hd'].includes(normalized)) return '720p'
  if (['1080', '1080p', 'full_hd', 'full-hd', 'fhd'].includes(normalized)) return '1080p'
  return normalized
}

function routePricingKey(entry: RoutePricingFormEntry): string {
  return JSON.stringify([
    form.target_group_id,
    entry.account_id,
    entry.model.trim(),
    entry.kind,
    entry.kind === 'image' ? normalizedImageTier(entry.image_size) : '',
    entry.kind === 'image' ? entry.image_quality?.trim().toLowerCase() || '' : '',
    entry.kind === 'video' ? normalizedVideoResolution(entry.video_resolution) : '',
    entry.kind === 'video' ? entry.video_duration_seconds || 0 : 0
  ])
}

async function load() {
  loading.value = true
  error.value = ''
  success.value = ''
  try {
    state.value = await getUnifiedGatewayRoutePricing()
    form.target_group_id = state.value.saved.target_group_id || state.value.groups[0]?.id || 0
    form.entries = cloneEntries(state.value.saved.entries || [])
  } catch {
    error.value = t('admin.unifiedGateway.pricingLoadFailed')
  } finally {
    loading.value = false
  }
}

function validate(): boolean {
  if (!state.value || !form.target_group_id || !state.value.groups.some((group) => group.id === form.target_group_id)) {
    error.value = t('admin.unifiedGateway.pricingInvalidGroup')
    return false
  }
  for (const entry of form.entries) {
    if (!entry.account_id || !entry.model.trim()) {
      error.value = t('admin.unifiedGateway.pricingRequired')
      return false
    }
    if (entry.kind === 'token') {
      const multiplier = entry.multiplier
      const hasMultiplier = typeof multiplier === 'number'
      if (hasMultiplier && (!Number.isFinite(multiplier) || multiplier < 0)) {
        error.value = t('admin.unifiedGateway.pricingInvalidValue')
        return false
      }
      if (entry.token_base_price) {
        const values = Object.values(entry.token_base_price)
        if (values.length !== 6 || values.some((value) => typeof value !== 'number' || !Number.isFinite(value) || value < 0)) {
          error.value = t('admin.unifiedGateway.pricingBasePriceInvalid')
          return false
        }
      } else if (!hasMultiplier) {
        error.value = t('admin.unifiedGateway.pricingRequired')
        return false
      }
    }
    if (entry.kind === 'image' && (!entry.image_size?.trim() || !entry.image_quality?.trim() || !Number.isFinite(entry.unit_price ?? NaN) || (entry.unit_price ?? -1) < 0)) {
      error.value = t('admin.unifiedGateway.pricingRequired')
      return false
    }
    if (entry.kind === 'video') {
      if (!Number.isInteger(entry.video_duration_seconds) || (entry.video_duration_seconds ?? 0) < 1 || (entry.video_duration_seconds ?? 0) > 15) {
        error.value = t('admin.unifiedGateway.pricingVideoDurationRange')
        return false
      }
      if (!entry.video_resolution?.trim() || !Number.isFinite(entry.unit_price ?? NaN) || (entry.unit_price ?? -1) < 0) {
        error.value = t('admin.unifiedGateway.pricingRequired')
        return false
      }
    }
  }
  const routeKeys = new Set<string>()
  for (const entry of form.entries) {
    const key = routePricingKey(entry)
    if (routeKeys.has(key)) {
      error.value = t('admin.unifiedGateway.pricingDuplicate')
      return false
    }
    routeKeys.add(key)
  }
  return true
}

function serializeEntries(entries: RoutePricingFormEntry[]): RoutePricingEntry[] {
  return entries.map((entry) => {
    const { multiplier, token_base_price, ...rest } = entry
    const result: RoutePricingEntry = { ...rest }
    if (typeof multiplier === 'number') result.multiplier = multiplier
    if (token_base_price) {
      result.token_base_price = {
        input_per_million: Number(token_base_price.input_per_million),
        output_per_million: Number(token_base_price.output_per_million),
        cache_read_per_million: Number(token_base_price.cache_read_per_million),
        cache_write_per_million: Number(token_base_price.cache_write_per_million),
        cache_write_5m_per_million: Number(token_base_price.cache_write_5m_per_million),
        cache_write_1h_per_million: Number(token_base_price.cache_write_1h_per_million)
      }
    }
    return result
  })
}

async function save() {
  error.value = ''
  success.value = ''
  if (!validate() || !state.value) return
  saving.value = true
  try {
    state.value = await updateUnifiedGatewayRoutePricing({
      expected_revision: state.value.saved.revision,
      target_group_id: form.target_group_id,
      entries: serializeEntries(form.entries)
    })
    form.entries = cloneEntries(state.value.saved.entries || [])
    success.value = t('admin.unifiedGateway.pricingSaved')
  } catch (cause) {
    error.value = t('admin.unifiedGateway.pricingSaveFailed')
    const errorObject = cause && typeof cause === 'object'
      ? cause as { status?: unknown; code?: unknown; message?: unknown; response?: { status?: unknown; data?: { code?: unknown; message?: unknown } } }
      : undefined
    const status = errorObject?.status ?? errorObject?.response?.status
    const code = errorObject?.code ?? errorObject?.response?.data?.code
    const message = String(errorObject?.message ?? errorObject?.response?.data?.message ?? cause)
    if (Number(status) === 409 || Number(code) === 409) {
      error.value = t('admin.unifiedGateway.pricingConflict')
      return
    }
    if (message.toLowerCase().includes('revision') || message.toLowerCase().includes('changed')) {
      error.value = t('admin.unifiedGateway.pricingConflict')
    }
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

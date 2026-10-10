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

      <section v-if="state" class="card space-y-4 p-5" data-testid="wokey-price-sync">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 class="text-lg font-semibold">{{ t('admin.unifiedGateway.wokeySyncTitle') }}</h2>
            <p class="mt-1 text-sm text-gray-500">{{ t('admin.unifiedGateway.wokeySyncDescription') }}</p>
          </div>
          <label class="flex items-center gap-2 text-sm">
            <input v-model="wokeyForm.enabled" data-testid="wokey-sync-enabled" type="checkbox" :disabled="saving || syncingWokey" />
            {{ t('admin.unifiedGateway.wokeySyncEnabled') }}
          </label>
        </div>
        <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
          <label>
            <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.wokeySyncFX') }}</span>
            <input v-model.trim="wokeyForm.fx" data-testid="wokey-sync-fx" class="input w-full" type="text" inputmode="decimal" :disabled="saving || syncingWokey" />
          </label>
          <label>
            <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.wokeySyncInterval') }}</span>
            <input v-model.number="wokeyForm.interval_minutes" data-testid="wokey-sync-interval" class="input w-full" type="number" min="5" max="60" step="1" :disabled="saving || syncingWokey" />
          </label>
          <div class="flex items-end justify-end">
            <button class="btn btn-secondary" type="button" data-testid="wokey-sync-now" :disabled="saving || syncingWokey || wokeyConfigChanged || !wokeyForm.enabled || !wokeyForm.account_ids.length" @click="syncWokeyPrices">
              {{ syncingWokey ? t('admin.unifiedGateway.wokeySyncRunning') : t('admin.unifiedGateway.wokeySyncNow') }}
            </button>
          </div>
        </div>
        <fieldset data-testid="wokey-sync-accounts" class="space-y-2" :disabled="saving || syncingWokey">
          <legend class="mb-2 text-xs text-gray-500">{{ t('admin.unifiedGateway.wokeySyncAccounts') }}</legend>
          <p v-if="!eligibleWokeyAccounts.length" class="text-sm text-gray-500">{{ t('admin.unifiedGateway.wokeySyncNoAccounts') }}</p>
          <label v-for="account in eligibleWokeyAccounts" :key="account.id" class="mr-5 inline-flex items-center gap-2 text-sm">
            <input v-model="wokeyForm.account_ids" type="checkbox" :value="account.id" />
            {{ account.name }} (#{{ account.id }})
          </label>
        </fieldset>
        <p v-if="wokeyConfigChanged" class="text-sm text-amber-700 dark:text-amber-300" data-testid="wokey-sync-unsaved">
          {{ t('admin.unifiedGateway.wokeySyncSaveBeforeRefresh') }}
        </p>
        <div class="rounded border border-gray-200 p-3 text-sm dark:border-gray-700" data-testid="wokey-sync-status">
          <p>{{ t('admin.unifiedGateway.wokeySyncStatus', { managed: state.saved.wokey_sync?.status?.managed_card_count ?? 0, conflicts: state.saved.wokey_sync?.status?.manual_conflict_count ?? 0, unsupported: state.saved.wokey_sync?.status?.unsupported_count ?? 0, missing: state.saved.wokey_sync?.status?.not_returned_count ?? 0 }) }}</p>
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.unifiedGateway.wokeySyncLastSuccess', { time: formatWokeyTimestamp(state.saved.wokey_sync?.status?.last_success_at) }) }}</p>
          <p v-if="state.saved.wokey_sync?.status?.last_error_code" class="mt-1 text-sm text-red-600">{{ t('admin.unifiedGateway.wokeySyncLastError', { code: state.saved.wokey_sync.status.last_error_code, http: state.saved.wokey_sync.status.last_http_status || '—' }) }}</p>
          <p v-if="state.saved.wokey_sync?.status?.reasons?.length" class="mt-1 break-words text-xs text-amber-700 dark:text-amber-300">{{ state.saved.wokey_sync.status.reasons.join(' · ') }}</p>
        </div>
        <div v-if="managedWokeyEntries.length" class="space-y-2" data-testid="wokey-managed-cards">
          <h3 class="text-sm font-medium">{{ t('admin.unifiedGateway.wokeyManagedCards') }}</h3>
          <div v-for="entry in managedWokeyEntries" :key="managedRouteKey(entry)" class="flex flex-wrap items-center justify-between gap-3 rounded border border-gray-200 px-3 py-2 text-sm dark:border-gray-700">
            <div>
              <p class="font-medium">{{ accountName(entry.account_id) }} (#{{ entry.account_id }}) · {{ entry.model }} · {{ entry.kind }}</p>
              <div v-if="entry.time_of_day_token_price" data-testid="wokey-dynamic-price">
                <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.wokeySyncTimeOfDayPeak', { price: wokeyTokenTierPrice(entry.time_of_day_token_price.peak.token_base_price) }) }}</p>
                <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.wokeySyncTimeOfDayOffPeak', { price: wokeyTokenTierPrice(entry.time_of_day_token_price.off_peak.token_base_price) }) }}</p>
                <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.wokeySyncTimeOfDayWindows', { windows: formatWokeyWindows(entry.time_of_day_token_price.peak_windows_utc) }) }}</p>
                <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.wokeySyncManagedSource', { fx: entry.source_fx || '—', time: formatWokeyTimestamp(entry.source_fetched_at) }) }}</p>
                <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.wokeySyncTimeOfDayBillingNote') }}</p>
              </div>
              <p class="text-xs text-gray-500"><span v-if="!entry.time_of_day_token_price">{{ wokeyEntryPrice(entry) }} · </span>{{ t(`admin.unifiedGateway.wokeySyncCardState_${entry.sync_state || 'current'}`) }}<span v-if="entry.sync_reason"> · {{ entry.sync_reason }}</span></p>
            </div>
            <button v-if="!entry.time_of_day_token_price" class="btn btn-secondary" type="button" data-testid="wokey-manualize-card" :disabled="saving || syncingWokey" @click="manualizeWokeyCard(entry)">
              {{ t('admin.unifiedGateway.wokeySyncManualize') }}
            </button>
            <p v-else class="text-xs text-gray-500" data-testid="wokey-dynamic-manualize-note">{{ t('admin.unifiedGateway.wokeySyncDynamicManualizeDisabled') }}</p>
          </div>
        </div>
      </section>

      <section class="card space-y-4 p-5" data-testid="route-pricing-video-per-second">
        <div>
          <h2 class="text-lg font-semibold">{{ t('admin.unifiedGateway.pricingVideoPerSecondTitle') }}</h2>
          <p class="mt-1 text-sm text-gray-500">{{ t('admin.unifiedGateway.pricingVideoPerSecondDescription') }}</p>
        </div>
        <div class="grid grid-cols-1 gap-3 md:grid-cols-4">
          <label>
            <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingAccount') }}</span>
            <select v-model.number="videoRateAccountID" data-testid="route-pricing-video-rate-account" class="input w-full" :disabled="saving || !state">
              <option :value="0">—</option>
              <option v-for="account in state?.accounts || []" :key="account.id" :value="account.id">{{ account.name }} (#{{ account.id }})</option>
            </select>
          </label>
          <label>
            <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingModel') }}</span>
            <input v-model.trim="videoRateModel" data-testid="route-pricing-video-rate-model" class="input w-full" autocomplete="off" :disabled="saving" />
          </label>
          <label>
            <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingVideoResolution') }}</span>
            <select v-model="videoRateResolution" data-testid="route-pricing-video-rate-resolution" class="input w-full" :disabled="saving">
              <option value="480p">480p</option>
              <option value="720p">720p</option>
              <option value="1080p">1080p</option>
            </select>
          </label>
          <label>
            <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingVideoPerSecondRate') }}</span>
            <input v-model="videoRatePerSecond" data-testid="route-pricing-video-rate" class="input w-full" type="text" inputmode="decimal" autocomplete="off" :disabled="saving" />
          </label>
        </div>
        <div v-if="videoRatePreview.length" class="flex flex-wrap gap-x-4 gap-y-1 text-sm text-gray-600" data-testid="route-pricing-video-rate-preview">
          <span v-for="preview in videoRatePreview" :key="preview.duration">{{ t('admin.unifiedGateway.pricingVideoPerSecondPreview', preview) }}</span>
        </div>
        <div class="flex justify-end">
          <button class="btn btn-secondary" type="button" data-testid="route-pricing-video-rate-generate" :disabled="saving" @click="expandVideoRate">
            {{ t('admin.unifiedGateway.pricingVideoPerSecondGenerate') }}
          </button>
        </div>
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
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingImageMode') }}</span>
                  <select v-model="entry.image_pricing_mode" data-testid="route-pricing-image-mode" class="input w-full" :disabled="saving" @change="changeImagePricingMode(entry)">
                    <option value="">{{ t('admin.unifiedGateway.pricingImageExactSpec') }}</option>
                    <option value="flat_per_image">{{ t('admin.unifiedGateway.pricingImageFlatPerImage') }}</option>
                  </select>
                </label>
                <template v-if="entry.image_pricing_mode !== 'flat_per_image'">
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
                </template>
                <p v-else class="md:col-span-4 self-end text-xs text-gray-500" data-testid="route-pricing-image-flat-hint">
                  {{ t('admin.unifiedGateway.pricingImageFlatHint') }}
                </p>
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
                  <span class="mt-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheWriteUncovered') }}</span>
                </label>
                <label>
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheWrite5m') }}</span>
                  <input v-model.number="entry.token_base_price.cache_write_5m_per_million" data-testid="route-base-cache-write-5m" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                  <span class="mt-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheWriteUncovered') }}</span>
                </label>
                <label>
                  <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheWrite1h') }}</span>
                  <input v-model.number="entry.token_base_price.cache_write_1h_per_million" data-testid="route-base-cache-write-1h" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                  <span class="mt-1 block text-xs text-amber-700 dark:text-amber-300">{{ t('admin.unifiedGateway.pricingBaseCacheWrite1hZeroUnsupported') }}</span>
                </label>
              </div>
              <div v-if="entry.token_base_price" class="mt-4 border-t border-gray-200 pt-4 dark:border-gray-700">
                <div class="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <p class="text-sm font-medium">{{ t('admin.unifiedGateway.pricingLongContextTitle') }}</p>
                    <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingLongContextUnit') }}</p>
                  </div>
                  <button v-if="!entry.long_context_token_base_price" class="btn btn-secondary" type="button" data-testid="add-route-long-context-price" :disabled="saving" @click="addLongContextPrice(entry)">
                    {{ t('admin.unifiedGateway.pricingLongContextAdd') }}
                  </button>
                  <button v-else class="btn btn-secondary text-red-600" type="button" data-testid="remove-route-long-context-price" :disabled="saving" @click="removeLongContextPrice(entry)">
                    {{ t('admin.unifiedGateway.pricingLongContextRemove') }}
                  </button>
                </div>
                <div v-if="entry.long_context_token_base_price" class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
                  <label>
                    <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseInput') }}</span>
                    <input v-model.number="entry.long_context_token_base_price.input_per_million" data-testid="route-long-context-input" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                  </label>
                  <label>
                    <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseOutput') }}</span>
                    <input v-model.number="entry.long_context_token_base_price.output_per_million" data-testid="route-long-context-output" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                  </label>
                  <label>
                    <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheRead') }}</span>
                    <input v-model.number="entry.long_context_token_base_price.cache_read_per_million" data-testid="route-long-context-cache-read" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                  </label>
                  <label>
                    <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheWrite') }}</span>
                    <input v-model.number="entry.long_context_token_base_price.cache_write_per_million" data-testid="route-long-context-cache-write" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                    <span class="mt-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheWriteUncovered') }}</span>
                  </label>
                  <label>
                    <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheWrite5m') }}</span>
                    <input v-model.number="entry.long_context_token_base_price.cache_write_5m_per_million" data-testid="route-long-context-cache-write-5m" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                    <span class="mt-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheWriteUncovered') }}</span>
                  </label>
                  <label>
                    <span class="mb-1 block text-xs text-gray-500">{{ t('admin.unifiedGateway.pricingBaseCacheWrite1h') }}</span>
                    <input v-model.number="entry.long_context_token_base_price.cache_write_1h_per_million" data-testid="route-long-context-cache-write-1h" class="input w-full" type="number" min="0" step="0.000001" :disabled="saving" />
                    <span class="mt-1 block text-xs text-amber-700 dark:text-amber-300">{{ t('admin.unifiedGateway.pricingBaseCacheWrite1hZeroUnsupported') }}</span>
                  </label>
                </div>
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
  manualizeUnifiedGatewayWokeyPrice,
  syncUnifiedGatewayWokeyPrices,
  updateUnifiedGatewayRoutePricing,
  type RoutePricingEntry,
  type RoutePricingKind,
  type RouteTokenBasePrice,
  type UnifiedGatewayRoutePricingState,
  type WokeySyncConfig
} from '@/api/admin/unifiedGatewayRoutePricing'

const { t } = useI18n()
const state = ref<UnifiedGatewayRoutePricingState | null>(null)
const loading = ref(false)
const saving = ref(false)
const syncingWokey = ref(false)
const error = ref('')
const success = ref('')
const searchQuery = ref('')
type RouteTokenBasePriceForm = { [K in keyof RouteTokenBasePrice]: number | '' | undefined }
type RoutePricingFormEntry = Omit<RoutePricingEntry, 'multiplier' | 'token_base_price' | 'long_context_token_base_price' | 'image_pricing_mode'> & {
  multiplier?: number | ''
  image_pricing_mode?: RoutePricingEntry['image_pricing_mode'] | ''
  token_base_price?: RouteTokenBasePriceForm
  long_context_token_base_price?: RouteTokenBasePriceForm
}
const form = reactive({ target_group_id: 0, entries: [] as RoutePricingFormEntry[] })
const wokeyForm = reactive({ enabled: false, account_ids: [] as number[], fx: '6.9', interval_minutes: 15 })
const videoRateAccountID = ref(0)
const videoRateModel = ref('')
const videoRateResolution = ref('480p')
const videoRatePerSecond = ref('')

const groupName = computed(() => state.value?.groups.find((group) => group.id === form.target_group_id)?.name || '')
const eligibleWokeyAccounts = computed(() => (state.value?.accounts || []).filter((account) => account.wokey_eligible))
const managedWokeyEntries = computed(() => (state.value?.saved.entries || []).filter((entry) => entry.source === 'wokey_catalog'))
const wokeyConfigChanged = computed(() => {
  const saved = normalizeWokeyConfig(state.value?.saved.wokey_sync)
  const current = normalizeWokeyConfig(wokeyForm)
  return JSON.stringify(saved) !== JSON.stringify(current)
})
const videoRatePreview = computed(() => {
  const scaledRate = parseStarsPerSecond(videoRatePerSecond.value)
  if (scaledRate === null) return []
  return [1, 5, 15].map((duration) => ({
    duration,
    price: formatScaledStarPrice(scaledRate * BigInt(duration))
  }))
})
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
  return entries.filter((entry) => entry.source !== 'wokey_catalog').map((entry) => ({
    ...entry,
    image_pricing_mode: entry.image_pricing_mode || '',
    token_base_price: entry.token_base_price ? cloneTokenBasePrice(entry.token_base_price) : undefined,
    long_context_token_base_price: entry.long_context_token_base_price ? cloneTokenBasePrice(entry.long_context_token_base_price) : undefined
  }))
}

function normalizeWokeyConfig(config?: Partial<WokeySyncConfig>) {
  return {
    enabled: Boolean(config?.enabled),
    account_ids: [...(config?.account_ids || [])].sort((a, b) => a - b),
    fx: String(config?.fx || '6.9'),
    interval_minutes: Number(config?.interval_minutes || 15)
  }
}

function setWokeyForm(config?: WokeySyncConfig) {
  const normalized = normalizeWokeyConfig(config)
  wokeyForm.enabled = normalized.enabled
  wokeyForm.account_ids = normalized.account_ids
  wokeyForm.fx = normalized.fx
  wokeyForm.interval_minutes = normalized.interval_minutes
}

function accountName(accountID: number): string {
  return state.value?.accounts.find((account) => account.id === accountID)?.name || `#${accountID}`
}

function formatWokeyTimestamp(value?: string): string {
  if (!value) return t('admin.unifiedGateway.wokeySyncNever')
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function wokeyEntryPrice(entry: RoutePricingEntry): string {
  if (entry.kind === 'token' && entry.token_base_price) {
    return `in ${entry.token_base_price.input_per_million} · out ${entry.token_base_price.output_per_million} · cache ${entry.token_base_price.cache_read_per_million} ★/M`
  }
  if (entry.kind === 'video') return `${entry.video_resolution} · ${entry.video_duration_seconds}s · ${entry.unit_price} ★`
  if (entry.kind === 'image') return `${entry.image_pricing_mode === 'flat_per_image' ? t('admin.unifiedGateway.wokeySyncPerImage') : entry.image_size} · ${entry.unit_price} ★`
  return '—'
}

function wokeyTokenTierPrice(price: RouteTokenBasePrice): string {
  const meters = [
    `${t('admin.unifiedGateway.wokeySyncInputShort')} ${price.input_per_million}`,
    `${t('admin.unifiedGateway.wokeySyncOutputShort')} ${price.output_per_million}`,
    `${t('admin.unifiedGateway.wokeySyncCacheReadShort')} ${price.cache_read_per_million}`
  ]
  if (price.cache_write_per_million != null) meters.push(`${t('admin.unifiedGateway.wokeySyncCacheWriteShort')} ${price.cache_write_per_million}`)
  if (price.cache_write_5m_per_million != null) meters.push(`${t('admin.unifiedGateway.wokeySyncCacheWrite5mShort')} ${price.cache_write_5m_per_million}`)
  if (price.cache_write_1h_per_million != null) meters.push(`${t('admin.unifiedGateway.wokeySyncCacheWrite1hShort')} ${price.cache_write_1h_per_million}`)
  return `${meters.join(' · ')} ★/M`
}

function formatWokeyWindows(windows: Array<{ start_hour: number; end_hour: number }>): string {
  const hour = (value: number) => `${String(value).padStart(2, '0')}:00`
  return windows.map(({ start_hour, end_hour }) => `${hour(start_hour)}–${hour(end_hour)}`).join(', ')
}

function managedRouteKey(entry: RoutePricingEntry): string {
  return JSON.stringify([entry.account_id, entry.model, entry.kind, entry.image_pricing_mode, entry.image_size, entry.image_quality, entry.video_resolution, entry.video_duration_seconds])
}

function cloneTokenBasePrice(price: RouteTokenBasePrice): RouteTokenBasePriceForm {
  return {
    input_per_million: price.input_per_million,
    output_per_million: price.output_per_million,
    cache_read_per_million: price.cache_read_per_million,
    cache_write_per_million: price.cache_write_per_million ?? '',
    cache_write_5m_per_million: price.cache_write_5m_per_million ?? '',
    cache_write_1h_per_million: price.cache_write_1h_per_million ?? ''
  }
}

function newTokenBasePriceForm(): RouteTokenBasePriceForm {
  return {
    input_per_million: '',
    output_per_million: '',
    cache_read_per_million: '',
    cache_write_per_million: '',
    cache_write_5m_per_million: '',
    cache_write_1h_per_million: ''
  }
}

function newEntry(kind: RoutePricingKind = 'token'): RoutePricingFormEntry {
  const entry: RoutePricingFormEntry = { account_id: state.value?.accounts[0]?.id || 0, model: '', kind }
  if (kind === 'token') entry.multiplier = 1
  if (kind === 'image') Object.assign(entry, { image_pricing_mode: '', image_size: '', image_quality: '', unit_price: 0 })
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
  delete entry.long_context_token_base_price
  delete entry.image_pricing_mode
  delete entry.unit_price
  delete entry.image_size
  delete entry.image_quality
  delete entry.video_resolution
  delete entry.video_duration_seconds
  if (entry.kind === 'token') entry.multiplier = 1
  if (entry.kind === 'image') Object.assign(entry, { image_pricing_mode: '', image_size: '', image_quality: '', unit_price: 0 })
  if (entry.kind === 'video') Object.assign(entry, { video_resolution: '', video_duration_seconds: 0, unit_price: 0 })
}

function addBasePriceCard(entry: RoutePricingFormEntry) {
  entry.token_base_price = newTokenBasePriceForm()
  success.value = ''
}

function removeBasePriceCard(entry: RoutePricingFormEntry) {
  delete entry.token_base_price
  delete entry.long_context_token_base_price
  success.value = ''
}

function addLongContextPrice(entry: RoutePricingFormEntry) {
  entry.long_context_token_base_price = newTokenBasePriceForm()
  success.value = ''
}

function removeLongContextPrice(entry: RoutePricingFormEntry) {
  delete entry.long_context_token_base_price
  success.value = ''
}

function changeImagePricingMode(entry: RoutePricingFormEntry) {
  if (entry.image_pricing_mode === 'flat_per_image') {
    delete entry.image_size
    delete entry.image_quality
  } else {
    entry.image_size = ''
    entry.image_quality = ''
  }
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

const starPriceScale = 1_000_000_000n

function parseStarsPerSecond(value: string): bigint | null {
  const normalized = value.trim()
  if (!/^\d+(?:\.\d{1,9})?$/.test(normalized)) return null
  const [whole, fraction = ''] = normalized.split('.')
  try {
    return BigInt(whole) * starPriceScale + BigInt(fraction.padEnd(9, '0') || '0')
  } catch {
    return null
  }
}

function formatScaledStarPrice(scaledPrice: bigint): string {
  const whole = scaledPrice / starPriceScale
  const fraction = (scaledPrice % starPriceScale).toString().padStart(9, '0').replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : whole.toString()
}

function expandVideoRate() {
  error.value = ''
  success.value = ''
  const scaledRate = parseStarsPerSecond(videoRatePerSecond.value)
  if (!videoRateAccountID.value || !videoRateModel.value.trim()) {
    error.value = t('admin.unifiedGateway.pricingVideoPerSecondAccountRequired')
    return
  }
  if (scaledRate === null) {
    error.value = t('admin.unifiedGateway.pricingVideoPerSecondInvalid')
    return
  }

  const model = videoRateModel.value.trim()
  const resolution = normalizedVideoResolution(videoRateResolution.value)
  const generated: RoutePricingFormEntry[] = []
  for (let duration = 1; duration <= 15; duration++) {
    const unitPrice = Number(formatScaledStarPrice(scaledRate * BigInt(duration)))
    if (!Number.isFinite(unitPrice)) {
      error.value = t('admin.unifiedGateway.pricingVideoPerSecondInvalid')
      return
    }
    generated.push({
      account_id: videoRateAccountID.value,
      model,
      kind: 'video',
      video_resolution: resolution,
      video_duration_seconds: duration,
      unit_price: unitPrice
    })
  }

  const preserved = form.entries.filter((entry) => !(
    entry.kind === 'video' &&
    entry.account_id === videoRateAccountID.value &&
    entry.model.trim() === model &&
    normalizedVideoResolution(entry.video_resolution) === resolution &&
    (entry.video_duration_seconds ?? 0) >= 1 && (entry.video_duration_seconds ?? 0) <= 15
  ))
  form.entries.splice(0, form.entries.length, ...preserved, ...generated)
}

function routePricingKey(entry: RoutePricingFormEntry): string {
  return JSON.stringify([
    form.target_group_id,
    entry.account_id,
    entry.model.trim(),
    entry.kind,
    entry.kind === 'image' ? entry.image_pricing_mode || '' : '',
    entry.kind === 'image' && entry.image_pricing_mode !== 'flat_per_image' ? normalizedImageTier(entry.image_size) : '',
    entry.kind === 'image' && entry.image_pricing_mode !== 'flat_per_image' ? entry.image_quality?.trim().toLowerCase() || '' : '',
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
    setWokeyForm(state.value.saved.wokey_sync)
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
  if (!/^(?:\d+)(?:\.\d+)?$/.test(wokeyForm.fx.trim()) || !Number.isFinite(Number(wokeyForm.fx)) || Number(wokeyForm.fx) <= 0 ||
      !Number.isInteger(wokeyForm.interval_minutes) || wokeyForm.interval_minutes < 5 || wokeyForm.interval_minutes > 60 ||
      (wokeyForm.enabled && wokeyForm.account_ids.length === 0) ||
      wokeyForm.account_ids.some((id) => !eligibleWokeyAccounts.value.some((account) => account.id === id))) {
    error.value = t('admin.unifiedGateway.wokeySyncInvalidConfig')
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
        if (!isValidBasePriceForm(entry.token_base_price)) {
          error.value = t('admin.unifiedGateway.pricingBasePriceInvalid')
          return false
        }
      } else if (!hasMultiplier) {
        error.value = t('admin.unifiedGateway.pricingRequired')
        return false
      }
      if (entry.long_context_token_base_price) {
        if (entry.model.trim() !== 'claude-haiku-5-5') {
          error.value = t('admin.unifiedGateway.pricingLongContextModelOnly')
          return false
        }
        if (!entry.token_base_price || !isValidBasePriceForm(entry.long_context_token_base_price)) {
          error.value = t('admin.unifiedGateway.pricingBasePriceInvalid')
          return false
        }
      }
    }
    if (entry.kind === 'image') {
      const isFlat = entry.image_pricing_mode === 'flat_per_image'
      if ((!isFlat && (!entry.image_size?.trim() || !entry.image_quality?.trim())) ||
          (isFlat && (entry.image_size?.trim() || entry.image_quality?.trim())) ||
          !Number.isFinite(entry.unit_price ?? NaN) || (entry.unit_price ?? -1) < 0) {
        error.value = t('admin.unifiedGateway.pricingRequired')
        return false
      }
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
  const imageModesByCard = new Map<string, string>()
  for (const entry of form.entries) {
    if (entry.kind === 'image') {
      const cardKey = JSON.stringify([form.target_group_id, entry.account_id, entry.model.trim()])
      const mode = entry.image_pricing_mode || ''
      const previousMode = imageModesByCard.get(cardKey)
      if (previousMode !== undefined && (previousMode === 'flat_per_image' || mode === 'flat_per_image')) {
        error.value = t('admin.unifiedGateway.pricingImageModesConflict')
        return false
      }
      imageModesByCard.set(cardKey, mode)
    }
    const key = routePricingKey(entry)
    if (routeKeys.has(key)) {
      error.value = t('admin.unifiedGateway.pricingDuplicate')
      return false
    }
    routeKeys.add(key)
  }
  return true
}

function isValidBasePriceForm(price: RouteTokenBasePriceForm): boolean {
  const required = [price.input_per_million, price.output_per_million, price.cache_read_per_million]
  if (required.some((value) => typeof value !== 'number' || !Number.isFinite(value) || value < 0)) return false
  const optional = [price.cache_write_per_million, price.cache_write_5m_per_million, price.cache_write_1h_per_million]
  return optional.every((value) => value === '' || value === undefined || value === null || (typeof value === 'number' && Number.isFinite(value) && value >= 0))
}

function serializeBasePrice(price: RouteTokenBasePriceForm): RouteTokenBasePrice {
  const serialized: RouteTokenBasePrice = {
    input_per_million: Number(price.input_per_million),
    output_per_million: Number(price.output_per_million),
    cache_read_per_million: Number(price.cache_read_per_million)
  }
  if (typeof price.cache_write_per_million === 'number') serialized.cache_write_per_million = price.cache_write_per_million
  if (typeof price.cache_write_5m_per_million === 'number') serialized.cache_write_5m_per_million = price.cache_write_5m_per_million
  if (typeof price.cache_write_1h_per_million === 'number') serialized.cache_write_1h_per_million = price.cache_write_1h_per_million
  return serialized
}

function serializeEntries(entries: RoutePricingFormEntry[]): RoutePricingEntry[] {
  return entries.map((entry) => {
    const { multiplier, token_base_price, long_context_token_base_price, image_pricing_mode, ...rest } = entry
    const result: RoutePricingEntry = { ...rest }
    if (typeof multiplier === 'number') result.multiplier = multiplier
    if (token_base_price) result.token_base_price = serializeBasePrice(token_base_price)
    if (long_context_token_base_price) result.long_context_token_base_price = serializeBasePrice(long_context_token_base_price)
    if (image_pricing_mode === 'flat_per_image') result.image_pricing_mode = image_pricing_mode
    return result
  })
}

async function save() {
  error.value = ''
  success.value = ''
  if (!validate() || !state.value) return
  saving.value = true
  try {
    const input: Parameters<typeof updateUnifiedGatewayRoutePricing>[0] = {
      expected_revision: state.value.saved.revision,
      target_group_id: form.target_group_id,
      entries: serializeEntries(form.entries)
    }
    if (wokeyConfigChanged.value) {
      input.wokey_sync = {
        enabled: wokeyForm.enabled,
        account_ids: [...wokeyForm.account_ids].sort((a, b) => a - b),
        fx: wokeyForm.fx.trim(),
        interval_minutes: wokeyForm.interval_minutes
      }
    }
    state.value = await updateUnifiedGatewayRoutePricing(input)
    form.entries = cloneEntries(state.value.saved.entries || [])
    setWokeyForm(state.value.saved.wokey_sync)
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

async function syncWokeyPrices() {
  error.value = ''
  success.value = ''
  syncingWokey.value = true
  try {
    state.value = await syncUnifiedGatewayWokeyPrices()
    form.entries = cloneEntries(state.value.saved.entries || [])
    setWokeyForm(state.value.saved.wokey_sync)
    success.value = t('admin.unifiedGateway.wokeySyncSucceeded')
  } catch (cause) {
    const errorObject = cause && typeof cause === 'object'
      ? cause as { message?: unknown; response?: { data?: { message?: unknown } } }
      : undefined
    error.value = String(errorObject?.response?.data?.message ?? errorObject?.message ?? t('admin.unifiedGateway.wokeySyncFailed'))
  } finally {
    syncingWokey.value = false
  }
}

async function manualizeWokeyCard(entry: RoutePricingEntry) {
  if (!state.value) return
  error.value = ''
  success.value = ''
  saving.value = true
  try {
    state.value = await manualizeUnifiedGatewayWokeyPrice({
      expected_revision: state.value.saved.revision,
      key: {
        account_id: entry.account_id,
        model: entry.model,
        kind: entry.kind,
        image_pricing_mode: entry.image_pricing_mode,
        image_size: entry.image_size,
        image_quality: entry.image_quality,
        video_resolution: entry.video_resolution,
        video_duration_seconds: entry.video_duration_seconds
      }
    })
    form.entries = cloneEntries(state.value.saved.entries || [])
    setWokeyForm(state.value.saved.wokey_sync)
    success.value = t('admin.unifiedGateway.wokeySyncManualized')
  } catch {
    error.value = t('admin.unifiedGateway.wokeySyncFailed')
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

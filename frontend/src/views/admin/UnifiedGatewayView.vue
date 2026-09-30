<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.unifiedGateway.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.unifiedGateway.description') }}</p>
        </div>
        <button class="btn btn-secondary" :disabled="loading" @click="loadPage">{{ t('common.refresh') }}</button>
      </div>

      <div v-if="loading" class="card p-8 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>

      <div v-else class="space-y-6">
        <section data-testid="gateway-status" class="card space-y-4 p-5">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.unifiedGateway.statusTitle') }}</h2>
              <p class="mt-1 text-sm text-gray-500">{{ gateMessage }}</p>
            </div>
            <span data-testid="status-management" :class="managementStatusClass">{{ managementStatusLabel }}</span>
          </div>
          <details class="rounded-lg border border-gray-200 px-3 py-2 dark:border-gray-700">
            <summary class="cursor-pointer text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.unifiedGateway.showDiagnostics') }}</summary>
            <dl class="mt-3 grid gap-3 text-sm sm:grid-cols-2 xl:grid-cols-4">
              <div>
                <dt class="text-xs text-gray-500">{{ t('admin.unifiedGateway.statusAdminUi') }}</dt>
                <dd data-testid="status-admin-ui" class="font-medium">{{ booleanStatus(meta?.admin_ui_enabled) }}</dd>
              </div>
              <div>
                <dt class="text-xs text-gray-500">{{ t('admin.unifiedGateway.statusMigration') }}</dt>
                <dd data-testid="status-migration" class="font-medium">{{ readinessStatus(meta?.migration_ready) }}</dd>
              </div>
              <div>
                <dt class="text-xs text-gray-500">{{ t('admin.unifiedGateway.statusRuntimeEnabled') }}</dt>
                <dd data-testid="status-runtime-enabled" class="font-medium">{{ booleanStatus(meta?.runtime_enabled) }}</dd>
              </div>
              <div>
                <dt class="text-xs text-gray-500">{{ t('admin.unifiedGateway.statusRuntimeEffective') }}</dt>
                <dd data-testid="status-runtime-effective" class="font-medium">{{ runtimeEffectiveLabel(currentRuntimeEffective) }}</dd>
              </div>
              <div>
                <dt class="text-xs text-gray-500">{{ t('admin.unifiedGateway.statusCapabilityRead') }}</dt>
                <dd data-testid="status-read" class="font-medium">{{ capabilityStatus(meta?.capabilities?.read) }}</dd>
              </div>
              <div>
                <dt class="text-xs text-gray-500">{{ t('admin.unifiedGateway.statusCapabilityWrite') }}</dt>
                <dd data-testid="status-write" class="font-medium">{{ capabilityStatus(writeCapability) }}</dd>
              </div>
              <div>
                <dt class="text-xs text-gray-500">{{ t('admin.unifiedGateway.statusCapabilityProbe') }}</dt>
                <dd data-testid="status-probe" class="font-medium">{{ capabilityStatus(meta?.capabilities?.probe) }}</dd>
              </div>
            </dl>
            <ul v-if="meta?.blockers?.length" class="mt-3 list-disc space-y-1 pl-5 text-xs text-amber-700 dark:text-amber-300">
              <li v-for="blocker in meta.blockers" :key="`${blocker.code}-${blocker.path ?? ''}`">{{ blocker.message }}</li>
            </ul>
          </details>
          <div class="grid gap-3 text-sm sm:grid-cols-2 xl:grid-cols-4">
            <div class="rounded-lg bg-gray-50 p-3 dark:bg-gray-800/60"><span class="text-xs text-gray-500">{{ t('admin.unifiedGateway.statusPublishedModels') }}</span><strong data-testid="status-published-models" class="mt-1 block text-lg">{{ publishedModelCount }}</strong></div>
            <div class="rounded-lg bg-gray-50 p-3 dark:bg-gray-800/60"><span class="text-xs text-gray-500">{{ t('admin.unifiedGateway.statusAvailableModels') }}</span><strong data-testid="status-available-models" class="mt-1 block text-lg">{{ availableModelCount }}</strong></div>
            <div class="rounded-lg bg-gray-50 p-3 dark:bg-gray-800/60"><span class="text-xs text-gray-500">{{ t('admin.unifiedGateway.statusNeedsAction') }}</span><strong data-testid="status-needs-action" class="mt-1 block text-lg">{{ needsActionModelCount }}</strong></div>
            <div class="rounded-lg bg-gray-50 p-3 dark:bg-gray-800/60"><span class="text-xs text-gray-500">{{ t('admin.unifiedGateway.statusLastChecked') }}</span><strong data-testid="status-last-checked" class="mt-1 block break-words text-sm">{{ meta?.server_time || '—' }}</strong></div>
          </div>
        </section>

        <div v-if="!managementAvailable" data-testid="gateway-gate" class="card border-amber-200 p-6 dark:border-amber-800">
          <h2 class="font-medium text-amber-800 dark:text-amber-200">{{ t('admin.unifiedGateway.gatedTitle') }}</h2>
          <p class="mt-2 text-sm text-amber-700 dark:text-amber-300">{{ gateMessage }}</p>
        </div>

        <details v-if="managementAvailable" data-testid="gateway-model-candidates" class="card group p-5">
          <summary class="flex cursor-pointer list-none flex-wrap items-center justify-between gap-3">
            <div class="min-w-0">
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.unifiedGateway.modelCandidates') }}</h2>
              <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.modelCandidatesHint') }} · {{ modelCandidateGroups.length }} {{ t('admin.unifiedGateway.modelGroupCount') }} · {{ needsActionModelCount }} {{ t('admin.unifiedGateway.pendingModels') }}</p>
            </div>
            <div class="flex items-center gap-2">
              <button data-testid="gateway-configure-all-models" type="button" class="btn btn-primary text-xs" :disabled="saving || bulkConfiguring || !canWrite || !needsActionModelCount" @click.prevent="configureAllModels">{{ bulkConfiguring ? t('admin.unifiedGateway.configuringAll') : t('admin.unifiedGateway.configureAllModels') }}</button>
              <span class="text-xs text-gray-500 group-open:hidden">{{ t('admin.unifiedGateway.expandCandidates') }}</span>
            </div>
          </summary>
          <div class="mt-4 space-y-3">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.candidatePanelHint') }}</p>
            <div class="flex flex-wrap items-center gap-2">
              <label class="sr-only" for="gateway-model-search">{{ t('admin.unifiedGateway.searchModels') }}</label>
              <input id="gateway-model-search" data-testid="gateway-model-search" v-model.trim="candidateSearch" class="input min-w-[220px] text-sm" :placeholder="t('admin.unifiedGateway.searchModels')" />
              <button class="btn btn-secondary text-xs" :disabled="saving || loading || bulkConfiguring" @click="loadModelCandidates">{{ t('admin.unifiedGateway.refreshCandidates') }}</button>
            </div>
          </div>
          <p v-if="bulkSummary" data-testid="gateway-bulk-summary" class="rounded-lg bg-teal-50 p-3 text-sm text-teal-800 dark:bg-teal-950/30 dark:text-teal-200">{{ bulkSummary }}</p>
          <div v-if="modelCandidateGroups.length === 0" class="text-sm text-gray-500">{{ t('admin.unifiedGateway.noModelCandidates') }}</div>
          <div v-else-if="filteredModelCandidateGroups.length === 0" data-testid="gateway-no-matching-candidates" class="text-sm text-gray-500">{{ t('admin.unifiedGateway.noMatchingCandidates') }}</div>
          <div v-else class="overflow-x-auto">
            <table class="min-w-full text-left text-xs">
              <thead class="text-gray-500"><tr><th class="px-2 py-1">{{ t('admin.unifiedGateway.publicModel') }}</th><th class="px-2 py-1">{{ t('admin.unifiedGateway.provider') }}</th><th class="px-2 py-1">{{ t('admin.unifiedGateway.endpoint') }}</th><th class="px-2 py-1">{{ t('admin.unifiedGateway.availableRoutes') }}</th><th class="px-2 py-1">{{ t('admin.unifiedGateway.billingMode') }}</th><th class="px-2 py-1">{{ t('admin.unifiedGateway.status') }}</th><th class="px-2 py-1">{{ t('admin.unifiedGateway.quickStartAction') }}</th></tr></thead>
              <tbody>
                <template v-for="group in filteredModelCandidateGroups" :key="group.key">
                  <tr class="border-t border-gray-100 dark:border-gray-700">
                    <td class="px-2 py-2 font-medium">{{ group.public_model }}</td>
                    <td class="px-2 py-2">{{ providerSummary(group) }}</td>
                    <td class="px-2 py-2">{{ endpointLabel(group.endpoint) }}</td>
                    <td class="px-2 py-2"><span :class="group.eligible_count ? 'text-green-600' : 'text-amber-600'">{{ group.eligible_count }}/{{ group.candidates.length }} {{ t('admin.unifiedGateway.eligible') }}</span></td>
                    <td class="px-2 py-2">{{ billingModeLabel(billingModeForEndpoint(group.endpoint)) }}</td>
                    <td class="px-2 py-2"><span :class="groupStatusClass(group)">{{ groupStatusLabel(group) }}</span></td>
                    <td class="px-2 py-2"><button data-testid="gateway-configure-model" class="btn btn-secondary text-xs" :disabled="saving || bulkConfiguring || !canWrite || !group.eligible_count" @click="useFirstCandidate(group)">{{ t('admin.unifiedGateway.configureModel') }}</button></td>
                  </tr>
                  <tr class="border-t border-gray-50 dark:border-gray-800">
                    <td colspan="7" class="px-2 py-2">
                      <details>
                        <summary class="cursor-pointer text-teal-700 dark:text-teal-300">{{ t('admin.unifiedGateway.showRoutes') }}</summary>
                        <div class="mt-2 space-y-2">
                          <div v-for="candidate in group.candidates" :key="`${candidate.public_model}-${candidate.provider_identity}-${candidate.account_id}-${candidate.endpoint}`" class="flex flex-wrap items-center justify-between gap-2 rounded-lg bg-gray-50 p-2 dark:bg-gray-800/60">
                            <div><span class="font-medium">{{ candidate.account_name }}</span><span class="ml-2 text-gray-500">{{ candidate.provider_identity }} · {{ candidate.upstream_model }}</span><span class="ml-2" :class="candidate.runtime_eligible ? 'text-green-600' : 'text-amber-600'">{{ candidate.runtime_eligible ? t('admin.unifiedGateway.eligible') : (candidate.blockers?.map(item => item.code).join(', ') || t('admin.unifiedGateway.blocked')) }}</span></div>
                            <button data-testid="gateway-use-candidate" class="btn btn-secondary text-xs" :disabled="saving || bulkConfiguring || !canWrite || !candidate.runtime_eligible" @click="useCandidate(candidate)">{{ t('admin.unifiedGateway.useThisRoute') }}</button>
                          </div>
                        </div>
                      </details>
                    </td>
                  </tr>
                </template>
              </tbody>
            </table>
          </div>
          </div>
        </details>

        <section v-if="managementAvailable" data-testid="gateway-api-usage" class="card space-y-3 border-teal-100 p-5 dark:border-teal-900">
          <div>
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.unifiedGateway.apiUsageTitle') }}</h2>
            <p class="mt-1 text-sm text-gray-500">{{ t('admin.unifiedGateway.apiUsageHint') }}</p>
          </div>
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-gray-800/60">
            <span class="text-xs text-gray-500">{{ t('admin.unifiedGateway.baseUrl') }}</span>
            <code data-testid="gateway-base-url" class="mt-1 block break-all text-sm text-gray-900 dark:text-gray-100">{{ unifiedBaseUrl }}</code>
          </div>
          <div class="flex flex-wrap items-center gap-2 text-xs text-gray-600 dark:text-gray-300"><span>{{ t('admin.unifiedGateway.unifiedGroup') }}</span><strong data-testid="gateway-unified-group" class="text-gray-900 dark:text-gray-100">{{ unifiedGroupName || t('admin.unifiedGateway.groupNotReady') }}</strong></div>
          <div class="flex flex-wrap items-center gap-2 text-xs text-gray-600 dark:text-gray-300"><span>{{ t('admin.unifiedGateway.modelListPath') }}</span><code data-testid="gateway-model-list-path" class="rounded bg-gray-100 px-2 py-1 dark:bg-gray-800">/models</code></div>
          <div class="flex flex-wrap gap-2 text-xs text-gray-600 dark:text-gray-300"><span>{{ t('admin.unifiedGateway.supportedEndpoints') }}：</span><span v-for="endpoint in (meta?.supported_endpoints ?? [])" :key="endpoint" class="rounded-full bg-gray-100 px-2 py-1 dark:bg-gray-800">{{ endpointLabel(endpoint) }}</span></div>
        </section>

        <template v-if="managementAvailable">
        <div class="grid gap-6 xl:grid-cols-[minmax(0,1.5fr)_minmax(360px,1fr)]">
          <section data-testid="gateway-editor" class="card space-y-5 p-5">
            <fieldset :disabled="!canWrite" class="m-0 space-y-5 border-0 p-0">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div>
                <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.unifiedGateway.editor') }}</h2>
                <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.editorHint') }}</p>
              </div>
              <div class="flex gap-2">
                <button class="btn btn-secondary" :disabled="saving" @click="resetDocument">{{ t('admin.unifiedGateway.newDraft') }}</button>
                <button data-testid="gateway-advanced-toggle" class="btn btn-secondary" type="button" @click="advancedEditorOpen = !advancedEditorOpen">{{ advancedEditorOpen ? t('admin.unifiedGateway.hideAdvanced') : t('admin.unifiedGateway.showAdvanced') }}</button>
                <button v-if="advancedEditorOpen" data-testid="gateway-save" class="btn btn-primary" :disabled="saving" @click="saveDraft">{{ saving ? t('common.saving') : t('common.save') }}</button>
              </div>
            </div>

            <div data-testid="gateway-selection-summary" class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-gray-700 dark:bg-gray-800/60">
              <div class="grid gap-3 text-sm sm:grid-cols-4">
                <div><span class="block text-xs text-gray-500">{{ t('admin.unifiedGateway.accessGroup') }}</span><strong data-testid="gateway-selected-group" class="mt-1 block">{{ selectedGroupName || t('admin.unifiedGateway.groupNotReady') }}</strong></div>
                <div><span class="block text-xs text-gray-500">{{ t('admin.unifiedGateway.publicModel') }}</span><strong data-testid="gateway-selected-model" class="mt-1 block break-words">{{ document.public_model || t('admin.unifiedGateway.selectModelFirst') }}</strong></div>
                <div><span class="block text-xs text-gray-500">{{ t('admin.unifiedGateway.endpoint') }}</span><strong data-testid="gateway-endpoint" class="mt-1 block">{{ endpointLabel(document.endpoint) }}</strong></div>
                <div><span class="block text-xs text-gray-500">{{ t('admin.unifiedGateway.selectedRoute') }}</span><strong data-testid="gateway-selected-route" class="mt-1 block break-words">{{ selectedRouteSummary }}</strong></div>
              </div>
              <p class="mt-3 text-xs text-gray-500">{{ t('admin.unifiedGateway.selectionReadonlyHint') }}</p>
              <p v-if="backupRouteCount" data-testid="gateway-backup-route-hint" class="mt-2 text-xs text-teal-700 dark:text-teal-300">{{ t('admin.unifiedGateway.backupRouteHint', { count: backupRouteCount }) }}</p>
            </div>

            <div v-if="advancedEditorOpen" class="grid gap-4 md:grid-cols-3">
              <label class="field-label">{{ t('admin.unifiedGateway.accessGroup') }}
                <select v-model="document.access_group_id" class="input mt-1">
                  <option value="">{{ t('admin.unifiedGateway.selectGroup') }}</option>
                  <option v-for="group in options?.items.access_groups ?? []" :key="group.id" :value="group.id">{{ group.name }}</option>
                </select>
              </label>
              <label class="field-label">{{ t('admin.unifiedGateway.publicModel') }}
                <input v-model.trim="document.public_model" class="input mt-1" placeholder="gpt-5.5 / deepseek-chat" />
              </label>
              <label class="field-label">{{ t('admin.unifiedGateway.endpoint') }}
                <select data-testid="gateway-endpoint-editor" v-model="document.endpoint" class="input mt-1">
                  <option v-for="endpoint in (meta?.supported_endpoints ?? [])" :key="endpoint" :value="endpoint">{{ endpointLabel(endpoint) }}</option>
                </select>
              </label>
            </div>

            <section v-if="document.lanes.length" data-testid="gateway-basic-pricing" class="space-y-4 rounded-xl border border-teal-100 bg-teal-50/50 p-4 dark:border-teal-900 dark:bg-teal-950/20">
              <div class="flex flex-wrap items-start justify-between gap-4">
                <div>
                <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.unifiedGateway.basicPricingTitle') }}</h3>
                <p class="mt-1 text-xs text-gray-600 dark:text-gray-300">{{ t('admin.unifiedGateway.basicPricingHint') }}</p>
                </div>
                <label class="field-label min-w-[220px]">{{ t('admin.unifiedGateway.unifiedMarkup') }}
                  <input data-testid="gateway-unified-markup" v-model.trim="unifiedMarkupMultiplier" class="input mt-1" inputmode="decimal" @input="applyUnifiedMarkup" />
                </label>
              </div>
              <div v-for="(lane, laneIndex) in document.lanes" :key="lane.id" :data-testid="`gateway-basic-lane-${laneIndex}`" class="space-y-3 rounded-lg border border-white/80 bg-white/70 p-3 dark:border-gray-700 dark:bg-gray-900/30">
                <div class="flex flex-wrap items-start justify-between gap-2">
                  <div>
                    <strong class="text-sm text-gray-900 dark:text-gray-100">{{ laneIndex === 0 ? t('admin.unifiedGateway.primaryRouteName') : t('admin.unifiedGateway.backupRouteName') }} · {{ laneRouteSummary(lane) }}</strong>
                    <p class="mt-1 text-xs text-gray-500">{{ t('admin.unifiedGateway.providerPriceHint') }}</p>
                  </div>
                  <span class="rounded-full bg-gray-100 px-2 py-1 text-xs text-gray-600 dark:bg-gray-800 dark:text-gray-300">{{ billingModeLabel(lane.profile.billing_mode) }}</span>
                </div>
                <div class="grid gap-3 md:grid-cols-3">
                  <div class="field-label"><span>{{ t('admin.unifiedGateway.billingMode') }}</span><strong :data-testid="simpleTestId('gateway-basic-billing-mode', laneIndex)" class="mt-1 block rounded-md bg-gray-50 px-3 py-2 text-sm text-gray-900 dark:bg-gray-800 dark:text-gray-100">{{ billingModeLabel(lane.profile.billing_mode) }}</strong></div>
                  <div class="field-label"><span>{{ t('admin.unifiedGateway.providerPriceSource') }}</span><strong class="mt-1 block rounded-md bg-gray-50 px-3 py-2 text-sm text-gray-900 dark:bg-gray-800 dark:text-gray-100">{{ lane.profile.provider_base_unit_price ? t('admin.unifiedGateway.providerPriceLoaded') : t('admin.unifiedGateway.providerPricePending') }}</strong></div>
                  <div class="field-label"><span>{{ t('admin.unifiedGateway.effectiveMarkup') }}</span><strong class="mt-1 block rounded-md bg-gray-50 px-3 py-2 text-sm text-gray-900 dark:bg-gray-800 dark:text-gray-100">{{ lane.profile.user_markup_multiplier || unifiedMarkupMultiplier }}×</strong></div>
                </div>
                <p class="text-xs text-gray-600 dark:text-gray-300">{{ t('admin.unifiedGateway.providerPriceFormula') }}</p>
              </div>
            </section>

            <section data-testid="gateway-simple-preview" class="rounded-xl border border-gray-200 p-4 dark:border-gray-700">
              <div class="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.unifiedGateway.simplePreviewTitle') }}</h3>
                  <p class="mt-1 text-xs text-gray-500">{{ t('admin.unifiedGateway.simplePreviewHint') }}</p>
                </div>
                <button data-testid="gateway-basic-preview" type="button" class="btn btn-secondary" :disabled="saving || !canWrite || !document.public_model" @click="previewCurrent">{{ t('admin.unifiedGateway.previewAction') }}</button>
              </div>
              <div v-if="preview" data-testid="gateway-simple-preview-result" class="mt-3 rounded-lg bg-gray-50 p-3 text-sm dark:bg-gray-800/60">
                <div class="flex flex-wrap items-center justify-between gap-2"><span>{{ t('admin.unifiedGateway.estimatedCharge') }}</span><strong>{{ preview.estimated_charge }} {{ preview.currency }}</strong></div>
                <p class="mt-1 text-xs text-gray-500">{{ t('admin.unifiedGateway.previewMatchedRoute') }}：{{ [preview.account_display_name, preview.provider_identity, preview.upstream_model].filter(Boolean).join(' · ') || selectedRouteSummary }}</p>
                <p class="mt-1 text-xs text-gray-500">{{ t('admin.unifiedGateway.billingUnit') }}：{{ preview.billing_unit }} · {{ t('admin.unifiedGateway.rateSource') }}：{{ preview.resolved_rate_source }}<span v-if="preview.price_source_revision"> · {{ t('admin.unifiedGateway.priceSourceRevision') }}：{{ preview.price_source_revision }}</span></p>
                <p class="mt-1 text-xs text-gray-500">{{ t('admin.unifiedGateway.userUnitPrice') }}：{{ preview.user_unit_price }} {{ preview.currency }} · {{ t('admin.unifiedGateway.effectiveMultiplier') }}：{{ preview.effective_multiplier }} · {{ t('admin.unifiedGateway.failureCharge') }}：{{ preview.failure_charge_amount }} {{ preview.currency }}</p>
              </div>
            </section>

            <div v-show="advancedEditorOpen" data-testid="gateway-advanced-editor" class="space-y-4 rounded-xl border border-dashed border-gray-200 p-3 dark:border-gray-700">
              <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.advancedHint') }}</p>
              <div class="space-y-4">
              <div v-for="(lane, laneIndex) in document.lanes" :key="lane.id" class="rounded-xl border border-gray-200 p-4 dark:border-gray-700">
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.unifiedGateway.lane') }} {{ laneIndex + 1 }}</h3>
                  <button v-if="document.lanes.length > 1" class="text-xs text-red-600" @click="removeLane(laneIndex)">{{ t('common.delete') }}</button>
                </div>
                <div class="mt-3 grid gap-3 md:grid-cols-3">
                  <label class="field-label">{{ t('admin.unifiedGateway.laneCode') }}<input v-model.trim="lane.code" class="input mt-1" placeholder="chatgpt-plus" /></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.laneName') }}<input v-model.trim="lane.name" class="input mt-1" placeholder="ChatGPT Plus" /></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.pricingModel') }}<select v-model="lane.profile.pricing_model" class="input mt-1"><option v-for="model in (meta?.supported_pricing_models ?? [])" :key="model" :value="model">{{ model }}</option></select></label>
                </div>
                <div class="mt-3 grid gap-3 md:grid-cols-2">
                  <label class="field-label">{{ t('admin.unifiedGateway.pricingSourceGroup') }}<select v-model="lane.pricing_source_group_id" class="input mt-1"><option value="">{{ t('admin.unifiedGateway.noPricingImport') }}</option><option v-for="group in options?.items.pricing_source_groups ?? []" :key="group.id" :value="group.id">{{ group.name }} ({{ group.id }})</option></select></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.pricingSourceRevision') }}<input v-model.trim="lane.pricing_source_revision" class="input mt-1" placeholder="sha256:..." /><span v-if="pricingImports[lane.id]" class="mt-1 block text-[11px] text-gray-500">{{ pricingImports[lane.id] }}</span></label>
                  <div class="self-end"><button type="button" class="btn btn-secondary text-xs" :disabled="saving || !draftId || !lane.pricing_source_group_id" @click="previewPricingImport(lane)">{{ t('admin.unifiedGateway.previewImport') }}</button><button v-if="pricingImportPreviews[lane.id]" type="button" class="ml-2 btn btn-primary text-xs" :disabled="saving" @click="applyPricingImport(lane)">{{ t('admin.unifiedGateway.confirmImport') }}</button><span v-if="pricingImportPreviews[lane.id]" class="mt-1 block text-[11px] text-gray-500">{{ pricingImportPreviews[lane.id].import_digest }}</span></div>
                  <div v-if="pricingImportPreviews[lane.id]" class="col-span-full rounded-lg border border-amber-200 bg-amber-50 p-3 text-[11px] text-amber-950 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-100">
                    <div class="font-medium">{{ t('admin.unifiedGateway.importPreviewDetails') }}</div>
                    <div class="mt-2 grid gap-1 md:grid-cols-3">
                      <span>{{ t('admin.unifiedGateway.pricingModel') }}: {{ pricingImportPreviews[lane.id].profile.pricing_model }}</span>
                      <span>{{ t('admin.unifiedGateway.billingMode') }}: {{ pricingImportPreviews[lane.id].profile.billing_mode }}</span>
                      <span>{{ t('admin.unifiedGateway.rateMode') }}: {{ pricingImportPreviews[lane.id].profile.rate_mode }}</span>
                      <span>{{ t('admin.unifiedGateway.rateBasis') }}: {{ pricingImportPreviews[lane.id].profile.rate_basis }}</span>
                      <span>{{ t('admin.unifiedGateway.schema') }}: {{ pricingImportPreviews[lane.id].profile.pricing_schema_id }}</span>
                      <span>{{ t('admin.unifiedGateway.baseSemantics') }}: {{ pricingImportPreviews[lane.id].profile.base_price_semantics }}</span>
                      <span>{{ t('admin.unifiedGateway.basePrice') }}: {{ pricingImportPreviews[lane.id].profile.provider_base_unit_price ?? '-' }}</span>
                      <span>{{ t('admin.unifiedGateway.upstreamMultiplier') }}: {{ pricingImportPreviews[lane.id].profile.manual_upstream_multiplier ?? '-' }}</span>
                      <span>{{ t('admin.unifiedGateway.markup') }}: {{ pricingImportPreviews[lane.id].profile.user_markup_multiplier ?? '-' }}</span>
                      <span>{{ t('admin.unifiedGateway.finalUserPrice') }}: {{ pricingImportPreviews[lane.id].profile.final_user_unit_price ?? '-' }}</span>
                      <span>{{ t('admin.unifiedGateway.pricingSourceRevision') }}: {{ pricingImportPreviews[lane.id].source_revision }}</span>
                      <span>{{ t('admin.unifiedGateway.importDigest') }}: {{ pricingImportPreviews[lane.id].import_digest }}</span>
                    </div>
                  </div>
                </div>

                <div class="mt-4 grid gap-3 md:grid-cols-4">
                  <label class="field-label">{{ t('admin.unifiedGateway.billingMode') }}<select data-testid="gateway-advanced-billing-mode" v-model="lane.profile.billing_mode" class="input mt-1" @change="syncPricingMode(lane)"><option v-for="mode in (meta?.supported_billing_modes ?? [])" :key="mode" :value="mode">{{ mode }}</option></select></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.rateMode') }}<select v-model="lane.profile.rate_mode" class="input mt-1"><option v-for="mode in (meta?.supported_rate_modes ?? [])" :key="mode" :value="mode">{{ mode }}</option></select></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.rateBasis') }}<select v-model="lane.profile.rate_basis" class="input mt-1"><option v-for="basis in (meta?.supported_rate_bases ?? [])" :key="basis" :value="basis">{{ basis }}</option></select></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.schema') }}<select v-model="lane.profile.pricing_schema_id" class="input mt-1" @change="syncPricingMode(lane)"><option v-for="schema in schemasFor(lane.profile.billing_mode)" :key="schema" :value="schema">{{ schema }}</option></select></label>
                </div>

                <div class="mt-4 grid gap-3 md:grid-cols-4">
                  <label class="field-label">{{ t('admin.unifiedGateway.baseSemantics') }}<select v-model="lane.profile.base_price_semantics" class="input mt-1" @change="syncPricingMode(lane)"><option value="provider_base">provider_base</option><option value="final_user_price">final_user_price</option></select></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.basePrice') }}<input v-model.trim="lane.profile.provider_base_unit_price" class="input mt-1" placeholder="0.000010000000" /></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.manualBasePrice') }}<input v-model.trim="lane.profile.manual_base_unit_price" class="input mt-1" placeholder="0.000010000000" /></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.upstreamMultiplier') }}<input v-model.trim="lane.profile.manual_upstream_multiplier" class="input mt-1" placeholder="1.000000" /></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.markup') }}<input v-model.trim="lane.profile.user_markup_multiplier" class="input mt-1" placeholder="1.200000" /></label>
                  <label v-if="lane.profile.base_price_semantics === 'final_user_price'" class="field-label">{{ t('admin.unifiedGateway.finalUserPrice') }}<input v-model.trim="lane.profile.final_user_unit_price" class="input mt-1" placeholder="0.00100000" /></label>
                </div>
                <div class="mt-3 grid gap-3 md:grid-cols-4">
                  <label class="field-label">{{ t('admin.unifiedGateway.fixedFee') }}<input v-model.trim="lane.profile.fixed_fee" class="input mt-1" placeholder="0" /></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.minimumCharge') }}<input v-model.trim="lane.profile.minimum_charge" class="input mt-1" placeholder="0" /></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.precision') }}<input v-model.number="lane.profile.precision" type="number" min="0" max="12" class="input mt-1" /></label>
                  <label class="field-label md:col-span-2">{{ t('admin.unifiedGateway.fallbackReason') }}<input v-model.trim="lane.profile.fallback_reason" class="input mt-1" :placeholder="t('admin.unifiedGateway.fallbackPlaceholder')" /></label>
                </div>
                <div v-if="lane.profile.manual_pricing_rules" class="mt-3 grid gap-3 rounded-lg border border-teal-100 bg-teal-50 p-3 md:grid-cols-3 dark:border-teal-900 dark:bg-teal-950/30">
                  <label class="field-label">{{ t('admin.unifiedGateway.manualRuleUnit') }}<select v-model="lane.profile.manual_pricing_rules.unit" class="input mt-1"><option value="request">request</option><option value="image">image</option><option value="video_task">video_task</option></select></label>
                  <label class="field-label">{{ t('admin.unifiedGateway.manualRulePrice') }}<input v-model.trim="lane.profile.manual_pricing_rules.unit_price" class="input mt-1" placeholder="0.01000000" /></label>
                  <p class="self-end text-xs text-gray-500">{{ t('admin.unifiedGateway.manualRuleHint') }}</p>
                </div>

                <div class="mt-5 space-y-3 border-t border-gray-100 pt-4 dark:border-gray-700">
                  <div class="flex items-center justify-between"><h4 class="text-sm font-medium">{{ t('admin.unifiedGateway.targets') }}</h4><button class="btn btn-secondary text-xs" @click="addTarget(lane)">{{ t('admin.unifiedGateway.addTarget') }}</button></div>
                  <div v-for="(target, targetIndex) in lane.targets" :key="target.id" class="rounded-lg bg-gray-50 p-3 dark:bg-gray-800/60">
                    <div class="mb-2 flex items-center justify-between"><span class="text-xs text-gray-500">{{ t('admin.unifiedGateway.targetNumber') }} {{ targetIndex + 1 }}</span><button v-if="lane.targets.length > 1" class="text-xs text-red-600" @click="removeTarget(lane, targetIndex)">{{ t('common.delete') }}</button></div>
                    <div class="grid gap-3 md:grid-cols-3">
                      <label class="field-label">{{ t('admin.unifiedGateway.provider') }}<input v-model.trim="target.provider_identity" class="input mt-1" placeholder="openai-chatgpt / volcengine-ark" /></label>
                      <label class="field-label">{{ t('admin.unifiedGateway.upstreamModel') }}<input v-model.trim="target.upstream_model" class="input mt-1" placeholder="gpt-5.5 / doubao-seed" /></label>
                      <label class="field-label">{{ t('admin.unifiedGateway.targetEndpoint') }}<select v-model="target.endpoint" class="input mt-1"><option v-for="endpoint in (meta?.supported_endpoints ?? [])" :key="endpoint" :value="endpoint">{{ endpoint }}</option></select></label>
                      <label class="field-label">{{ t('admin.unifiedGateway.priority') }}<input v-model.number="target.priority" type="number" class="input mt-1" /></label>
                    </div>
                    <div v-for="(binding, bindingIndex) in target.bindings" :key="binding.id" class="mt-3 grid gap-3 md:grid-cols-[minmax(0,1fr)_160px_120px_100px_60px]">
                      <label class="field-label">{{ t('admin.unifiedGateway.account') }}<select v-model="binding.account_id" class="input mt-1" @change="syncBindingFromAccount(binding)"><option value="">{{ t('admin.unifiedGateway.selectAccount') }}</option><option v-for="account in options?.items.accounts ?? []" :key="account.id" :value="account.id">{{ account.name }} · {{ account.platform }} · {{ account.id }}</option></select><span v-if="accountOption(binding.account_id)" class="mt-1 block text-[11px] text-gray-500">{{ accountOption(binding.account_id)?.status }} · {{ accountOption(binding.account_id)?.schedulable ? t('admin.unifiedGateway.schedulable') : t('admin.unifiedGateway.unschedulable') }} · {{ t('admin.unifiedGateway.eligibility') }}: {{ binding.eligibility }}<span v-if="accountOption(binding.account_id)?.capabilities?.length"> · {{ accountOption(binding.account_id)?.capabilities?.join(', ') }}</span></span><span v-if="canProbe && binding.id && binding.account_id" class="mt-1 block text-[11px]"><button data-testid="gateway-probe" type="button" class="text-teal-700" @click="probeBinding(binding)">{{ t('admin.unifiedGateway.probeCapabilityAction') }}</button><span v-if="probeResults[binding.id]" class="ml-2 text-gray-500">{{ probeResults[binding.id] }}</span></span><span v-else-if="meta?.capabilities?.probe !== true" data-testid="gateway-probe-unsupported" class="mt-1 block text-[11px] text-amber-700 dark:text-amber-300">{{ t('admin.unifiedGateway.probeUnsupported') }}</span><span v-else class="mt-1 block text-[11px] text-gray-500">{{ t('admin.unifiedGateway.probeManualConfirmation') }}</span></label>
                      <label class="field-label">{{ t('admin.unifiedGateway.bindingEndpoint') }}<select v-model="binding.endpoint" class="input mt-1"><option value="">{{ t('admin.unifiedGateway.inheritTargetEndpoint') }}</option><option v-for="endpoint in (meta?.supported_endpoints ?? [])" :key="endpoint" :value="endpoint">{{ endpoint }}</option></select></label>
                      <label class="field-label">{{ t('admin.unifiedGateway.bindingPriority') }}<input v-model.number="binding.priority" type="number" class="input mt-1" /></label>
                      <label class="mt-6 flex items-center gap-2 text-xs"><input v-model="binding.enabled" type="checkbox" />{{ t('common.enabled') }}</label>
                      <button v-if="target.bindings.length > 1" class="mt-6 text-xs text-red-600" @click="removeBinding(target, bindingIndex)">{{ t('common.delete') }}</button>
                    </div>
                    <button class="mt-3 text-xs text-teal-600" @click="addBinding(target)">{{ t('admin.unifiedGateway.addBinding') }}</button>
                  </div>
                </div>
              </div>
                <button class="btn btn-secondary w-full" @click="addLane">{{ t('admin.unifiedGateway.addLane') }}</button>
              </div>
            </div>
            </fieldset>
          </section>

          <aside class="space-y-6">
            <section v-if="advancedEditorOpen" class="card space-y-3 p-5">
              <div class="flex items-center justify-between"><h2 class="text-lg font-semibold">{{ t('admin.unifiedGateway.validation') }}</h2><button class="btn btn-secondary text-xs" :disabled="saving || !draftId" @click="validateCurrent">{{ t('admin.unifiedGateway.validate') }}</button></div>
              <p v-if="!draftId" class="text-sm text-gray-500">{{ t('admin.unifiedGateway.saveFirst') }}</p>
              <div v-if="validation" class="space-y-2 text-sm"><p :class="validation.valid ? 'text-green-600' : 'text-red-600'">{{ validation.valid ? t('admin.unifiedGateway.ready') : t('admin.unifiedGateway.blocked') }}</p><ul v-if="validation.blockers.length" class="list-disc space-y-1 pl-5 text-red-600"><li v-for="issue in validation.blockers" :key="`${issue.code}-${issue.path}`">{{ issue.path }}: {{ issue.message }}</li></ul><ul v-if="validation.warnings.length" class="list-disc space-y-1 pl-5 text-amber-600"><li v-for="issue in validation.warnings" :key="`${issue.code}-${issue.path}`">{{ issue.path }}: {{ issue.message }}</li></ul></div>
            </section>

            <section v-if="advancedEditorOpen" class="card space-y-3 p-5">
              <div class="flex items-center justify-between"><h2 class="text-lg font-semibold">{{ t('admin.unifiedGateway.preview') }}</h2><button class="btn btn-secondary text-xs" :disabled="saving || !draftId" @click="previewCurrent">{{ t('admin.unifiedGateway.previewAction') }}</button></div>
              <div class="grid gap-3 md:grid-cols-3">
                <label class="field-label">{{ t('admin.unifiedGateway.previewLane') }}<select v-model="previewLaneId" class="input mt-1" @change="syncPreviewSelection"><option v-for="lane in document.lanes" :key="lane.id" :value="lane.id">{{ lane.name || lane.code || lane.id }}</option></select></label>
                <label class="field-label">{{ t('admin.unifiedGateway.previewTarget') }}<select v-model="previewTargetId" class="input mt-1" @change="syncPreviewSelection"><option v-for="target in previewTargetOptions" :key="target.id" :value="target.id">{{ target.provider_identity || target.id }} · {{ target.upstream_model || '-' }}</option></select></label>
                <label class="field-label">{{ t('admin.unifiedGateway.previewBinding') }}<select v-model="previewBindingId" class="input mt-1"><option v-for="binding in previewBindingOptions" :key="binding.id" :value="binding.id">{{ binding.account_id || binding.id }}</option></select></label>
              </div>
              <div class="grid gap-3 md:grid-cols-3"><label class="field-label">{{ t('admin.unifiedGateway.inputTokens') }}<input v-model="previewInput" class="input mt-1" /></label><label class="field-label">{{ t('admin.unifiedGateway.outputTokens') }}<input v-model="previewOutput" class="input mt-1" /></label><label class="field-label">{{ t('admin.unifiedGateway.deliveryState') }}<select v-model="previewDeliveryState" class="input mt-1"><option value="success">success</option><option value="failed">failed</option></select></label></div>
              <div v-if="preview" class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-gray-800/60"><div class="flex justify-between"><span>{{ t('admin.unifiedGateway.estimatedCharge') }}</span><strong>{{ preview.estimated_charge }} {{ preview.currency }}</strong></div><div class="mt-1 text-xs text-gray-500">{{ t('admin.unifiedGateway.previewMatchedRoute') }}: {{ [preview.account_display_name, preview.provider_identity, preview.upstream_model].filter(Boolean).join(' · ') }} · {{ t('admin.unifiedGateway.billingUnit') }}: {{ preview.billing_unit }}</div><div class="mt-1 text-xs text-gray-500">{{ t('admin.unifiedGateway.rateSource') }}: {{ preview.resolved_rate_source }} · {{ t('admin.unifiedGateway.effectiveMultiplier') }}: {{ preview.effective_multiplier }} · {{ t('admin.unifiedGateway.failureCharge') }}: {{ preview.failure_charge_amount }} {{ preview.currency }}</div><div class="mt-1 text-xs text-gray-500">{{ t('admin.unifiedGateway.dryRun') }} ({{ preview.preview_digest }})</div></div>
            </section>

            <section class="card space-y-3 p-5">
              <h2 class="text-lg font-semibold">{{ t('admin.unifiedGateway.publish') }}</h2>
              <p class="text-sm text-gray-500">{{ t('admin.unifiedGateway.publishHint') }}</p>
              <div v-if="validation" data-testid="gateway-publish-validation" class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-gray-800/60"><p :class="validation.valid ? 'text-green-600' : 'text-red-600'">{{ validation.valid ? t('admin.unifiedGateway.ready') : t('admin.unifiedGateway.blocked') }}</p><ul v-if="validation.blockers.length" class="mt-2 list-disc space-y-1 pl-5 text-red-600"><li v-for="issue in validation.blockers" :key="`${issue.code}-${issue.path}`">{{ issue.message }}</li></ul></div>
              <button data-testid="gateway-publish" class="btn btn-primary w-full" :disabled="saving || !canWrite || !document.public_model" @click="publishCurrent">{{ t('admin.unifiedGateway.publishAction') }}</button>
              <p class="text-xs text-gray-500">{{ runtimeStatusMessage }}</p>
            </section>

            <details class="card p-5">
              <summary class="cursor-pointer text-lg font-semibold">{{ t('admin.unifiedGateway.publishedConfigs') }}</summary>
              <div v-if="configs.length === 0" class="mt-3 text-sm text-gray-500">{{ t('admin.unifiedGateway.noConfigs') }}</div>
              <div v-for="item in configs" :key="item.id" class="mt-3 rounded-lg border border-gray-100 p-3 text-sm dark:border-gray-700"><div class="flex justify-between gap-2"><button class="truncate text-left font-semibold text-teal-700" :disabled="saving || !canWrite" @click="editPublished(item)">{{ item.public_model }}</button><span :class="item.readiness === 'ready' ? 'text-green-600' : 'text-amber-600'">{{ item.lifecycle === 'published' ? t('admin.unifiedGateway.enabled') : t('admin.unifiedGateway.disabledLabel') }}</span></div><div class="mt-1 text-xs text-gray-500">{{ endpointLabel(item.endpoint) }} · {{ t('admin.unifiedGateway.configuredSummary', { lanes: item.lanes.length, markup: item.lanes[0]?.profile.user_markup_multiplier || '1.000000' }) }}</div><div class="mt-3 flex flex-wrap gap-2"><button class="btn btn-secondary text-xs" :disabled="saving || !canWrite" @click="editPublished(item)">{{ t('admin.unifiedGateway.editDraft') }}</button><button v-if="item.lifecycle === 'published'" class="btn btn-secondary text-xs" :disabled="saving || !canWrite" @click="disablePublished(item)">{{ t('admin.unifiedGateway.disableAction') }}</button><button v-if="item.lifecycle === 'disabled'" class="btn btn-secondary text-xs" :disabled="saving || !canWrite" @click="restorePublished(item)">{{ t('admin.unifiedGateway.restoreAction') }}</button><button class="btn btn-secondary text-xs" :disabled="saving" @click="showRevisions(item)">{{ t('admin.unifiedGateway.showTechnicalDetails') }}</button></div><details v-if="selectedConfigId === item.id && revisions.length" class="mt-3 rounded bg-gray-50 p-2 text-xs dark:bg-gray-800/60"><summary class="cursor-pointer font-medium">{{ t('admin.unifiedGateway.versionDetails') }}</summary><div v-for="revision in revisions" :key="revision.revision" class="flex items-center justify-between gap-2 py-1"><span>{{ t('admin.unifiedGateway.versionNumber', { number: revision.revision }) }} · {{ revision.lifecycle }} · {{ revision.reason || '-' }}</span><button v-if="item.lifecycle === 'disabled' && revision.lifecycle === 'published'" class="text-teal-700" :disabled="!canWrite" @click="restorePublished(item, revision.revision)">{{ t('admin.unifiedGateway.restoreAction') }}</button></div><div class="mt-3 border-t border-gray-200 pt-2 dark:border-gray-700"><div class="font-medium">{{ t('admin.unifiedGateway.snapshots') }}</div><div v-if="snapshots.length === 0" class="mt-1 text-gray-500">{{ t('admin.unifiedGateway.noSnapshots') }}</div><div v-for="snapshot in snapshots" :key="snapshot.id" class="mt-1 text-gray-500">{{ snapshot.status }} · {{ snapshot.provider_identity }} · {{ snapshot.user_charge }} {{ snapshot.currency }}</div></div></details></div>
            </details>
          </aside>
        </div>
        </template>
      </div>
      <TotpStepUpDialog :controller="gatewayStepUp" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpBlocked, isStepUpCancelled, stepUpBlockReason, useStepUp } from '@/composables/useStepUp'
import type { UnifiedGatewayAccountBinding, UnifiedGatewayBillingLane, UnifiedGatewayBillingMode, UnifiedGatewayConfig, UnifiedGatewayMeta, UnifiedGatewayModelCandidate, UnifiedGatewayOptionsPage, UnifiedGatewayPricingImportResult, UnifiedGatewayPreviewResult, UnifiedGatewayRevision, UnifiedGatewayRouteTarget, UnifiedGatewaySnapshotView, UnifiedGatewayValidationResult, UnifiedGatewayEndpoint, UnifiedGatewayRateBasis } from '@/types/unifiedGateway'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const meta = ref<UnifiedGatewayMeta | null>(null)
const options = ref<UnifiedGatewayOptionsPage | null>(null)
const modelCandidates = ref<UnifiedGatewayModelCandidate[]>([])
const candidateSearch = ref('')
const configs = ref<UnifiedGatewayConfig[]>([])
const draftId = ref('')
const draftRevision = ref(0)
const configRevision = ref(0)
const validation = ref<UnifiedGatewayValidationResult | null>(null)
const preview = ref<UnifiedGatewayPreviewResult | null>(null)
const previewInput = ref('1000')
const previewOutput = ref('500')
const previewDeliveryState = ref<'success' | 'failed'>('success')
const previewLaneId = ref('')
const previewTargetId = ref('')
const previewBindingId = ref('')
const metaLoadFailed = ref(false)
const selectedConfigId = ref('')
const revisions = ref<UnifiedGatewayRevision[]>([])
const snapshots = ref<UnifiedGatewaySnapshotView[]>([])
const probeResults = ref<Record<string, string>>({})
const pricingImports = ref<Record<string, string>>({})
const pricingImportPreviews = ref<Record<string, UnifiedGatewayPricingImportResult>>({})
const advancedEditorOpen = ref(false)
const unifiedMarkupMultiplier = ref('1.000000')
const bulkConfiguring = ref(false)
const bulkSummary = ref('')
const gatewayStepUp = useStepUp()

interface UnifiedGatewayCandidateGroup {
  key: string
  public_model: string
  endpoint: UnifiedGatewayEndpoint
  candidates: UnifiedGatewayModelCandidate[]
  eligible_count: number
}

function newBinding(): UnifiedGatewayAccountBinding { return { id: `binding_${crypto.randomUUID?.() ?? Date.now()}`, account_id: '', endpoint: '', schedulable: true, eligibility: 'unknown', priority: 10, enabled: true, revision: 1 } }
function newTarget(endpoint: UnifiedGatewayEndpoint = 'chat_completions'): UnifiedGatewayRouteTarget { return { id: `target_${crypto.randomUUID?.() ?? Date.now()}`, provider_identity: '', upstream_model: '', endpoint, priority: 10, bindings: [newBinding()] } }
function newLane(endpoint: UnifiedGatewayEndpoint = 'chat_completions'): UnifiedGatewayBillingLane {
  const billingMode = billingModeForEndpoint(endpoint)
  return { id: `lane_${crypto.randomUUID?.() ?? Date.now()}`, code: '', name: '', selection_strategy: 'fixed_priority', profile: { id: `profile_${crypto.randomUUID?.() ?? Date.now()}`, version: '1', pricing_model: pricingModelForBillingMode(billingMode), billing_mode: billingMode, rate_mode: 'manual_only', rate_basis: billingMode, pricing_schema_id: schemasFor(billingMode)[0], currency: 'USD', base_price_semantics: 'provider_base', provider_base_unit_price: null, manual_base_unit_price: null, manual_upstream_multiplier: null, user_markup_multiplier: '1.000000', final_user_unit_price: null, fixed_fee: '0', minimum_charge: '0', rounding_mode: 'half_up', precision: 8, fallback_reason: defaultPricingReason(), manual_pricing_rules: null, charge_trigger: 'success_delivery', failure_charge: 'zero' }, targets: [newTarget(endpoint)] }
}
function emptyDocument(): UnifiedGatewayConfig { return { access_group_id: '', public_model: '', endpoint: 'chat_completions', lanes: [newLane()] } }
const document = reactive<UnifiedGatewayConfig>(emptyDocument())
const primaryLane = computed(() => document.lanes[0] ?? null)
const managementAvailable = computed(() => meta.value?.admin_ui_enabled === true && meta.value?.migration_ready === true && meta.value?.capabilities?.read === true)
const writeCapability = computed<boolean | null>(() => {
  const capabilities = meta.value?.capabilities
  if (!capabilities) return null
  if (typeof capabilities.write === 'boolean') return capabilities.write
  if (typeof capabilities.draft === 'boolean' || typeof capabilities.publish === 'boolean') return capabilities.draft === true && capabilities.publish === true
  return null
})
const canWrite = computed(() => managementAvailable.value && writeCapability.value === true)
const canProbe = computed(() => canWrite.value && meta.value?.capabilities?.probe === true)
const readOnly = computed(() => managementAvailable.value && !canWrite.value)
const currentRuntimeEffective = computed<boolean | null>(() => {
  if (document.runtime_effective !== undefined) return document.runtime_effective
  const selected = configs.value.find(item => item.id === selectedConfigId.value)
  if (selected?.runtime_effective !== undefined) return selected.runtime_effective
  if (configs.value.length === 1 && configs.value[0].runtime_effective !== undefined) return configs.value[0].runtime_effective
  return meta.value?.runtime_effective ?? null
})
const managementStatusLabel = computed(() => !managementAvailable.value ? t('admin.unifiedGateway.statusManagementUnavailable') : readOnly.value ? t('admin.unifiedGateway.statusReadOnly') : t('admin.unifiedGateway.statusManagementAvailable'))
const managementStatusClass = computed(() => !managementAvailable.value ? 'text-red-600' : readOnly.value ? 'text-amber-600' : 'text-green-600')
const gateMessage = computed(() => metaLoadFailed.value || !meta.value ? t('admin.unifiedGateway.metaUnavailable') : !meta.value.admin_ui_enabled ? t('admin.unifiedGateway.uiDisabled') : !meta.value.migration_ready ? t('admin.unifiedGateway.migrationNotReady') : meta.value.capabilities?.read !== true ? t('admin.unifiedGateway.capabilityUnavailable') : !canWrite.value ? t('admin.unifiedGateway.statusReadOnly') : meta.value.runtime_enabled ? t('admin.unifiedGateway.statusManagementAvailable') : t('admin.unifiedGateway.runtimeDisabled'))
const runtimeStatusMessage = computed(() => meta.value?.runtime_enabled === true ? t('admin.unifiedGateway.runtimeEnabled') : meta.value?.runtime_enabled === false ? t('admin.unifiedGateway.runtimeDisabled') : t('admin.unifiedGateway.runtimeUnknown'))
const modelCandidateGroups = computed<UnifiedGatewayCandidateGroup[]>(() => {
  const groups = new Map<string, UnifiedGatewayCandidateGroup>()
  for (const candidate of modelCandidates.value) {
    const key = `${candidate.public_model}::${candidate.endpoint}`
    const group = groups.get(key) ?? {
      key,
      public_model: candidate.public_model,
      endpoint: candidate.endpoint,
      candidates: [],
      eligible_count: 0,
    }
    group.candidates.push(candidate)
    if (candidate.runtime_eligible) group.eligible_count += 1
    groups.set(key, group)
  }
  return Array.from(groups.values()).sort((left, right) => left.public_model.localeCompare(right.public_model))
})
const filteredModelCandidateGroups = computed(() => {
  const query = candidateSearch.value.trim().toLowerCase()
  if (!query) return modelCandidateGroups.value
  return modelCandidateGroups.value.filter(group => [group.public_model, group.endpoint, providerSummary(group)].some(value => value.toLowerCase().includes(query)))
})
const publishedModelKeys = computed(() => new Set(configs.value.filter(item => item.lifecycle === 'published').map(item => `${item.public_model}::${item.endpoint}`)))
const availableModelCount = computed(() => modelCandidateGroups.value.filter(group => group.eligible_count > 0).length)
const publishedModelCount = computed(() => publishedModelKeys.value.size)
const needsActionModelCount = computed(() => Math.max(availableModelCount.value - publishedModelCount.value, 0))
const selectedGroupName = computed(() => options.value?.items.access_groups.find(group => group.id === document.access_group_id)?.name ?? '')
const unifiedGroupName = computed(() => selectedGroupName.value || ((options.value?.items.access_groups.length ?? 0) === 1 ? options.value!.items.access_groups[0].name : ''))
const selectedRouteSummary = computed(() => {
  const target = primaryLane.value?.targets[0]
  const binding = target?.bindings[0]
  if (!target || !binding?.account_id) return t('admin.unifiedGateway.routeNotSelected')
  const account = accountOption(binding.account_id)
  return [account?.name, target.provider_identity, target.upstream_model].filter(Boolean).join(' · ') || t('admin.unifiedGateway.routeNotSelected')
})
const backupRouteCount = computed(() => Math.max(document.lanes.length - 1, 0))
const unifiedBaseUrl = computed(() => {
  if (typeof window === 'undefined' || !window.location?.origin) return '/unified/v1'
  return `${window.location.origin}/unified/v1`
})

function booleanStatus(value: boolean | null | undefined) { return value === true ? t('admin.unifiedGateway.statusEnabled') : value === false ? t('admin.unifiedGateway.statusDisabled') : t('admin.unifiedGateway.statusUnknown') }
function readinessStatus(value: boolean | null | undefined) { return value === true ? t('admin.unifiedGateway.statusReady') : value === false ? t('admin.unifiedGateway.statusNotReady') : t('admin.unifiedGateway.statusUnknown') }
function capabilityStatus(value: boolean | null | undefined) { return value === true ? t('admin.unifiedGateway.statusAvailable') : value === false ? t('admin.unifiedGateway.statusUnavailable') : t('admin.unifiedGateway.statusUnknown') }
function runtimeEffectiveLabel(value: boolean | null | undefined) { return value === true ? t('admin.unifiedGateway.runtimeEffectiveEnabled') : value === false ? t('admin.unifiedGateway.runtimeEffectiveDisabled') : t('admin.unifiedGateway.runtimeEffectiveUnknown') }
function endpointLabel(endpoint: UnifiedGatewayEndpoint) {
  const labels: Record<UnifiedGatewayEndpoint, string> = {
    chat_completions: t('admin.unifiedGateway.endpointChatCompletions'),
    responses: t('admin.unifiedGateway.endpointResponses'),
    images_generations: t('admin.unifiedGateway.endpointImageGeneration'),
    images_edits: t('admin.unifiedGateway.endpointImageEdit'),
    videos: t('admin.unifiedGateway.endpointVideo'),
  }
  return labels[endpoint] ?? endpoint
}
function providerSummary(group: UnifiedGatewayCandidateGroup) {
  return Array.from(new Set(group.candidates.map(candidate => candidate.provider_identity).filter(Boolean))).slice(0, 3).join(', ') || t('admin.unifiedGateway.providerUnknown')
}
function groupStatusLabel(group: UnifiedGatewayCandidateGroup) {
  if (!group.eligible_count) return t('admin.unifiedGateway.modelStatusBlocked')
  if (publishedModelKeys.value.has(group.key)) return t('admin.unifiedGateway.modelStatusPublished')
  return t('admin.unifiedGateway.modelStatusReadyToConfigure')
}
function groupStatusClass(group: UnifiedGatewayCandidateGroup) {
  if (!group.eligible_count) return 'text-amber-600'
  return publishedModelKeys.value.has(group.key) ? 'text-green-600' : 'text-teal-700'
}

function billingModeForEndpoint(endpoint: UnifiedGatewayEndpoint): UnifiedGatewayBillingMode {
  if (endpoint === 'images_generations' || endpoint === 'images_edits') return 'image'
  if (endpoint === 'videos') return 'video'
  return 'token'
}

function rateBasisForBillingMode(mode: UnifiedGatewayBillingMode): UnifiedGatewayRateBasis {
  return mode
}

function pricingModelForBillingMode(mode: UnifiedGatewayBillingMode) {
  if (mode === 'image') return 'image'
  if (mode === 'video') return 'video'
  if (mode === 'per_request') return 'fixed_request'
  return 'provider_metered'
}

function defaultPricingReason() { return t('admin.unifiedGateway.defaultFallbackReason') }
function ensurePricingReason(lane: UnifiedGatewayBillingLane) {
  if (!lane.profile.fallback_reason?.trim()) lane.profile.fallback_reason = defaultPricingReason()
}

function billingModeLabel(mode: string) {
  const labels: Record<string, string> = {
    token: t('admin.unifiedGateway.billingModeToken'),
    per_request: t('admin.unifiedGateway.billingModePerRequest'),
    image: t('admin.unifiedGateway.billingModeImage'),
    video: t('admin.unifiedGateway.billingModeVideo'),
  }
  return labels[mode] ?? mode
}

function resetDocument() {
  const next = emptyDocument()
  const defaultGroup = options.value?.items.access_groups.length === 1 ? options.value.items.access_groups[0] : undefined
  if (defaultGroup) next.access_group_id = defaultGroup.id
  Object.assign(document, next)
  draftId.value = ''
  draftRevision.value = 0
  configRevision.value = 0
  validation.value = null
  preview.value = null
  selectedConfigId.value = ''
  revisions.value = []
  snapshots.value = []
  advancedEditorOpen.value = false
  unifiedMarkupMultiplier.value = '1.000000'
  bulkSummary.value = ''
}
function addLane() { document.lanes.push(newLane(document.endpoint)) }
function removeLane(index: number) { document.lanes.splice(index, 1) }
function addTarget(lane: UnifiedGatewayBillingLane) { lane.targets.push(newTarget(document.endpoint)) }
function removeTarget(lane: UnifiedGatewayBillingLane, index: number) { if (lane.targets.length > 1) lane.targets.splice(index, 1) }
function addBinding(target: UnifiedGatewayRouteTarget) { target.bindings.push(newBinding()) }
function removeBinding(target: UnifiedGatewayRouteTarget, index: number) { if (target.bindings.length > 1) target.bindings.splice(index, 1) }
function accountOption(id: string) { return options.value?.items.accounts.find(account => account.id === id) }
function syncBindingFromAccount(binding: UnifiedGatewayAccountBinding) { const account = accountOption(binding.account_id); if (!account) return; binding.schedulable = account.schedulable === true; binding.eligibility = account.status === 'active' && account.schedulable ? 'eligible' : 'ineligible' }
function updateSimpleBillingMode(lane: UnifiedGatewayBillingLane, mode: string) {
  if (mode !== 'token' && mode !== 'per_request' && mode !== 'image' && mode !== 'video') return
  lane.profile.billing_mode = mode
  lane.profile.rate_basis = rateBasisForBillingMode(mode)
  lane.profile.pricing_model = pricingModelForBillingMode(mode)
  syncPricingMode(lane)
  ensurePricingReason(lane)
}
function applyUnifiedMarkup() {
  const value = unifiedMarkupMultiplier.value.trim() || '1.000000'
  for (const lane of document.lanes) {
    if (lane.profile.base_price_semantics === 'provider_base' && !lane.profile.manual_pricing_rules) lane.profile.user_markup_multiplier = value
  }
}
function syncUnifiedMarkupFromDocument() {
  const value = document.lanes.map(lane => lane.profile.user_markup_multiplier).find(item => item && item.trim())
  unifiedMarkupMultiplier.value = value || '1.000000'
  applyUnifiedMarkup()
}
function simpleTestId(prefix: string, laneIndex: number) { return laneIndex === 0 ? prefix : `${prefix}-${laneIndex}` }
function laneRouteSummary(lane: UnifiedGatewayBillingLane) {
  const target = lane.targets[0]
  const binding = target?.bindings[0]
  if (!target || !binding?.account_id) return t('admin.unifiedGateway.routeNotSelected')
  const account = accountOption(binding.account_id)
  return [account?.name, target.provider_identity, target.upstream_model].filter(Boolean).join(' · ') || t('admin.unifiedGateway.routeNotSelected')
}
function useCandidate(candidate: UnifiedGatewayModelCandidate) {
  if (!candidate.runtime_eligible) return
  const sameModelCandidates = modelCandidates.value.filter(item => item.public_model === candidate.public_model && item.endpoint === candidate.endpoint)
  useCandidates([candidate, ...sameModelCandidates.filter(item => item !== candidate)])
}
function buildDocumentFromCandidates(candidates: UnifiedGatewayModelCandidate[]): UnifiedGatewayConfig | null {
  const eligibleCandidates = candidates.filter(candidate => candidate.runtime_eligible)
  if (!eligibleCandidates.length) return null
  const next = emptyDocument()
  const defaultGroup = options.value?.items.access_groups.length === 1 ? options.value.items.access_groups[0] : undefined
  if (defaultGroup) next.access_group_id = defaultGroup.id
  const first = eligibleCandidates[0]
  next.public_model = first.public_model
  next.endpoint = first.endpoint
  next.lanes.splice(0, next.lanes.length)
  for (const [index, candidate] of eligibleCandidates.entries()) {
    const lane = newLane(candidate.endpoint)
    const priority = (index + 1) * 10
    updateSimpleBillingMode(lane, billingModeForEndpoint(candidate.endpoint))
    lane.code = `${candidate.provider_identity}-${candidate.account_id}-${index + 1}`.slice(0, 128)
    lane.name = `${index === 0 ? t('admin.unifiedGateway.primaryRouteName') : t('admin.unifiedGateway.backupRouteName')} · ${candidate.account_name} · ${candidate.provider_identity}`.slice(0, 255)
    lane.targets[0].priority = priority
    const target = lane.targets[0]
    target.provider_identity = candidate.provider_identity
    target.upstream_model = candidate.upstream_model
    target.endpoint = candidate.endpoint
    const binding = target.bindings[0]
    binding.account_id = candidate.account_id
    binding.priority = priority
    binding.schedulable = candidate.schedulable
    binding.eligibility = 'eligible'
    next.lanes.push(lane)
  }
  return next
}
function useCandidates(candidates: UnifiedGatewayModelCandidate[]) {
  if (!canWrite.value) return
  const next = buildDocumentFromCandidates(candidates)
  if (!next) return
  resetDocument()
  Object.assign(document, next)
  applyUnifiedMarkup()
  advancedEditorOpen.value = false
  syncPreviewSelection()
}
function useFirstCandidate(group: UnifiedGatewayCandidateGroup) {
  useCandidates(group.candidates)
}
function schemasFor(mode: string) { if (mode === 'image') return ['image_delivery_v1', 'flat_unit_price_v1']; if (mode === 'video') return ['video_delivery_v1', 'flat_unit_price_v1']; if (mode === 'per_request') return ['request_v1', 'flat_unit_price_v1']; return ['token_v1'] }
function manualRuleUnitFor(mode: string) { if (mode === 'image') return 'image'; if (mode === 'video') return 'video_task'; return 'request' }
function syncPricingMode(lane: UnifiedGatewayBillingLane) {
  const profile = lane.profile
  const allowed = schemasFor(profile.billing_mode)
  if (!allowed.includes(profile.pricing_schema_id)) profile.pricing_schema_id = allowed[0]
  if (profile.pricing_schema_id === 'flat_unit_price_v1') {
    profile.base_price_semantics = 'final_user_price'
    profile.provider_base_unit_price = null
    profile.manual_base_unit_price = null
    profile.manual_upstream_multiplier = null
    profile.user_markup_multiplier = null
    if (!profile.manual_pricing_rules) profile.manual_pricing_rules = { formula_id: 'flat_unit_price', unit: manualRuleUnitFor(profile.billing_mode), unit_price: '' }
    profile.manual_pricing_rules.unit = manualRuleUnitFor(profile.billing_mode) as 'request' | 'image' | 'video_task'
  } else {
    profile.manual_pricing_rules = null
    if (profile.base_price_semantics === 'final_user_price') {
      profile.provider_base_unit_price = null
      profile.manual_base_unit_price = null
      profile.manual_upstream_multiplier = null
      profile.user_markup_multiplier = null
    } else {
      profile.final_user_unit_price = null
      // Leave pricing inputs empty until an administrator explicitly enters or
      // imports a profile. An unconfigured profile must fail closed server-side.
    }
  }
}

const previewLane = computed(() => document.lanes.find(lane => lane.id === previewLaneId.value) ?? document.lanes[0])
const previewTargetOptions = computed(() => previewLane.value?.targets ?? [])
const previewTarget = computed(() => previewTargetOptions.value.find(target => target.id === previewTargetId.value) ?? previewTargetOptions.value[0])
const previewBindingOptions = computed(() => previewTarget.value?.bindings ?? [])

function syncPreviewSelection() {
  const lane = document.lanes.find(item => item.id === previewLaneId.value) ?? document.lanes[0]
  previewLaneId.value = lane?.id ?? ''
  const target = lane?.targets.find(item => item.id === previewTargetId.value) ?? lane?.targets[0]
  previewTargetId.value = target?.id ?? ''
  const binding = target?.bindings.find(item => item.id === previewBindingId.value) ?? target?.bindings[0]
  previewBindingId.value = binding?.id ?? ''
}

watch(() => document.endpoint, (endpoint) => {
  for (const lane of document.lanes) for (const target of lane.targets) if (!target.endpoint) target.endpoint = endpoint
})
watch(() => document.lanes.map(lane => lane.profile.billing_mode), () => {
  for (const lane of document.lanes) {
    syncPricingMode(lane)
    for (const target of lane.targets) if (!target.endpoint) target.endpoint = document.endpoint
  }
})
watch(() => document.lanes, syncPreviewSelection, { deep: true, immediate: true })

async function loadModelCandidates() { if (!managementAvailable.value) return; try { const result = await adminAPI.unifiedGateway.listModelCandidates(); modelCandidates.value = result.items } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.loadFailed')) } }
async function loadPage() { loading.value = true; metaLoadFailed.value = false; try { meta.value = await adminAPI.unifiedGateway.getMeta(); if (managementAvailable.value) { options.value = await adminAPI.unifiedGateway.getOptions(); const page = await adminAPI.unifiedGateway.listConfigs(''); configs.value = page.items; await loadModelCandidates() } else { options.value = null; configs.value = []; modelCandidates.value = [] } } catch (error: any) { meta.value = null; options.value = null; configs.value = []; modelCandidates.value = []; metaLoadFailed.value = true; appStore.showError(error?.message || t('admin.unifiedGateway.loadFailed')) } finally { loading.value = false } }
async function configureAllModels() {
  if (!canWrite.value || bulkConfiguring.value) return
  const pending = modelCandidateGroups.value.filter(group => group.eligible_count > 0 && !publishedModelKeys.value.has(group.key))
  if (!pending.length) return
  if (typeof window !== 'undefined' && !window.confirm(t('admin.unifiedGateway.configureAllConfirm', { count: pending.length }))) return
  bulkConfiguring.value = true
  bulkSummary.value = t('admin.unifiedGateway.configuringProgress', { done: 0, total: pending.length })
  let published = 0
  let blocked = 0
  let cancelled = false
  try {
    for (const [index, group] of pending.entries()) {
      bulkSummary.value = t('admin.unifiedGateway.configuringProgress', { done: index, total: pending.length })
      const next = buildDocumentFromCandidates(group.candidates)
      if (!next) { blocked += 1; continue }
      try {
        const draft = await adminAPI.unifiedGateway.createDraft(next)
        const checked = await adminAPI.unifiedGateway.validateDraft(draft.id, undefined, draft.revision)
        if (!checked.valid || !checked.validation_token) { blocked += 1; continue }
        const result = await gatewayStepUp.run(() => adminAPI.unifiedGateway.publishDraft(draft.id, draft.document.revision ?? 0, checked.validation_token!, 'configure all available models'))
        configs.value = [result, ...configs.value.filter(item => item.id !== result.id)]
        published += 1
      } catch (error: unknown) {
        if (isStepUpCancelled(error)) { cancelled = true; break }
        blocked += 1
      }
    }
    await loadModelCandidates()
    const page = await adminAPI.unifiedGateway.listConfigs('')
    configs.value = page.items
    bulkSummary.value = t('admin.unifiedGateway.configuringDone', { published, blocked, total: pending.length })
    if (!cancelled && published > 0) appStore.showSuccess(t('admin.unifiedGateway.bulkPublished', { count: published }))
  } catch (error: any) {
    bulkSummary.value = ''
    appStore.showError(error?.message || t('admin.unifiedGateway.publishFailed'))
  } finally {
    bulkConfiguring.value = false
    if (cancelled) appStore.showError(t('admin.unifiedGateway.stepUpUnavailable'))
  }
}
async function saveDraft(): Promise<boolean> { if (!canWrite.value) return false; saving.value = true; try { if (!draftId.value) { const draft = await adminAPI.unifiedGateway.createDraft(JSON.parse(JSON.stringify(document))); draftId.value = draft.id; draftRevision.value = draft.revision; configRevision.value = draft.document.revision ?? 0; Object.assign(document, draft.document); } else { const draft = await adminAPI.unifiedGateway.updateDraft(draftId.value, JSON.parse(JSON.stringify(document)), draftRevision.value); draftRevision.value = draft.revision; Object.assign(document, draft.document) } syncUnifiedMarkupFromDocument(); validation.value = null; preview.value = null; appStore.showSuccess(t('admin.unifiedGateway.saved')); return true } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.saveFailed')); return false } finally { saving.value = false } }
async function validateCurrent() { if (!canWrite.value) return; saving.value = true; try { if (!(await saveDraft())) return; validation.value = await adminAPI.unifiedGateway.validateDraft(draftId.value, JSON.parse(JSON.stringify(document)), draftRevision.value) } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.validateFailed')) } finally { saving.value = false } }
async function previewCurrent() { if (!canWrite.value) return; saving.value = true; try { if (!(await saveDraft())) return; syncPreviewSelection(); const lane = previewLane.value; const target = previewTarget.value; const binding = previewBindingOptions.value.find(item => item.id === previewBindingId.value) ?? previewBindingOptions.value[0]; if (!lane || !target || !binding) throw new Error(t('admin.unifiedGateway.previewSelectionRequired')); const request = lane.profile.billing_mode === 'token' ? { lane_id: lane.id, target_id: target.id, binding_id: binding.id, input_tokens: previewInput.value, output_tokens: previewOutput.value, delivery_state: previewDeliveryState.value } : { lane_id: lane.id, target_id: target.id, binding_id: binding.id, units: '1', delivery_state: previewDeliveryState.value }; preview.value = await adminAPI.unifiedGateway.previewDraft(draftId.value, request, JSON.parse(JSON.stringify(document))) } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.previewFailed')) } finally { saving.value = false } }
function operationKey(prefix: string) { if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return `${prefix}-${crypto.randomUUID()}`; return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}` }
function reportSensitiveActionError(error: unknown) { if (isStepUpCancelled(error)) return; if (isStepUpBlocked(error)) { appStore.showError(stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? t('admin.unifiedGateway.stepUpApiKeyForbidden') : t('admin.unifiedGateway.stepUpUnavailable')); return } appStore.showError((error as any)?.message || t('admin.unifiedGateway.publishFailed')) }
async function publishCurrent() { if (!canWrite.value) return; saving.value = true; const key = operationKey('unified-publish'); try { if (!(await saveDraft())) return; validation.value = await adminAPI.unifiedGateway.validateDraft(draftId.value, JSON.parse(JSON.stringify(document)), draftRevision.value); if (!validation.value?.validation_token) throw new Error(t('admin.unifiedGateway.validationRequired')); const result = await gatewayStepUp.run(() => adminAPI.unifiedGateway.publishDraft(draftId.value, configRevision.value, validation.value!.validation_token!, 'admin publish', key)); appStore.showSuccess(t('admin.unifiedGateway.published')); configs.value = [result, ...configs.value.filter(item => item.id !== result.id)]; Object.assign(document, result); configRevision.value = result.revision ?? configRevision.value; validation.value = null; preview.value = null } catch (error: unknown) { reportSensitiveActionError(error) } finally { saving.value = false } }
async function loadRevisionAndSnapshots(item: UnifiedGatewayConfig) { if (!item.id) return; revisions.value = await adminAPI.unifiedGateway.listRevisions(item.id); const page = await adminAPI.unifiedGateway.listSnapshots({ access_group_id: item.access_group_id, public_model: item.public_model, page: 1, page_size: 10 }); snapshots.value = page.items }
async function editPublished(item: UnifiedGatewayConfig) { if (!canWrite.value || !item.id) return; saving.value = true; try { const current = await adminAPI.unifiedGateway.getConfig(item.id); const draft = await adminAPI.unifiedGateway.createDraftFromConfig(item.id); draftId.value = draft.id; draftRevision.value = draft.revision; configRevision.value = current.revision ?? 0; selectedConfigId.value = item.id; await loadRevisionAndSnapshots(item); Object.assign(document, draft.document); syncUnifiedMarkupFromDocument(); validation.value = null; preview.value = null; advancedEditorOpen.value = false; appStore.showSuccess(t('admin.unifiedGateway.draftOpened')) } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.loadFailed')) } finally { saving.value = false } }
async function showRevisions(item: UnifiedGatewayConfig) { if (!item.id) return; selectedConfigId.value = item.id; try { await loadRevisionAndSnapshots(item) } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.loadFailed')) } }
async function disablePublished(item: UnifiedGatewayConfig) { if (!canWrite.value || !item.id || item.revision === undefined) return; saving.value = true; const key = operationKey('unified-disable'); try { const result = await gatewayStepUp.run(() => adminAPI.unifiedGateway.disableConfig(item.id!, item.revision!, 'admin disable', key)); configs.value = configs.value.map(config => config.id === result.id ? result : config); appStore.showSuccess(t('admin.unifiedGateway.disabled')) } catch (error: unknown) { reportSensitiveActionError(error) } finally { saving.value = false } }
async function restorePublished(item: UnifiedGatewayConfig, sourceRevision?: number) { if (!canWrite.value || !item.id || item.revision === undefined) return; const source = sourceRevision ?? revisions.value.find(revision => revision.lifecycle === 'published')?.revision; if (!source) return; saving.value = true; const key = operationKey('unified-restore'); try { const result = await gatewayStepUp.run(() => adminAPI.unifiedGateway.restoreConfig(item.id!, source, item.revision!, 'admin restore', key)); configs.value = configs.value.map(config => config.id === result.id ? result : config); appStore.showSuccess(t('admin.unifiedGateway.restored')) } catch (error: unknown) { reportSensitiveActionError(error) } finally { saving.value = false } }
async function probeBinding(binding: UnifiedGatewayAccountBinding) { if (!canProbe.value || !binding.id || !binding.account_id) return; saving.value = true; const key = operationKey('unified-probe'); try { const result = await gatewayStepUp.run(() => adminAPI.unifiedGateway.probeBinding(binding.id, key)); probeResults.value[binding.id] = String(result.status || result.reason || 'unsupported') } catch (error: unknown) { reportSensitiveActionError(error) } finally { saving.value = false } }
async function previewPricingImport(lane: UnifiedGatewayBillingLane) { if (!draftId.value || !lane.pricing_source_group_id) return; saving.value = true; try { const result = await adminAPI.unifiedGateway.pricingImportPreview(draftId.value, lane.id, lane.pricing_source_group_id, JSON.parse(JSON.stringify(document))); pricingImportPreviews.value[lane.id] = result } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.pricingImportFailed')) } finally { saving.value = false } }
async function applyPricingImport(lane: UnifiedGatewayBillingLane) { const sourceGroupId = lane.pricing_source_group_id; const previewResult = pricingImportPreviews.value[lane.id]; if (!canWrite.value || !draftId.value || !sourceGroupId || !previewResult) return; saving.value = true; const key = operationKey('unified-pricing-import'); try { const result = await adminAPI.unifiedGateway.pricingImportApply(draftId.value, lane.id, sourceGroupId, draftRevision.value, previewResult.import_digest, key); if (result.draft) { draftRevision.value = result.draft.revision; Object.assign(document, result.draft.document) } pricingImports.value[lane.id] = result.source_revision; delete pricingImportPreviews.value[lane.id]; appStore.showSuccess(t('admin.unifiedGateway.pricingImported')) } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.pricingImportFailed')) } finally { saving.value = false } }
onMounted(loadPage)
</script>

<style scoped>
.field-label { @apply text-xs font-medium text-gray-700 dark:text-gray-300; }
</style>

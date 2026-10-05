<template>
  <AppLayout>
    <main class="space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.unifiedGateway.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500">{{ t('admin.unifiedGateway.description') }}</p>
        </div>
        <button class="btn btn-secondary" :disabled="loading" @click="loadPage">{{ t('common.refresh') }}</button>
      </header>

      <div v-if="loading" class="card p-6 text-sm text-gray-500">{{ t('common.loading') }}</div>
      <template v-else>
        <section class="card space-y-3 p-5" data-testid="gateway-status">
          <h2 class="font-semibold">{{ t('admin.unifiedGateway.managementStatus') }}</h2>
          <p data-testid="status-management" :class="managementAvailable ? (canWrite ? 'text-green-600' : 'text-amber-600') : 'text-red-600'">
            {{ !managementAvailable ? t('admin.unifiedGateway.unavailable') : canWrite ? t('admin.unifiedGateway.migrationReady') : t('admin.unifiedGateway.readOnly') }}
          </p>
          <dl class="grid gap-2 text-sm sm:grid-cols-3">
            <div><dt class="text-gray-500">Admin UI</dt><dd data-testid="status-admin-ui">{{ meta?.admin_ui_enabled ? t('admin.unifiedGateway.enabledStatus') : t('admin.unifiedGateway.disabledStatus') }}</dd></div>
            <div><dt class="text-gray-500">{{ t('admin.unifiedGateway.migrationReady') }}</dt><dd data-testid="status-migration">{{ meta?.migration_ready ? t('admin.unifiedGateway.enabledStatus') : t('admin.unifiedGateway.disabledStatus') }}</dd></div>
            <div><dt class="text-gray-500">{{ t('admin.unifiedGateway.runtimeConfig') }}</dt><dd data-testid="status-runtime-enabled">{{ meta?.runtime_enabled ? t('admin.unifiedGateway.enabledStatus') : t('admin.unifiedGateway.disabledStatus') }}</dd></div>
            <div><dt class="text-gray-500">{{ t('admin.unifiedGateway.runtimeEffective') }}</dt><dd data-testid="status-runtime-effective">{{ currentRuntimeEffective === true ? t('admin.unifiedGateway.enabledStatus') : currentRuntimeEffective === false ? t('admin.unifiedGateway.disabledStatus') : t('admin.unifiedGateway.notReported') }}</dd></div>
            <div><dt class="text-gray-500">{{ t('admin.unifiedGateway.endpoint') }}</dt><dd>{{ meta?.supported_endpoints?.join(', ') || t('admin.unifiedGateway.notReported') }}</dd></div>
            <div><dt class="text-gray-500">Contract</dt><dd>{{ meta?.contract_rev || t('admin.unifiedGateway.notReported') }}</dd></div>
          </dl>
          <p class="rounded bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-200" data-testid="runtime-notice">
            {{ meta?.runtime_enabled ? t('admin.unifiedGateway.runtimeEnabled') : t('admin.unifiedGateway.runtimeDisabled') }}
          </p>
          <div v-if="meta?.blockers?.length" class="text-sm text-amber-700"><p v-for="issue in meta.blockers" :key="`${issue.code}-${issue.path}`">{{ issue.message }}</p></div>
        </section>

        <section v-if="!managementAvailable" class="card p-6 text-sm text-amber-700" data-testid="gateway-gate">
          {{ loadFailed ? t('admin.unifiedGateway.unavailable') : meta?.admin_ui_enabled === false ? t('admin.unifiedGateway.unavailable') : meta?.migration_ready === false ? t('admin.unifiedGateway.migrationNotReady') : t('admin.unifiedGateway.writeUnavailable') }}
        </section>

        <template v-if="managementAvailable">
          <section class="card space-y-4 p-5" data-testid="gateway-model-candidates">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div><h2 class="text-lg font-semibold">{{ t('admin.unifiedGateway.modelCandidates') }}</h2><p class="text-sm text-gray-500">{{ modelGroups.length }} · {{ availableModelCount }} {{ t('admin.unifiedGateway.configure') }}</p></div>
              <div class="flex gap-2">
                <button class="btn btn-secondary" :disabled="loading || bulkConfiguring" @click="loadCandidates">{{ t('common.refresh') }}</button>
                <button data-testid="gateway-configure-all-models" class="btn btn-primary" :disabled="!canWrite || bulkConfiguring || !pendingGroups.length" @click="configureAllModels">{{ t('admin.unifiedGateway.configureAll') }}</button>
              </div>
            </div>
            <input v-model.trim="candidateSearch" class="input max-w-md" :placeholder="t('admin.unifiedGateway.model')" data-testid="gateway-model-search" />
            <p v-if="bulkSummary" data-testid="gateway-bulk-summary" class="text-sm text-teal-700">{{ bulkSummary }}</p>
            <p v-if="!filteredGroups.length" class="text-sm text-gray-500">{{ t('admin.unifiedGateway.noCandidates') }}</p>
            <div v-else class="overflow-x-auto">
              <table class="min-w-full text-left text-sm">
                <thead><tr class="text-gray-500"><th class="p-2">{{ t('admin.unifiedGateway.model') }}</th><th class="p-2">{{ t('admin.unifiedGateway.endpoint') }}</th><th class="p-2">{{ t('admin.unifiedGateway.provider') }}</th><th class="p-2">{{ t('admin.unifiedGateway.account') }}</th><th class="p-2">{{ t('admin.unifiedGateway.managementStatus') }}</th><th class="p-2"></th></tr></thead>
                <tbody><tr v-for="group in filteredGroups" :key="group.key" class="border-t border-gray-100 dark:border-gray-700">
                  <td class="p-2 font-medium">{{ group.public_model }}</td><td class="p-2">{{ group.endpoint }}</td>
                  <td class="p-2">{{ [...new Set(group.items.map(item => item.provider_identity))].join(', ') }}</td>
                  <td class="p-2">{{ group.eligible_count }}/{{ group.items.length }}</td>
                  <td class="p-2">{{ publishedKeys.has(group.key) ? t('admin.unifiedGateway.published') : group.eligible_count ? t('admin.unifiedGateway.configure') : t('admin.unifiedGateway.validationFailed') }}</td>
                  <td class="p-2"><button data-testid="gateway-configure-model" class="btn btn-secondary text-xs" :disabled="!canWrite || bulkConfiguring || !group.eligible_count" @click="configureGroup(group)">{{ t('admin.unifiedGateway.configure') }}</button></td>
                </tr></tbody>
              </table>
            </div>
          </section>

          <section class="card space-y-4 p-5" data-testid="gateway-api-usage">
            <h2 class="text-lg font-semibold">Unified API</h2>
            <p class="text-sm text-gray-500">{{ t('admin.unifiedGateway.runtimeDisabled') }}</p>
            <div class="grid gap-3 text-sm sm:grid-cols-2"><div><span class="text-gray-500">Base URL</span><code class="mt-1 block break-all">{{ unifiedBaseUrl }}</code></div><label>{{ t('admin.unifiedGateway.accessGroup') }}<select v-model="document.access_group_id" class="input mt-1" :disabled="!canWrite"><option value="">{{ t('admin.unifiedGateway.selectGroup') }}</option><option v-for="group in options?.items.access_groups ?? []" :key="group.id" :value="group.id">{{ group.name }}</option></select></label></div>
          </section>

          <section class="card space-y-5 p-5" data-testid="gateway-editor">
            <div class="flex flex-wrap items-center justify-between gap-2"><h2 class="text-lg font-semibold">{{ t('admin.unifiedGateway.editor') }}</h2><div class="flex gap-2"><button class="btn btn-secondary" :disabled="saving" @click="resetDocument">{{ t('admin.unifiedGateway.newDraft') }}</button><button data-testid="gateway-save" class="btn btn-secondary" :disabled="saving || !canWrite" @click="saveDraft">{{ t('admin.unifiedGateway.saveDraft') }}</button></div></div>
            <fieldset class="space-y-4 border-0 p-0" :disabled="!canWrite || saving">
              <div class="grid gap-3 md:grid-cols-3">
                <label>{{ t('admin.unifiedGateway.accessGroup') }}<select v-model="document.access_group_id" class="input mt-1"><option value="">{{ t('admin.unifiedGateway.selectGroup') }}</option><option v-for="group in options?.items.access_groups ?? []" :key="group.id" :value="group.id">{{ group.name }}</option></select></label>
                <label>{{ t('admin.unifiedGateway.model') }}<input v-model.trim="document.public_model" class="input mt-1" data-testid="gateway-public-model" /></label>
                <label>{{ t('admin.unifiedGateway.endpoint') }}<select v-model="document.endpoint" class="input mt-1"><option v-for="endpoint in meta?.supported_endpoints ?? []" :key="endpoint" :value="endpoint">{{ endpoint }}</option></select></label>
              </div>
              <div v-for="(lane, laneIndex) in document.lanes" :key="lane.id" class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-gray-700" :data-testid="`gateway-lane-${laneIndex}`">
                <div class="flex items-center justify-between"><h3 class="font-medium">{{ laneIndex ? t('admin.unifiedGateway.backupLane') : t('admin.unifiedGateway.primaryLane') }}</h3><button v-if="document.lanes.length > 1" class="text-sm text-red-600" @click="document.lanes.splice(laneIndex, 1)">{{ t('admin.unifiedGateway.remove') }}</button></div>
                <div class="grid gap-3 md:grid-cols-3"><label>{{ t('admin.unifiedGateway.laneCode') }}<input v-model.trim="lane.code" class="input mt-1" /></label><label>{{ t('admin.unifiedGateway.laneName') }}<input v-model.trim="lane.name" class="input mt-1" /></label><label>{{ t('admin.unifiedGateway.pricingModel') }}<select v-model="lane.profile.pricing_model" class="input mt-1"><option v-for="model in meta?.supported_pricing_models ?? []" :key="model" :value="model">{{ model }}</option></select></label></div>
                <div class="grid gap-3 md:grid-cols-4">
                  <label>{{ t('admin.unifiedGateway.billingMode') }}<select v-model="lane.profile.billing_mode" class="input mt-1" @change="syncPricingMode(lane)"><option v-for="mode in meta?.supported_billing_modes ?? []" :key="mode" :value="mode">{{ mode }}</option></select></label>
                  <label>{{ t('admin.unifiedGateway.rateMode') }}<select v-model="lane.profile.rate_mode" class="input mt-1"><option v-for="mode in meta?.supported_rate_modes ?? []" :key="mode" :value="mode">{{ mode }}</option></select></label>
                  <label>{{ t('admin.unifiedGateway.rateBasis') }}<select v-model="lane.profile.rate_basis" class="input mt-1"><option v-for="basis in meta?.supported_rate_bases ?? []" :key="basis" :value="basis">{{ basis }}</option></select></label>
                  <label>{{ t('admin.unifiedGateway.pricingSchema') }}<select v-model="lane.profile.pricing_schema_id" class="input mt-1" @change="syncPricingMode(lane)"><option v-for="schema in schemasFor(lane.profile.billing_mode)" :key="schema" :value="schema">{{ schema }}</option></select></label>
                </div>
                <div class="grid gap-3 md:grid-cols-4">
                  <label>{{ t('admin.unifiedGateway.providerPrice') }}<input v-model.trim="lane.profile.provider_base_unit_price" class="input mt-1" /></label>
                  <label>{{ t('admin.unifiedGateway.manualBasePrice') }}<input v-model.trim="lane.profile.manual_base_unit_price" class="input mt-1" /></label>
                  <label>{{ t('admin.unifiedGateway.upstreamMultiplier') }}<input v-model.trim="lane.profile.manual_upstream_multiplier" class="input mt-1" /></label>
                  <label>{{ t('admin.unifiedGateway.userMarkup') }}<input v-model.trim="lane.profile.user_markup_multiplier" class="input mt-1" /></label>
                  <label>{{ t('admin.unifiedGateway.baseSemantics') }}<select v-model="lane.profile.base_price_semantics" class="input mt-1" @change="syncPricingMode(lane)"><option value="provider_base">provider_base</option><option value="final_user_price">final_user_price</option></select></label>
                  <label v-if="lane.profile.base_price_semantics === 'final_user_price'">{{ t('admin.unifiedGateway.finalPrice') }}<input v-model.trim="lane.profile.final_user_unit_price" class="input mt-1" /></label>
                  <label>{{ t('admin.unifiedGateway.fixedFee') }}<input v-model.trim="lane.profile.fixed_fee" class="input mt-1" /></label>
                  <label>{{ t('admin.unifiedGateway.minimumCharge') }}<input v-model.trim="lane.profile.minimum_charge" class="input mt-1" /></label>
                  <label>{{ t('admin.unifiedGateway.precision') }}<input v-model.number="lane.profile.precision" type="number" min="0" max="12" class="input mt-1" /></label>
                  <label class="md:col-span-2">{{ t('admin.unifiedGateway.fallbackReason') }}<input v-model.trim="lane.profile.fallback_reason" class="input mt-1" /></label>
                </div>
                <div v-if="lane.profile.manual_pricing_rules" class="grid gap-3 rounded bg-teal-50 p-3 md:grid-cols-3 dark:bg-teal-950/30"><label>{{ t('admin.unifiedGateway.manualRuleUnit') }}<select v-model="lane.profile.manual_pricing_rules.unit" class="input mt-1"><option value="request">request</option><option value="image">image</option><option value="video_task">video_task</option></select></label><label>{{ t('admin.unifiedGateway.manualRulePrice') }}<input v-model.trim="lane.profile.manual_pricing_rules.unit_price" class="input mt-1" /></label><p class="self-end text-xs text-gray-500">{{ t('admin.unifiedGateway.manualRuleHint') }}</p></div>
                <div class="grid gap-3 md:grid-cols-3"><label>{{ t('admin.unifiedGateway.sourceGroup') }}<select v-model="lane.pricing_source_group_id" class="input mt-1" data-testid="gateway-pricing-source"><option value="">—</option><option v-for="group in options?.items.pricing_source_groups ?? []" :key="group.id" :value="group.id">{{ group.name }}</option></select></label><div class="self-end"><button class="btn btn-secondary text-xs" :disabled="!draftId || !lane.pricing_source_group_id || saving" @click="previewPricingImport(lane)">{{ t('admin.unifiedGateway.previewImport') }}</button><button v-if="importPreviews[lane.id]" class="btn btn-primary ml-2 text-xs" :disabled="saving" @click="applyPricingImport(lane)">{{ t('admin.unifiedGateway.applyImport') }}</button><p v-if="importPreviews[lane.id]" class="break-all text-xs">{{ t('admin.unifiedGateway.importDigest') }}: {{ importPreviews[lane.id].import_digest }}</p></div></div>
                <div v-if="importPreviews[lane.id]" class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-950 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-100" data-testid="gateway-pricing-import-preview">
                  <div class="font-medium">{{ t('admin.unifiedGateway.importPreviewDetails') }}</div>
                  <div class="mt-2 grid gap-1 md:grid-cols-3">
                    <span>{{ t('admin.unifiedGateway.pricingModel') }}: {{ importPreviews[lane.id].profile.pricing_model }}</span>
                    <span>{{ t('admin.unifiedGateway.billingMode') }}: {{ importPreviews[lane.id].profile.billing_mode }}</span>
                    <span>{{ t('admin.unifiedGateway.rateMode') }}: {{ importPreviews[lane.id].profile.rate_mode }}</span>
                    <span>{{ t('admin.unifiedGateway.rateBasis') }}: {{ importPreviews[lane.id].profile.rate_basis }}</span>
                    <span>{{ t('admin.unifiedGateway.pricingSchema') }}: {{ importPreviews[lane.id].profile.pricing_schema_id }}</span>
                    <span>{{ t('admin.unifiedGateway.baseSemantics') }}: {{ importPreviews[lane.id].profile.base_price_semantics }}</span>
                    <span>{{ t('admin.unifiedGateway.providerPrice') }}: {{ importPreviews[lane.id].profile.provider_base_unit_price ?? '—' }}</span>
                    <span>{{ t('admin.unifiedGateway.upstreamMultiplier') }}: {{ importPreviews[lane.id].profile.manual_upstream_multiplier ?? '—' }}</span>
                    <span>{{ t('admin.unifiedGateway.userMarkup') }}: {{ importPreviews[lane.id].profile.user_markup_multiplier ?? '—' }}</span>
                    <span>{{ t('admin.unifiedGateway.finalPrice') }}: {{ importPreviews[lane.id].profile.final_user_unit_price ?? '—' }}</span>
                    <span>{{ t('admin.unifiedGateway.pricingSourceRevision') }}: {{ importPreviews[lane.id].source_revision }}</span>
                    <span>{{ t('admin.unifiedGateway.importDigest') }}: {{ importPreviews[lane.id].import_digest }}</span>
                  </div>
                </div>
                <div v-for="(target, targetIndex) in lane.targets" :key="target.id" class="space-y-3 rounded bg-gray-50 p-3 dark:bg-gray-800/50">
                  <div class="flex justify-between"><strong>{{ t('admin.unifiedGateway.target') }} {{ targetIndex + 1 }}</strong><button v-if="lane.targets.length > 1" class="text-sm text-red-600" @click="lane.targets.splice(targetIndex, 1)">{{ t('admin.unifiedGateway.remove') }}</button></div>
                  <div class="grid gap-3 md:grid-cols-4"><label>{{ t('admin.unifiedGateway.provider') }}<input v-model.trim="target.provider_identity" class="input mt-1" /></label><label>{{ t('admin.unifiedGateway.upstreamModel') }}<input v-model.trim="target.upstream_model" class="input mt-1" /></label><label>{{ t('admin.unifiedGateway.endpoint') }}<select v-model="target.endpoint" class="input mt-1"><option v-for="endpoint in meta?.supported_endpoints ?? []" :key="endpoint" :value="endpoint">{{ endpoint }}</option></select></label><label>{{ t('admin.unifiedGateway.priority') }}<input v-model.number="target.priority" type="number" class="input mt-1" /></label></div>
                  <div v-for="(binding, bindingIndex) in target.bindings" :key="binding.id" class="grid items-end gap-3 md:grid-cols-4">
                    <label>{{ t('admin.unifiedGateway.account') }}<select v-model="binding.account_id" class="input mt-1" @change="syncBindingFromAccount(binding)"><option value="">—</option><option v-for="account in options?.items.accounts ?? []" :key="account.id" :value="account.id">{{ account.name }} · {{ account.platform }} · {{ account.id }}</option></select><small v-if="binding.probe" class="text-gray-500">{{ binding.probe.status }} · {{ binding.probe.basis }}</small></label>
                    <label>{{ t('admin.unifiedGateway.endpoint') }}<select v-model="binding.endpoint" class="input mt-1"><option value="">Inherit</option><option v-for="endpoint in meta?.supported_endpoints ?? []" :key="endpoint" :value="endpoint">{{ endpoint }}</option></select></label>
                    <label>{{ t('admin.unifiedGateway.priority') }}<input v-model.number="binding.priority" type="number" class="input mt-1" /></label>
                    <div class="flex items-center gap-3"><label class="flex items-center gap-2"><input v-model="binding.enabled" type="checkbox" />{{ t('admin.unifiedGateway.enabled') }}</label><button v-if="target.bindings.length > 1" class="text-sm text-red-600" @click="target.bindings.splice(bindingIndex, 1)">{{ t('admin.unifiedGateway.remove') }}</button></div>
                  </div>
                  <button class="text-sm text-teal-700" @click="addBinding(target)">{{ t('admin.unifiedGateway.addBinding') }}</button>
                </div>
                <button class="btn btn-secondary text-sm" @click="addTarget(lane)">{{ t('admin.unifiedGateway.addTarget') }}</button>
              </div>
              <button class="btn btn-secondary" @click="addLane">{{ t('admin.unifiedGateway.addLane') }}</button>
            </fieldset>

            <section class="grid gap-4 border-t border-gray-200 pt-4 dark:border-gray-700 lg:grid-cols-2">
              <div class="space-y-3"><h3 class="font-medium">{{ t('admin.unifiedGateway.validate') }}</h3><div class="flex gap-2"><button class="btn btn-secondary" :disabled="saving || !canWrite" @click="validateCurrent">{{ t('admin.unifiedGateway.validate') }}</button><button class="btn btn-secondary" data-testid="gateway-preview" :disabled="saving || !canWrite" @click="previewCurrent">{{ t('admin.unifiedGateway.calculate') }}</button></div>
                <div v-if="validation" data-testid="gateway-validation"><p :class="validation.valid ? 'text-green-600' : 'text-red-600'">{{ validation.valid ? t('admin.unifiedGateway.validationPassed') : t('admin.unifiedGateway.validationFailed') }}</p><ul class="list-disc pl-5 text-sm text-red-600"><li v-for="item in validation.blockers" :key="`${item.code}-${item.path}`">{{ item.path }}: {{ item.message }}</li></ul><ul class="list-disc pl-5 text-sm text-amber-600"><li v-for="item in validation.warnings" :key="`${item.code}-${item.path}`">{{ item.path }}: {{ item.message }}</li></ul></div>
                <div class="grid gap-2 sm:grid-cols-3"><label>{{ t('admin.unifiedGateway.previewLane') }}<select v-model="previewLaneId" class="input mt-1" @change="syncPreviewSelection"><option v-for="lane in document.lanes" :key="lane.id" :value="lane.id">{{ lane.name || lane.code || lane.id }}</option></select></label><label>{{ t('admin.unifiedGateway.previewTarget') }}<select v-model="previewTargetId" class="input mt-1" @change="syncPreviewSelection"><option v-for="target in previewLane?.targets ?? []" :key="target.id" :value="target.id">{{ target.provider_identity }} · {{ target.upstream_model }}</option></select></label><label>{{ t('admin.unifiedGateway.previewBinding') }}<select v-model="previewBindingId" class="input mt-1"><option v-for="binding in previewTarget?.bindings ?? []" :key="binding.id" :value="binding.id">{{ binding.account_id }}</option></select></label></div>
                <div class="grid gap-2 sm:grid-cols-3"><label>{{ t('admin.unifiedGateway.inputTokens') }}<input v-model="previewInput" class="input mt-1" /></label><label>{{ t('admin.unifiedGateway.outputTokens') }}<input v-model="previewOutput" class="input mt-1" /></label><label>{{ t('admin.unifiedGateway.deliveryState') }}<select v-model="previewDeliveryState" class="input mt-1"><option value="success">success</option><option value="failed">failed</option></select></label></div>
                <p v-if="preview" data-testid="gateway-preview-result" class="rounded bg-gray-50 p-3 text-sm dark:bg-gray-800">{{ t('admin.unifiedGateway.estimatedCharge') }}: {{ preview.estimated_charge }} {{ preview.currency }} · {{ t('admin.unifiedGateway.previewOnly') }}</p>
              </div>
              <div class="space-y-3"><h3 class="font-medium">{{ t('admin.unifiedGateway.publish') }}</h3><label>{{ t('admin.unifiedGateway.publishReason') }}<input v-model="publishReason" class="input mt-1" /></label><button data-testid="gateway-publish" class="btn btn-primary w-full" :disabled="saving || !canWrite || !document.public_model" @click="publishCurrent">{{ t('admin.unifiedGateway.publish') }}</button><p class="text-sm text-gray-500">{{ t('admin.unifiedGateway.runtimeDisabled') }}</p></div>
            </section>
          </section>

          <section class="card space-y-3 p-5" data-testid="gateway-configs"><h2 class="text-lg font-semibold">{{ t('admin.unifiedGateway.published') }}</h2><p v-if="!configs.length" class="text-sm text-gray-500">{{ t('admin.unifiedGateway.noConfigs') }}</p><article v-for="item in configs" :key="item.id" class="space-y-2 rounded border border-gray-200 p-3 dark:border-gray-700"><div class="flex flex-wrap items-center justify-between gap-2"><strong>{{ item.public_model }}</strong><span>{{ item.lifecycle }} · {{ t('admin.unifiedGateway.runtimeEffective') }}: {{ item.runtime_effective === undefined ? t('admin.unifiedGateway.notReported') : item.runtime_effective ? t('admin.unifiedGateway.enabledStatus') : t('admin.unifiedGateway.disabledStatus') }}</span></div><p class="text-sm text-gray-500">{{ item.endpoint }} · revision {{ item.revision ?? 0 }}</p><div class="flex flex-wrap gap-2"><button class="btn btn-secondary text-xs" :disabled="saving || !canWrite" @click="editConfig(item)">{{ t('admin.unifiedGateway.editDraft') }}</button><button v-if="item.lifecycle === 'published'" class="btn btn-secondary text-xs" :disabled="saving || !canWrite" @click="disableConfig(item)">{{ t('admin.unifiedGateway.disable') }}</button><button v-if="item.lifecycle === 'disabled'" class="btn btn-secondary text-xs" :disabled="saving || !canWrite" @click="restoreConfig(item)">{{ t('admin.unifiedGateway.restore') }}</button><button class="btn btn-secondary text-xs" @click="loadRevisions(item)">{{ t('admin.unifiedGateway.revisions') }}</button></div><ul v-if="selectedConfigId === item.id && revisions.length" class="border-t pt-2 text-sm"><li v-for="revision in revisions" :key="revision.revision" class="flex justify-between py-1"><span>r{{ revision.revision }} · {{ revision.lifecycle }} · {{ revision.reason || '—' }}</span><button v-if="item.lifecycle === 'disabled' && revision.lifecycle === 'published'" class="text-teal-700" :disabled="saving || !canWrite" @click="restoreConfig(item, revision.revision)">{{ t('admin.unifiedGateway.restore') }}</button></li></ul></article></section>
        </template>
      </template>
      <TotpStepUpDialog :controller="stepUp" />
    </main>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { getMeta, getOptions, listModelCandidates, listConfigs, getConfig, createDraft, createDraftFromConfig, getDraft, updateDraft, validateDraft, listRevisions, previewDraft, pricingImportPreview, pricingImportApply, publishDraft, disableConfig as disableConfigRequest, restoreConfig as restoreConfigRequest } from '@/api/admin/unifiedGateway'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpBlocked, isStepUpCancelled, stepUpBlockReason, useStepUp } from '@/composables/useStepUp'
import type { UnifiedGatewayAccountBinding, UnifiedGatewayBillingLane, UnifiedGatewayBillingMode, UnifiedGatewayConfig, UnifiedGatewayEndpoint, UnifiedGatewayMeta, UnifiedGatewayModelCandidate, UnifiedGatewayOptionsPage, UnifiedGatewayPricingImportResult, UnifiedGatewayPreviewRequest, UnifiedGatewayPreviewResult, UnifiedGatewayRevision, UnifiedGatewayRouteTarget, UnifiedGatewayValidationResult } from '@/types/unifiedGateway'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const bulkConfiguring = ref(false)
const loadFailed = ref(false)
const meta = ref<UnifiedGatewayMeta | null>(null)
const options = ref<UnifiedGatewayOptionsPage | null>(null)
const candidates = ref<UnifiedGatewayModelCandidate[]>([])
const configs = ref<UnifiedGatewayConfig[]>([])
const candidateSearch = ref('')
const bulkSummary = ref('')
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
const publishReason = ref('')
const selectedConfigId = ref('')
const revisions = ref<UnifiedGatewayRevision[]>([])
const importPreviews = ref<Record<string, UnifiedGatewayPricingImportResult>>({})
const stepUp = useStepUp()

function uid(prefix: string) { return `${prefix}_${globalThis.crypto?.randomUUID?.() ?? `${Date.now()}_${Math.random().toString(36).slice(2)}`}` }
const managementAvailable = computed(() => meta.value?.admin_ui_enabled === true && meta.value?.migration_ready === true && meta.value?.capabilities?.read === true)
const canWrite = computed(() => managementAvailable.value && (meta.value?.capabilities?.write === true || (meta.value?.capabilities?.draft === true && meta.value?.capabilities?.publish === true)))
const currentRuntimeEffective = computed(() => configs.value.find(item => item.id === selectedConfigId.value)?.runtime_effective ?? meta.value?.runtime_effective ?? null)
const unifiedBaseUrl = computed(() => typeof window === 'undefined' ? '/unified/v1' : `${window.location.origin}/unified/v1`)
const previewLane = computed(() => document.lanes.find(lane => lane.id === previewLaneId.value) ?? document.lanes[0])
const previewTarget = computed(() => previewLane.value?.targets.find(target => target.id === previewTargetId.value) ?? previewLane.value?.targets[0])

interface CandidateGroup { key: string; public_model: string; endpoint: UnifiedGatewayEndpoint; items: UnifiedGatewayModelCandidate[]; eligible_count: number }
const modelGroups = computed<CandidateGroup[]>(() => {
  const map = new Map<string, CandidateGroup>()
  for (const item of candidates.value) {
    const key = `${item.public_model}::${item.endpoint}`
    const group = map.get(key) ?? { key, public_model: item.public_model, endpoint: item.endpoint, items: [], eligible_count: 0 }
    group.items.push(item)
    if (item.runtime_eligible) group.eligible_count += 1
    map.set(key, group)
  }
  return [...map.values()].sort((a, b) => a.public_model.localeCompare(b.public_model))
})
const publishedKeys = computed(() => new Set(configs.value.filter(item => item.lifecycle === 'published').map(item => `${item.public_model}::${item.endpoint}`)))
const pendingGroups = computed(() => modelGroups.value.filter(group => group.eligible_count > 0 && !publishedKeys.value.has(group.key)))
const availableModelCount = computed(() => modelGroups.value.filter(group => group.eligible_count > 0).length)
const filteredGroups = computed(() => {
  const search = candidateSearch.value.toLowerCase()
  return modelGroups.value.filter(group => !search || `${group.public_model} ${group.endpoint} ${group.items.map(item => item.provider_identity).join(' ')}`.toLowerCase().includes(search))
})

function defaultReason() { return t('admin.unifiedGateway.defaultFallbackReason') }
function billingModeForEndpoint(endpoint: UnifiedGatewayEndpoint): UnifiedGatewayBillingMode { return endpoint === 'videos' ? 'video' : endpoint === 'images_generations' || endpoint === 'images_edits' ? 'image' : 'token' }
function pricingModelFor(mode: UnifiedGatewayBillingMode) { return mode === 'image' ? 'image' : mode === 'video' ? 'video' : mode === 'per_request' ? 'fixed_request' : 'provider_metered' }
function schemasFor(mode: string) { return mode === 'image' ? ['image_delivery_v1', 'flat_unit_price_v1'] : mode === 'video' ? ['video_delivery_v1', 'flat_unit_price_v1'] : mode === 'per_request' ? ['request_v1', 'flat_unit_price_v1'] : ['token_v1'] }
function ruleUnit(mode: string): 'request' | 'image' | 'video_task' { return mode === 'image' ? 'image' : mode === 'video' ? 'video_task' : 'request' }
function newBinding(): UnifiedGatewayAccountBinding { return { id: uid('binding'), account_id: '', schedulable: true, eligibility: 'unknown', priority: 10, enabled: true, revision: 1 } }
function newTarget(endpoint: UnifiedGatewayEndpoint): UnifiedGatewayRouteTarget { return { id: uid('target'), provider_identity: '', upstream_model: '', endpoint, priority: 10, bindings: [newBinding()] } }
function newLane(endpoint: UnifiedGatewayEndpoint): UnifiedGatewayBillingLane {
  const mode = billingModeForEndpoint(endpoint)
  return { id: uid('lane'), code: '', name: '', selection_strategy: 'fixed_priority', profile: { id: uid('profile'), version: '1', pricing_model: pricingModelFor(mode), billing_mode: mode, rate_mode: 'manual_only', rate_basis: mode, pricing_schema_id: schemasFor(mode)[0], currency: 'USD', base_price_semantics: 'provider_base', provider_base_unit_price: null, manual_base_unit_price: null, manual_upstream_multiplier: null, user_markup_multiplier: '1.000000', final_user_unit_price: null, fixed_fee: '0', minimum_charge: '0', rounding_mode: 'half_up', precision: 8, fallback_reason: defaultReason(), manual_pricing_rules: null, charge_trigger: 'success_delivery', failure_charge: 'zero' }, targets: [newTarget(endpoint)] }
}
function emptyDocument(): UnifiedGatewayConfig { return { access_group_id: '', public_model: '', endpoint: 'chat_completions', lanes: [newLane('chat_completions')] } }
const document = reactive<UnifiedGatewayConfig>(emptyDocument())

function syncPricingMode(lane: UnifiedGatewayBillingLane) {
  const profile = lane.profile
  const validSchemas = schemasFor(profile.billing_mode)
  if (!validSchemas.includes(profile.pricing_schema_id)) profile.pricing_schema_id = validSchemas[0]
  if (profile.pricing_schema_id === 'flat_unit_price_v1') {
    profile.base_price_semantics = 'final_user_price'
    profile.provider_base_unit_price = null
    profile.manual_base_unit_price = null
    profile.manual_upstream_multiplier = null
    profile.user_markup_multiplier = null
    profile.manual_pricing_rules ??= { formula_id: 'flat_unit_price', unit: ruleUnit(profile.billing_mode), unit_price: '' }
    profile.manual_pricing_rules.unit = ruleUnit(profile.billing_mode)
  } else {
    profile.manual_pricing_rules = null
    if (profile.base_price_semantics === 'final_user_price') {
      profile.provider_base_unit_price = null
      profile.manual_base_unit_price = null
      profile.manual_upstream_multiplier = null
      profile.user_markup_multiplier = null
    } else {
      profile.final_user_unit_price = null
    }
  }
}
function resetDocument() {
  const next = emptyDocument()
  if (options.value?.items.access_groups.length === 1) next.access_group_id = options.value.items.access_groups[0].id
  Object.assign(document, next)
  draftId.value = ''; draftRevision.value = 0; configRevision.value = 0
  validation.value = null; preview.value = null; selectedConfigId.value = ''; revisions.value = []; publishReason.value = ''
}
function addLane() { document.lanes.push(newLane(document.endpoint)) }
function addTarget(lane: UnifiedGatewayBillingLane) { lane.targets.push(newTarget(document.endpoint)) }
function addBinding(target: UnifiedGatewayRouteTarget) { target.bindings.push(newBinding()) }
function syncBindingFromAccount(binding: UnifiedGatewayAccountBinding) {
  const account = options.value?.items.accounts.find(item => item.id === binding.account_id)
  if (!account) return
  binding.schedulable = account.schedulable === true
  binding.eligibility = account.status === 'active' && account.schedulable ? 'eligible' : 'ineligible'
}
watch(() => document.endpoint, endpoint => { for (const lane of document.lanes) for (const target of lane.targets) if (!target.endpoint) target.endpoint = endpoint })
function syncPreviewSelection() {
  const lane = document.lanes.find(item => item.id === previewLaneId.value) ?? document.lanes[0]
  previewLaneId.value = lane?.id ?? ''
  const target = lane?.targets.find(item => item.id === previewTargetId.value) ?? lane?.targets[0]
  previewTargetId.value = target?.id ?? ''
  const binding = target?.bindings.find(item => item.id === previewBindingId.value) ?? target?.bindings[0]
  previewBindingId.value = binding?.id ?? ''
}
watch(() => document.lanes, syncPreviewSelection, { deep: true, immediate: true })

function buildDocumentFromCandidates(items: UnifiedGatewayModelCandidate[]): UnifiedGatewayConfig | null {
  const eligible = items.filter(item => item.runtime_eligible)
  if (!eligible.length) return null
  const next = emptyDocument()
  if (options.value?.items.access_groups.length === 1) next.access_group_id = options.value.items.access_groups[0].id
  next.public_model = eligible[0].public_model
  next.endpoint = eligible[0].endpoint
  next.lanes = eligible.map((item, index) => {
    const lane = newLane(item.endpoint)
    lane.code = `${item.provider_identity}-${item.account_id}-${index + 1}`.slice(0, 128)
    lane.name = `${index ? t('admin.unifiedGateway.backupLane') : t('admin.unifiedGateway.primaryLane')} · ${item.account_name} · ${item.provider_identity}`.slice(0, 255)
    lane.targets[0].priority = (index + 1) * 10
    lane.targets[0].provider_identity = item.provider_identity
    lane.targets[0].upstream_model = item.upstream_model
    lane.targets[0].endpoint = item.endpoint
    Object.assign(lane.targets[0].bindings[0], { account_id: item.account_id, priority: (index + 1) * 10, schedulable: item.schedulable, eligibility: 'eligible' })
    return lane
  })
  return next
}
function configureGroup(group: CandidateGroup) {
  if (!canWrite.value) return
  const next = buildDocumentFromCandidates(group.items)
  if (!next) return
  resetDocument(); Object.assign(document, next)
}

async function loadCandidates() {
  if (!managementAvailable.value) return
  try { candidates.value = (await listModelCandidates()).items }
  catch (error) { appStore.showError(errorMessage(error)) }
}
function errorMessage(error: unknown) { return (error as { response?: { data?: { message?: string } }; message?: string })?.response?.data?.message || (error as { message?: string })?.message || t('admin.unifiedGateway.requestFailed') }
async function loadPage() {
  loading.value = true; loadFailed.value = false
  try {
    meta.value = await getMeta()
    if (!managementAvailable.value) { options.value = null; candidates.value = []; configs.value = []; return }
    const [nextOptions, nextConfigs, nextCandidates] = await Promise.all([getOptions(), listConfigs(''), listModelCandidates()])
    options.value = nextOptions; configs.value = nextConfigs.items; candidates.value = nextCandidates.items
    if (!document.access_group_id && nextOptions.items.access_groups.length === 1) document.access_group_id = nextOptions.items.access_groups[0].id
  } catch (error) { meta.value = null; options.value = null; candidates.value = []; configs.value = []; loadFailed.value = true; appStore.showError(errorMessage(error)) }
  finally { loading.value = false }
}
function cloneDocument() { return JSON.parse(JSON.stringify(document)) as UnifiedGatewayConfig }
async function saveDraft(): Promise<boolean> {
  if (!canWrite.value) return false
  saving.value = true
  try {
    const draft = draftId.value ? await updateDraft(draftId.value, cloneDocument(), draftRevision.value) : await createDraft(cloneDocument())
    draftId.value = draft.id; draftRevision.value = draft.revision; configRevision.value = draft.document.revision ?? configRevision.value
    Object.assign(document, draft.document); validation.value = null; preview.value = null
    appStore.showSuccess(t('admin.unifiedGateway.saved')); return true
  } catch (error) { appStore.showError(errorMessage(error)); return false }
  finally { saving.value = false }
}
async function ensureDraft() { return await saveDraft() }
async function validateCurrent() {
  if (!canWrite.value) return
  saving.value = true
  try { if (await ensureDraft()) validation.value = await validateDraft(draftId.value, cloneDocument(), draftRevision.value) }
  catch (error) { appStore.showError(errorMessage(error)) }
  finally { saving.value = false }
}
function previewRequest(lane: UnifiedGatewayBillingLane, target: UnifiedGatewayRouteTarget, binding: UnifiedGatewayAccountBinding): UnifiedGatewayPreviewRequest {
  return lane.profile.billing_mode === 'token'
    ? { lane_id: lane.id, target_id: target.id, binding_id: binding.id, input_tokens: previewInput.value, output_tokens: previewOutput.value, delivery_state: previewDeliveryState.value }
    : { lane_id: lane.id, target_id: target.id, binding_id: binding.id, units: '1', delivery_state: previewDeliveryState.value }
}
async function previewCurrent() {
  if (!canWrite.value) return
  saving.value = true
  try {
    if (!await ensureDraft()) return
    syncPreviewSelection()
    const lane = previewLane.value; const target = previewTarget.value; const binding = target?.bindings.find(item => item.id === previewBindingId.value)
    if (!lane || !target || !binding?.account_id) throw new Error(t('admin.unifiedGateway.noBindings'))
    preview.value = await previewDraft(draftId.value, previewRequest(lane, target, binding), cloneDocument())
  } catch (error) { appStore.showError(errorMessage(error)) }
  finally { saving.value = false }
}
async function previewPricingImport(lane: UnifiedGatewayBillingLane) {
  if (!draftId.value || !lane.pricing_source_group_id) return
  saving.value = true
  try { importPreviews.value[lane.id] = await pricingImportPreview(draftId.value, lane.id, lane.pricing_source_group_id, cloneDocument()) }
  catch (error) { appStore.showError(errorMessage(error)) }
  finally { saving.value = false }
}
async function applyPricingImport(lane: UnifiedGatewayBillingLane) {
  const current = importPreviews.value[lane.id]
  if (!canWrite.value || !draftId.value || !lane.pricing_source_group_id || !current) return
  saving.value = true
  try {
    const result = await pricingImportApply(draftId.value, lane.id, lane.pricing_source_group_id, draftRevision.value, current.import_digest)
    if (result.draft) { draftRevision.value = result.draft.revision; Object.assign(document, result.draft.document) }
    const updatedLane = document.lanes.find(item => item.id === lane.id)
    if (updatedLane) updatedLane.pricing_source_revision = result.source_revision
    delete importPreviews.value[lane.id]
    appStore.showSuccess(t('admin.unifiedGateway.pricingImported'))
  } catch (error) { appStore.showError(errorMessage(error)) }
  finally { saving.value = false }
}
function sensitiveActionError(error: unknown) {
  if (isStepUpCancelled(error)) return
  if (isStepUpBlocked(error)) { appStore.showError(stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? t('admin.unifiedGateway.stepUpApiKeyForbidden') : t('admin.unifiedGateway.stepUpUnavailable')); return }
  appStore.showError(errorMessage(error))
}
async function publishCurrent() {
  if (!canWrite.value) return
  saving.value = true
  try {
    if (!await ensureDraft()) return
    validation.value = await validateDraft(draftId.value, cloneDocument(), draftRevision.value)
    if (!validation.value.valid || !validation.value.validation_token) throw new Error(t('admin.unifiedGateway.validationRequired'))
    const result = await stepUp.run(() => publishDraft(draftId.value, configRevision.value, validation.value!.validation_token!, publishReason.value))
    configs.value = [result, ...configs.value.filter(item => item.id !== result.id)]
    Object.assign(document, result); configRevision.value = result.revision ?? configRevision.value
    validation.value = null; preview.value = null; appStore.showSuccess(t('admin.unifiedGateway.published'))
  } catch (error) { sensitiveActionError(error) }
  finally { saving.value = false }
}
async function configureAllModels() {
  if (!canWrite.value || bulkConfiguring.value || !pendingGroups.value.length) return
  if (!window.confirm(t('admin.unifiedGateway.confirmBulk', { count: pendingGroups.value.length }))) return
  bulkConfiguring.value = true
  let published = 0; let blocked = 0
  try {
    for (const group of pendingGroups.value) {
      const next = buildDocumentFromCandidates(group.items)
      if (!next) { blocked++; continue }
      try {
        const draft = await createDraft(next)
        const checked = await validateDraft(draft.id, undefined, draft.revision)
        if (!checked.valid || !checked.validation_token) { blocked++; continue }
        const result = await stepUp.run(() => publishDraft(draft.id, draft.document.revision ?? 0, checked.validation_token!, 'configure all eligible models'))
        configs.value = [result, ...configs.value.filter(item => item.id !== result.id)]; published++
      } catch (error) { if (isStepUpCancelled(error)) break; blocked++ }
    }
    bulkSummary.value = t('admin.unifiedGateway.bulkResult', { published, blocked })
    const refreshed = await listConfigs(''); configs.value = refreshed.items
    await loadCandidates()
  } catch (error) { appStore.showError(errorMessage(error)) }
  finally { bulkConfiguring.value = false }
}
async function editConfig(item: UnifiedGatewayConfig) {
  if (!canWrite.value || !item.id) return
  saving.value = true
  try {
    const [current, draft] = await Promise.all([getConfig(item.id), createDraftFromConfig(item.id)])
    const loadedDraft = draft.document ? draft : await getDraft(draft.id)
    draftId.value = loadedDraft.id; draftRevision.value = loadedDraft.revision; configRevision.value = current.revision ?? 0; selectedConfigId.value = item.id
    Object.assign(document, loadedDraft.document); validation.value = null; preview.value = null
  } catch (error) { appStore.showError(errorMessage(error)) }
  finally { saving.value = false }
}
async function loadRevisions(item: UnifiedGatewayConfig) {
  if (!item.id) return
  try { selectedConfigId.value = item.id; revisions.value = await listRevisions(item.id) }
  catch (error) { appStore.showError(errorMessage(error)) }
}
async function disableConfig(item: UnifiedGatewayConfig) {
  if (!canWrite.value || !item.id || item.revision === undefined) return
  saving.value = true
  try { const result = await stepUp.run(() => disableConfigRequest(item.id!, item.revision!, 'admin disable')); configs.value = configs.value.map(config => config.id === result.id ? result : config) }
  catch (error) { sensitiveActionError(error) }
  finally { saving.value = false }
}
async function restoreConfig(item: UnifiedGatewayConfig, sourceRevision?: number) {
  if (!canWrite.value || !item.id || item.revision === undefined) return
  const source = sourceRevision ?? revisions.value.find(revision => revision.lifecycle === 'published')?.revision
  if (!source) return
  saving.value = true
  try { const result = await stepUp.run(() => restoreConfigRequest(item.id!, source, item.revision!, 'admin restore')); configs.value = configs.value.map(config => config.id === result.id ? result : config) }
  catch (error) { sensitiveActionError(error) }
  finally { saving.value = false }
}

onMounted(loadPage)
</script>

<style scoped>
label { @apply block text-xs font-medium text-gray-700 dark:text-gray-300; }
</style>

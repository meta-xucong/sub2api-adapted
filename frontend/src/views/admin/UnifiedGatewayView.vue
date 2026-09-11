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

      <div v-else-if="!meta || !meta.admin_ui_enabled || !meta.migration_ready || meta.capabilities?.read !== true" class="card border-amber-200 p-6 dark:border-amber-800">
        <h2 class="font-medium text-amber-800 dark:text-amber-200">{{ t('admin.unifiedGateway.gatedTitle') }}</h2>
        <p class="mt-2 text-sm text-amber-700 dark:text-amber-300">{{ gateMessage }}</p>
        <p class="mt-3 text-xs text-gray-500">{{ t('admin.unifiedGateway.runtimeOff') }}</p>
      </div>

      <template v-else>
        <div class="grid gap-6 xl:grid-cols-[minmax(0,1.5fr)_minmax(360px,1fr)]">
          <section class="card space-y-5 p-5">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div>
                <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.unifiedGateway.editor') }}</h2>
                <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.editorHint') }}</p>
              </div>
              <div class="flex gap-2">
                <button class="btn btn-secondary" :disabled="saving" @click="resetDocument">{{ t('admin.unifiedGateway.newDraft') }}</button>
                <button class="btn btn-primary" :disabled="saving" @click="saveDraft">{{ saving ? t('common.saving') : t('common.save') }}</button>
              </div>
            </div>

            <div class="grid gap-4 md:grid-cols-3">
              <label class="field-label">{{ t('admin.unifiedGateway.accessGroup') }}
                <select v-model="document.access_group_id" class="input mt-1">
                  <option value="">{{ t('admin.unifiedGateway.selectGroup') }}</option>
                  <option v-for="group in options?.items.access_groups ?? []" :key="group.id" :value="group.id">{{ group.name }} ({{ group.id }})</option>
                </select>
              </label>
              <label class="field-label">{{ t('admin.unifiedGateway.publicModel') }}
                <input v-model.trim="document.public_model" class="input mt-1" placeholder="gpt-5.5 / deepseek-chat" />
              </label>
              <label class="field-label">{{ t('admin.unifiedGateway.endpoint') }}
                <select v-model="document.endpoint" class="input mt-1">
                  <option v-for="endpoint in (meta?.supported_endpoints ?? [])" :key="endpoint" :value="endpoint">{{ endpoint }}</option>
                </select>
              </label>
            </div>

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
                  <label class="field-label">{{ t('admin.unifiedGateway.billingMode') }}<select v-model="lane.profile.billing_mode" class="input mt-1" @change="syncPricingMode(lane)"><option v-for="mode in (meta?.supported_billing_modes ?? [])" :key="mode" :value="mode">{{ mode }}</option></select></label>
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
                    <div v-for="(binding, bindingIndex) in target.bindings" :key="binding.id" class="mt-3 grid gap-3 md:grid-cols-[minmax(0,1fr)_120px_100px_60px]">
                      <label class="field-label">{{ t('admin.unifiedGateway.account') }}<select v-model="binding.account_id" class="input mt-1" @change="syncBindingFromAccount(binding)"><option value="">{{ t('admin.unifiedGateway.selectAccount') }}</option><option v-for="account in options?.items.accounts ?? []" :key="account.id" :value="account.id">{{ account.name }} · {{ account.platform }} · {{ account.id }}</option></select><span v-if="accountOption(binding.account_id)" class="mt-1 block text-[11px] text-gray-500">{{ accountOption(binding.account_id)?.status }} · {{ accountOption(binding.account_id)?.schedulable ? t('admin.unifiedGateway.schedulable') : t('admin.unifiedGateway.unschedulable') }} · {{ t('admin.unifiedGateway.eligibility') }}: {{ binding.eligibility }}<span v-if="accountOption(binding.account_id)?.capabilities?.length"> · {{ accountOption(binding.account_id)?.capabilities?.join(', ') }}</span></span><button v-if="binding.id" type="button" class="mt-1 text-[11px] text-teal-700" @click="probeBinding(binding)">{{ t('admin.unifiedGateway.probeAction') }}</button><span v-if="probeResults[binding.id]" class="ml-2 text-[11px] text-gray-500">{{ probeResults[binding.id] }}</span></label>
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
          </section>

          <aside class="space-y-6">
            <section class="card space-y-3 p-5">
              <div class="flex items-center justify-between"><h2 class="text-lg font-semibold">{{ t('admin.unifiedGateway.validation') }}</h2><button class="btn btn-secondary text-xs" :disabled="saving || !draftId" @click="validateCurrent">{{ t('admin.unifiedGateway.validate') }}</button></div>
              <p v-if="!draftId" class="text-sm text-gray-500">{{ t('admin.unifiedGateway.saveFirst') }}</p>
              <div v-if="validation" class="space-y-2 text-sm"><p :class="validation.valid ? 'text-green-600' : 'text-red-600'">{{ validation.valid ? t('admin.unifiedGateway.ready') : t('admin.unifiedGateway.blocked') }}</p><ul v-if="validation.blockers.length" class="list-disc space-y-1 pl-5 text-red-600"><li v-for="issue in validation.blockers" :key="`${issue.code}-${issue.path}`">{{ issue.path }}: {{ issue.message }}</li></ul><ul v-if="validation.warnings.length" class="list-disc space-y-1 pl-5 text-amber-600"><li v-for="issue in validation.warnings" :key="`${issue.code}-${issue.path}`">{{ issue.path }}: {{ issue.message }}</li></ul></div>
            </section>

            <section class="card space-y-3 p-5">
              <div class="flex items-center justify-between"><h2 class="text-lg font-semibold">{{ t('admin.unifiedGateway.preview') }}</h2><button class="btn btn-secondary text-xs" :disabled="saving || !draftId" @click="previewCurrent">{{ t('admin.unifiedGateway.previewAction') }}</button></div>
              <div class="grid gap-3 md:grid-cols-3">
                <label class="field-label">{{ t('admin.unifiedGateway.previewLane') }}<select v-model="previewLaneId" class="input mt-1" @change="syncPreviewSelection"><option v-for="lane in document.lanes" :key="lane.id" :value="lane.id">{{ lane.name || lane.code || lane.id }}</option></select></label>
                <label class="field-label">{{ t('admin.unifiedGateway.previewTarget') }}<select v-model="previewTargetId" class="input mt-1" @change="syncPreviewSelection"><option v-for="target in previewTargetOptions" :key="target.id" :value="target.id">{{ target.provider_identity || target.id }} · {{ target.upstream_model || '-' }}</option></select></label>
                <label class="field-label">{{ t('admin.unifiedGateway.previewBinding') }}<select v-model="previewBindingId" class="input mt-1"><option v-for="binding in previewBindingOptions" :key="binding.id" :value="binding.id">{{ binding.account_id || binding.id }}</option></select></label>
              </div>
              <div class="grid gap-3 md:grid-cols-3"><label class="field-label">{{ t('admin.unifiedGateway.inputTokens') }}<input v-model="previewInput" class="input mt-1" /></label><label class="field-label">{{ t('admin.unifiedGateway.outputTokens') }}<input v-model="previewOutput" class="input mt-1" /></label><label class="field-label">{{ t('admin.unifiedGateway.deliveryState') }}<select v-model="previewDeliveryState" class="input mt-1"><option value="success">success</option><option value="failed">failed</option></select></label></div>
              <div v-if="preview" class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-gray-800/60"><div class="flex justify-between"><span>{{ t('admin.unifiedGateway.estimatedCharge') }}</span><strong>{{ preview.estimated_charge }} {{ preview.currency }}</strong></div><div class="mt-1 text-xs text-gray-500">{{ t('admin.unifiedGateway.rateSource') }}: {{ preview.resolved_rate_source }} · {{ t('admin.unifiedGateway.effectiveMultiplier') }}: {{ preview.effective_multiplier }}</div><div class="mt-1 text-xs text-gray-500">{{ t('admin.unifiedGateway.dryRun') }} ({{ preview.preview_digest }})</div></div>
            </section>

            <section class="card space-y-3 p-5">
              <h2 class="text-lg font-semibold">{{ t('admin.unifiedGateway.publish') }}</h2>
              <p class="text-sm text-gray-500">{{ t('admin.unifiedGateway.publishHint') }}</p>
              <button class="btn btn-primary w-full" :disabled="saving || !draftId || !validation?.valid" @click="publishCurrent">{{ t('admin.unifiedGateway.publishAction') }}</button>
              <p class="text-xs text-gray-500">{{ t('admin.unifiedGateway.runtimeOff') }}</p>
            </section>

            <section class="card p-5"><h2 class="text-lg font-semibold">{{ t('admin.unifiedGateway.publishedConfigs') }}</h2><div v-if="configs.length === 0" class="mt-3 text-sm text-gray-500">{{ t('admin.unifiedGateway.noConfigs') }}</div><div v-for="item in configs" :key="item.id" class="mt-3 rounded-lg border border-gray-100 p-3 text-sm dark:border-gray-700"><div class="flex justify-between gap-2"><button class="truncate text-left font-semibold text-teal-700" @click="editPublished(item)">{{ item.public_model }}</button><span :class="item.readiness === 'ready' ? 'text-green-600' : 'text-amber-600'">{{ item.readiness }}</span></div><div class="mt-1 text-xs text-gray-500">{{ item.endpoint }} · {{ item.lifecycle }} · rev {{ item.revision }}</div><div class="mt-3 flex flex-wrap gap-2"><button class="btn btn-secondary text-xs" :disabled="saving" @click="editPublished(item)">{{ t('admin.unifiedGateway.editDraft') }}</button><button v-if="item.lifecycle === 'published'" class="btn btn-secondary text-xs" :disabled="saving" @click="disablePublished(item)">{{ t('admin.unifiedGateway.disableAction') }}</button><button v-if="item.lifecycle === 'disabled'" class="btn btn-secondary text-xs" :disabled="saving" @click="restorePublished(item)">{{ t('admin.unifiedGateway.restoreAction') }}</button><button class="btn btn-secondary text-xs" :disabled="saving" @click="showRevisions(item)">{{ t('admin.unifiedGateway.revisionsAction') }}</button></div><div v-if="selectedConfigId === item.id && revisions.length" class="mt-3 rounded bg-gray-50 p-2 text-xs dark:bg-gray-800/60"><div v-for="revision in revisions" :key="revision.revision" class="flex items-center justify-between gap-2 py-1"><span>rev {{ revision.revision }} · {{ revision.lifecycle }} · {{ revision.reason || '-' }}</span><button v-if="item.lifecycle === 'disabled' && revision.lifecycle === 'published'" class="text-teal-700" @click="restorePublished(item, revision.revision)">{{ t('admin.unifiedGateway.restoreAction') }}</button></div><div class="mt-3 border-t border-gray-200 pt-2 dark:border-gray-700"><div class="font-medium">{{ t('admin.unifiedGateway.snapshots') }}</div><div v-if="snapshots.length === 0" class="mt-1 text-gray-500">{{ t('admin.unifiedGateway.noSnapshots') }}</div><div v-for="snapshot in snapshots" :key="snapshot.id" class="mt-1 text-gray-500">{{ snapshot.status }} · {{ snapshot.provider_identity }} · {{ snapshot.user_charge }} {{ snapshot.currency }}</div></div></div></div></section>
          </aside>
        </div>
      </template>
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
import type { UnifiedGatewayAccountBinding, UnifiedGatewayBillingLane, UnifiedGatewayConfig, UnifiedGatewayMeta, UnifiedGatewayOptionsPage, UnifiedGatewayPricingImportResult, UnifiedGatewayPreviewResult, UnifiedGatewayRevision, UnifiedGatewayRouteTarget, UnifiedGatewaySnapshotView, UnifiedGatewayValidationResult, UnifiedGatewayEndpoint } from '@/types/unifiedGateway'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const meta = ref<UnifiedGatewayMeta | null>(null)
const options = ref<UnifiedGatewayOptionsPage | null>(null)
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
const gatewayStepUp = useStepUp()

function newBinding() { return { id: `binding_${crypto.randomUUID?.() ?? Date.now()}`, account_id: '', schedulable: true, eligibility: 'unknown', priority: 10, enabled: true, revision: 1 } }
function newTarget(endpoint: UnifiedGatewayEndpoint = 'chat_completions'): UnifiedGatewayRouteTarget { return { id: `target_${crypto.randomUUID?.() ?? Date.now()}`, provider_identity: '', upstream_model: '', endpoint, priority: 10, bindings: [newBinding()] } }
function newLane(endpoint: UnifiedGatewayEndpoint = 'chat_completions'): UnifiedGatewayBillingLane { return { id: `lane_${crypto.randomUUID?.() ?? Date.now()}`, code: '', name: '', selection_strategy: 'fixed_priority', profile: { id: `profile_${crypto.randomUUID?.() ?? Date.now()}`, version: '1', pricing_model: 'provider_metered', billing_mode: 'token', rate_mode: 'manual_only', rate_basis: 'token', pricing_schema_id: 'token_v1', currency: 'USD', base_price_semantics: 'provider_base', provider_base_unit_price: '0.000010000000', manual_base_unit_price: null, manual_upstream_multiplier: '1.000000', user_markup_multiplier: '1.200000', final_user_unit_price: null, fixed_fee: '0', minimum_charge: '0', rounding_mode: 'half_up', precision: 8, fallback_reason: 'manual_only', manual_pricing_rules: null, charge_trigger: 'success_delivery', failure_charge: 'zero' }, targets: [newTarget(endpoint)] } }
function emptyDocument(): UnifiedGatewayConfig { return { access_group_id: '', public_model: '', endpoint: 'chat_completions', lanes: [newLane()] } }
const document = reactive<UnifiedGatewayConfig>(emptyDocument())
const gateMessage = computed(() => metaLoadFailed.value || !meta.value ? t('admin.unifiedGateway.metaUnavailable') : !meta.value.admin_ui_enabled ? t('admin.unifiedGateway.uiDisabled') : !meta.value.migration_ready ? t('admin.unifiedGateway.migrationNotReady') : t('admin.unifiedGateway.capabilityUnavailable'))

function resetDocument() { Object.assign(document, emptyDocument()); draftId.value = ''; draftRevision.value = 0; configRevision.value = 0; validation.value = null; preview.value = null }
function addLane() { document.lanes.push(newLane(document.endpoint)) }
function removeLane(index: number) { document.lanes.splice(index, 1) }
function addTarget(lane: UnifiedGatewayBillingLane) { lane.targets.push(newTarget(document.endpoint)) }
function removeTarget(lane: UnifiedGatewayBillingLane, index: number) { if (lane.targets.length > 1) lane.targets.splice(index, 1) }
function addBinding(target: UnifiedGatewayRouteTarget) { target.bindings.push(newBinding()) }
function removeBinding(target: UnifiedGatewayRouteTarget, index: number) { if (target.bindings.length > 1) target.bindings.splice(index, 1) }
function accountOption(id: string) { return options.value?.items.accounts.find(account => account.id === id) }
function syncBindingFromAccount(binding: UnifiedGatewayAccountBinding) { const account = accountOption(binding.account_id); if (!account) return; binding.schedulable = account.schedulable === true; binding.eligibility = account.status === 'active' && account.schedulable ? 'eligible' : 'ineligible' }
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
    if (!profile.manual_pricing_rules) profile.manual_pricing_rules = { formula_id: 'flat_unit_price', unit: manualRuleUnitFor(profile.billing_mode), unit_price: '0.01000000' }
    profile.manual_pricing_rules.unit = manualRuleUnitFor(profile.billing_mode) as 'request' | 'image' | 'video_task'
  } else {
    profile.manual_pricing_rules = null
    if (profile.base_price_semantics === 'final_user_price') {
      profile.provider_base_unit_price = null
      profile.manual_base_unit_price = null
      profile.manual_upstream_multiplier = null
      profile.user_markup_multiplier = null
      if (!profile.final_user_unit_price) profile.final_user_unit_price = '0.00100000'
    } else {
      profile.final_user_unit_price = null
      if (!profile.provider_base_unit_price && !profile.manual_base_unit_price) profile.provider_base_unit_price = '0.000010000000'
      if (!profile.manual_upstream_multiplier) profile.manual_upstream_multiplier = '1.000000'
      if (!profile.user_markup_multiplier) profile.user_markup_multiplier = '1.200000'
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

async function loadPage() { loading.value = true; metaLoadFailed.value = false; try { meta.value = await adminAPI.unifiedGateway.getMeta(); if (meta.value.admin_ui_enabled && meta.value.migration_ready && meta.value.capabilities?.read === true) { options.value = await adminAPI.unifiedGateway.getOptions(); const page = await adminAPI.unifiedGateway.listConfigs(''); configs.value = page.items } else { options.value = null; configs.value = [] } } catch (error: any) { meta.value = null; options.value = null; configs.value = []; metaLoadFailed.value = true; appStore.showError(error?.message || t('admin.unifiedGateway.loadFailed')) } finally { loading.value = false } }
async function saveDraft() { saving.value = true; try { if (!draftId.value) { const draft = await adminAPI.unifiedGateway.createDraft(JSON.parse(JSON.stringify(document))); draftId.value = draft.id; draftRevision.value = draft.revision; configRevision.value = draft.document.revision ?? 0; Object.assign(document, draft.document); } else { const draft = await adminAPI.unifiedGateway.updateDraft(draftId.value, JSON.parse(JSON.stringify(document)), draftRevision.value); draftRevision.value = draft.revision; Object.assign(document, draft.document) } appStore.showSuccess(t('admin.unifiedGateway.saved')) } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.saveFailed')) } finally { saving.value = false } }
async function validateCurrent() { saving.value = true; try { if (!draftId.value) { await saveDraft(); } validation.value = await adminAPI.unifiedGateway.validateDraft(draftId.value, JSON.parse(JSON.stringify(document)), draftRevision.value) } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.validateFailed')) } finally { saving.value = false } }
async function previewCurrent() { saving.value = true; try { if (!draftId.value) await saveDraft(); syncPreviewSelection(); const lane = previewLane.value; const target = previewTarget.value; const binding = previewBindingOptions.value.find(item => item.id === previewBindingId.value) ?? previewBindingOptions.value[0]; if (!lane || !target || !binding) throw new Error(t('admin.unifiedGateway.previewSelectionRequired')); preview.value = await adminAPI.unifiedGateway.previewDraft(draftId.value, { lane_id: lane.id, target_id: target.id, binding_id: binding.id, input_tokens: previewInput.value, output_tokens: previewOutput.value, delivery_state: previewDeliveryState.value }, JSON.parse(JSON.stringify(document))) } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.previewFailed')) } finally { saving.value = false } }
function operationKey(prefix: string) { if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return `${prefix}-${crypto.randomUUID()}`; return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}` }
function reportSensitiveActionError(error: unknown) { if (isStepUpCancelled(error)) return; if (isStepUpBlocked(error)) { appStore.showError(stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? t('admin.unifiedGateway.stepUpApiKeyForbidden') : t('admin.unifiedGateway.stepUpUnavailable')); return } appStore.showError((error as any)?.message || t('admin.unifiedGateway.publishFailed')) }
async function publishCurrent() { saving.value = true; const key = operationKey('unified-publish'); try { if (!draftId.value) await saveDraft(); if (!validation.value?.validation_token) validation.value = await adminAPI.unifiedGateway.validateDraft(draftId.value, JSON.parse(JSON.stringify(document)), draftRevision.value); if (!validation.value?.validation_token) throw new Error(t('admin.unifiedGateway.validationRequired')); const result = await gatewayStepUp.run(() => adminAPI.unifiedGateway.publishDraft(draftId.value, configRevision.value, validation.value!.validation_token!, 'admin publish', key)); appStore.showSuccess(t('admin.unifiedGateway.published')); configs.value = [result, ...configs.value.filter(item => item.id !== result.id)]; Object.assign(document, result); configRevision.value = result.revision ?? configRevision.value; validation.value = null } catch (error: unknown) { reportSensitiveActionError(error) } finally { saving.value = false } }
async function loadRevisionAndSnapshots(item: UnifiedGatewayConfig) { if (!item.id) return; revisions.value = await adminAPI.unifiedGateway.listRevisions(item.id); const page = await adminAPI.unifiedGateway.listSnapshots({ access_group_id: item.access_group_id, public_model: item.public_model, page: 1, page_size: 10 }); snapshots.value = page.items }
async function editPublished(item: UnifiedGatewayConfig) { if (!item.id) return; saving.value = true; try { const current = await adminAPI.unifiedGateway.getConfig(item.id); const draft = await adminAPI.unifiedGateway.createDraftFromConfig(item.id); draftId.value = draft.id; draftRevision.value = draft.revision; configRevision.value = current.revision ?? 0; selectedConfigId.value = item.id; await loadRevisionAndSnapshots(item); Object.assign(document, draft.document); validation.value = null; preview.value = null; appStore.showSuccess(t('admin.unifiedGateway.draftOpened')) } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.loadFailed')) } finally { saving.value = false } }
async function showRevisions(item: UnifiedGatewayConfig) { if (!item.id) return; selectedConfigId.value = item.id; try { await loadRevisionAndSnapshots(item) } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.loadFailed')) } }
async function disablePublished(item: UnifiedGatewayConfig) { if (!item.id || item.revision === undefined) return; saving.value = true; const key = operationKey('unified-disable'); try { const result = await gatewayStepUp.run(() => adminAPI.unifiedGateway.disableConfig(item.id!, item.revision!, 'admin disable', key)); configs.value = configs.value.map(config => config.id === result.id ? result : config); appStore.showSuccess(t('admin.unifiedGateway.disabled')) } catch (error: unknown) { reportSensitiveActionError(error) } finally { saving.value = false } }
async function restorePublished(item: UnifiedGatewayConfig, sourceRevision?: number) { if (!item.id || item.revision === undefined) return; const source = sourceRevision ?? revisions.value.find(revision => revision.lifecycle === 'published')?.revision; if (!source) return; saving.value = true; const key = operationKey('unified-restore'); try { const result = await gatewayStepUp.run(() => adminAPI.unifiedGateway.restoreConfig(item.id!, source, item.revision!, 'admin restore', key)); configs.value = configs.value.map(config => config.id === result.id ? result : config); appStore.showSuccess(t('admin.unifiedGateway.restored')) } catch (error: unknown) { reportSensitiveActionError(error) } finally { saving.value = false } }
async function probeBinding(binding: UnifiedGatewayAccountBinding) { if (!binding.id) return; saving.value = true; const key = operationKey('unified-probe'); try { const result = await gatewayStepUp.run(() => adminAPI.unifiedGateway.probeBinding(binding.id, key)); probeResults.value[binding.id] = String(result.status || result.reason || 'unsupported') } catch (error: unknown) { reportSensitiveActionError(error) } finally { saving.value = false } }
async function previewPricingImport(lane: UnifiedGatewayBillingLane) { if (!draftId.value || !lane.pricing_source_group_id) return; saving.value = true; try { const result = await adminAPI.unifiedGateway.pricingImportPreview(draftId.value, lane.id, lane.pricing_source_group_id, JSON.parse(JSON.stringify(document))); pricingImportPreviews.value[lane.id] = result } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.pricingImportFailed')) } finally { saving.value = false } }
async function applyPricingImport(lane: UnifiedGatewayBillingLane) { const sourceGroupId = lane.pricing_source_group_id; const previewResult = pricingImportPreviews.value[lane.id]; if (!draftId.value || !sourceGroupId || !previewResult) return; saving.value = true; const key = operationKey('unified-pricing-import'); try { const result = await adminAPI.unifiedGateway.pricingImportApply(draftId.value, lane.id, sourceGroupId, draftRevision.value, previewResult.import_digest, key); if (result.draft) { draftRevision.value = result.draft.revision; Object.assign(document, result.draft.document) } pricingImports.value[lane.id] = result.source_revision; delete pricingImportPreviews.value[lane.id]; appStore.showSuccess(t('admin.unifiedGateway.pricingImported')) } catch (error: any) { appStore.showError(error?.message || t('admin.unifiedGateway.pricingImportFailed')) } finally { saving.value = false } }
onMounted(loadPage)
</script>

<style scoped>
.field-label { @apply text-xs font-medium text-gray-700 dark:text-gray-300; }
</style>

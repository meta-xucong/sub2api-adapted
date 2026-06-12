<template>
  <div class="veyra-login-page">
    <header class="veyra-login-header">
      <a class="veyra-brand" href="/" aria-label="Veyra Agent">
        <strong>Veyra Agent</strong>
        <span>Unified console</span>
      </a>
      <a class="veyra-back-link" href="/">返回首页</a>
    </header>

    <main class="veyra-login-main">
      <section class="veyra-login-copy" aria-labelledby="veyraLoginTitle">
        <p class="veyra-eyebrow">Veyra Account</p>
        <h1 id="veyraLoginTitle">登录后进入你的 Agent 工具矩阵。</h1>
        <p>
          一个账号连接聚合平台与 Alchemy Media Agent。登录后会按入口自动回到对应应用。
        </p>
        <div class="veyra-login-notes" aria-label="账户能力">
          <span>聚合平台</span>
          <span>Alchemy</span>
          <span>统一积分</span>
        </div>
      </section>

      <section class="veyra-login-panel" aria-label="登录表单">
        <div class="veyra-panel-head">
          <p class="veyra-eyebrow">Sign in</p>
          <h2>{{ t('auth.welcomeBack') }}</h2>
          <span>{{ t('auth.signInToAccount') }}</span>
        </div>

        <form @submit.prevent="handleLogin" class="veyra-form">
          <label class="veyra-field" for="email">
            <span>{{ t('auth.emailLabel') }}</span>
            <div class="veyra-input-wrap">
              <Icon name="mail" size="md" class="veyra-input-icon" />
              <input
                id="email"
                v-model="formData.email"
                type="email"
                required
                autofocus
                autocomplete="email"
                :disabled="authActionDisabled"
                class="veyra-input"
                :class="{ 'veyra-input-error': errors.email }"
                :placeholder="t('auth.emailPlaceholder')"
              />
            </div>
          </label>

          <label class="veyra-field" for="password">
            <span>{{ t('auth.passwordLabel') }}</span>
            <div class="veyra-input-wrap">
              <Icon name="lock" size="md" class="veyra-input-icon" />
              <input
                id="password"
                v-model="formData.password"
                :type="showPassword ? 'text' : 'password'"
                required
                autocomplete="current-password"
                :disabled="authActionDisabled"
                class="veyra-input veyra-input-password"
                :class="{ 'veyra-input-error': errors.password }"
                :placeholder="t('auth.passwordPlaceholder')"
              />
              <button
                type="button"
                @click="showPassword = !showPassword"
                :disabled="authActionDisabled"
                class="veyra-password-toggle"
                :aria-label="showPassword ? '隐藏密码' : '显示密码'"
              >
                <Icon v-if="showPassword" name="eyeOff" size="md" />
                <Icon v-else name="eye" size="md" />
              </button>
            </div>
          </label>

          <div class="veyra-form-row">
            <span></span>
            <router-link
              v-if="passwordResetEnabled && !backendModeEnabled"
              to="/forgot-password"
              class="veyra-text-link"
            >
              {{ t('auth.forgotPassword') }}
            </router-link>
          </div>

          <p v-if="errorMessage" class="veyra-error" role="alert">{{ errorMessage }}</p>

          <div v-if="turnstileEnabled && turnstileSiteKey" class="veyra-turnstile">
            <TurnstileWidget
              ref="turnstileRef"
              :site-key="turnstileSiteKey"
              @verify="onTurnstileVerify"
              @expire="onTurnstileExpire"
              @error="onTurnstileError"
            />
          </div>

          <button
            type="submit"
            :disabled="authActionDisabled || (turnstileEnabled && !turnstileToken)"
            class="veyra-submit"
          >
            <svg
              v-if="isLoading"
              class="veyra-spinner"
              fill="none"
              viewBox="0 0 24 24"
              aria-hidden="true"
            >
              <circle
                class="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                stroke-width="4"
              ></circle>
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              ></path>
            </svg>
            <Icon v-else name="login" size="md" />
            {{ isLoading ? t('auth.signingIn') : t('auth.signIn') }}
          </button>

          <LoginAgreementPrompt
            v-if="loginAgreementEnabled"
            :accepted="agreementAccepted"
            :documents="loginAgreementDocuments"
            :mode="loginAgreementMode"
            :updated-at="loginAgreementUpdatedAt"
            :visible="showAgreementModal"
            @accept="acceptLoginAgreement"
            @reject="rejectLoginAgreement"
            @open="showAgreementModal = true"
          />

          <div v-if="showOAuthLogin" class="veyra-oauth-block">
            <div class="veyra-divider">
              <span>{{ t('auth.oauthOrContinue') }}</span>
            </div>

            <EmailOAuthButtons
              :disabled="authActionDisabled"
              :github-enabled="githubOAuthEnabled"
              :google-enabled="googleOAuthEnabled"
              :show-divider="false"
            />

            <LinuxDoOAuthSection
              v-if="linuxdoOAuthEnabled"
              :disabled="authActionDisabled"
              :show-divider="false"
            />
            <DingTalkOAuthSection
              v-if="dingtalkOAuthEnabled"
              :disabled="authActionDisabled"
              :show-divider="false"
            />
            <WechatOAuthSection
              v-if="wechatOAuthEnabled"
              :disabled="authActionDisabled"
              :show-divider="false"
            />
            <OidcOAuthSection
              v-if="oidcOAuthEnabled"
              :disabled="authActionDisabled"
              :provider-name="oidcOAuthProviderName"
              :show-divider="false"
            />
          </div>
        </form>

        <p v-if="!backendModeEnabled" class="veyra-login-footer">
          {{ t('auth.dontHaveAccount') }}
          <router-link to="/register" class="veyra-text-link">
            {{ t('auth.signUp') }}
          </router-link>
        </p>
      </section>
    </main>
  </div>

  <!-- 2FA Modal -->
  <TotpLoginModal
    v-if="show2FAModal"
    ref="totpModalRef"
    :temp-token="totpTempToken"
    :user-email-masked="totpUserEmailMasked"
    @verify="handle2FAVerify"
    @cancel="handle2FACancel"
  />
</template>

<script setup lang="ts">
import { computed, ref, reactive, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import LinuxDoOAuthSection from '@/components/auth/LinuxDoOAuthSection.vue'
import DingTalkOAuthSection from '@/components/auth/DingTalkOAuthSection.vue'
import OidcOAuthSection from '@/components/auth/OidcOAuthSection.vue'
import WechatOAuthSection from '@/components/auth/WechatOAuthSection.vue'
import EmailOAuthButtons from '@/components/auth/EmailOAuthButtons.vue'
import LoginAgreementPrompt from '@/components/auth/LoginAgreementPrompt.vue'
import TotpLoginModal from '@/components/auth/TotpLoginModal.vue'
import Icon from '@/components/icons/Icon.vue'
import TurnstileWidget from '@/components/TurnstileWidget.vue'
import { useAuthStore, useAppStore } from '@/stores'
import { getPublicSettings, isTotp2FARequired, isWeChatWebOAuthEnabled } from '@/api/auth'
import type { LoginAgreementDocument, TotpLoginResponse } from '@/types'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { clearAllAffiliateReferralCodes } from '@/utils/oauthAffiliate'

const { t } = useI18n()
const LOGIN_AGREEMENT_STORAGE_KEY = 'sub2api_login_agreement_consent'

// ==================== Router & Stores ====================

const router = useRouter()
const authStore = useAuthStore()
const appStore = useAppStore()

// ==================== State ====================

const isLoading = ref<boolean>(false)
const errorMessage = ref<string>('')
const showPassword = ref<boolean>(false)
const publicSettingsLoaded = ref<boolean>(false)

// Public settings
const turnstileEnabled = ref<boolean>(false)
const turnstileSiteKey = ref<string>('')
const linuxdoOAuthEnabled = ref<boolean>(false)
const dingtalkOAuthEnabled = ref<boolean>(false)
const wechatOAuthEnabled = ref<boolean>(false)
const backendModeEnabled = ref<boolean>(false)
const oidcOAuthEnabled = ref<boolean>(false)
const oidcOAuthProviderName = ref<string>('OIDC')
const githubOAuthEnabled = ref<boolean>(false)
const googleOAuthEnabled = ref<boolean>(false)
const passwordResetEnabled = ref<boolean>(false)
const loginAgreementEnabled = ref<boolean>(false)
const loginAgreementMode = ref<'modal' | 'checkbox' | string>('modal')
const loginAgreementUpdatedAt = ref<string>('')
const loginAgreementRevision = ref<string>('')
const loginAgreementDocuments = ref<LoginAgreementDocument[]>([])
const agreementAccepted = ref<boolean>(false)
const showAgreementModal = ref<boolean>(false)

// Turnstile
const turnstileRef = ref<InstanceType<typeof TurnstileWidget> | null>(null)
const turnstileToken = ref<string>('')

// 2FA state
const show2FAModal = ref<boolean>(false)
const totpTempToken = ref<string>('')
const totpUserEmailMasked = ref<string>('')

function resolveLoginRedirect(): string {
  return sanitizeLoginRedirect(router.currentRoute.value.query.redirect as string)
}

async function applyLoginRedirect(redirectTo: string): Promise<void> {
  if (redirectTo.startsWith('/_veyra/return')) {
    window.location.assign(redirectTo)
    return
  }
  await router.push(redirectTo)
}

function sanitizeLoginRedirect(path: string | undefined): string {
  if (!path || !path.startsWith('/') || path.startsWith('//')) {
    return '/dashboard'
  }
  if (path.includes('://') || path.includes('\n') || path.includes('\r')) {
    return '/dashboard'
  }
  return path
}
const totpModalRef = ref<InstanceType<typeof TotpLoginModal> | null>(null)

const formData = reactive({
  email: '',
  password: ''
})

const errors = reactive({
  email: '',
  password: '',
  turnstile: ''
})

const validationToastMessage = computed(
  () => errors.email || errors.password || errors.turnstile || ''
)

const agreementGateActive = computed(
  () => loginAgreementEnabled.value && !agreementAccepted.value
)

const authActionDisabled = computed(
  () => isLoading.value || !publicSettingsLoaded.value || agreementGateActive.value
)

const showOAuthLogin = computed(
  () =>
    !backendModeEnabled.value &&
    (linuxdoOAuthEnabled.value ||
      dingtalkOAuthEnabled.value ||
      wechatOAuthEnabled.value ||
      oidcOAuthEnabled.value ||
      githubOAuthEnabled.value ||
      googleOAuthEnabled.value)
)

watch(validationToastMessage, (value, previousValue) => {
  if (value && value !== previousValue) {
    appStore.showError(value)
  }
})

// ==================== Lifecycle ====================

onMounted(async () => {
  const expiredFlag = sessionStorage.getItem('auth_expired')
  if (expiredFlag) {
    sessionStorage.removeItem('auth_expired')
    const message = t('auth.reloginRequired')
    errorMessage.value = message
    appStore.showWarning(message)
  }

  try {
    const settings = await getPublicSettings()
    turnstileEnabled.value = settings.turnstile_enabled
    turnstileSiteKey.value = settings.turnstile_site_key || ''
    linuxdoOAuthEnabled.value = settings.linuxdo_oauth_enabled
    dingtalkOAuthEnabled.value = settings.dingtalk_oauth_enabled ?? false
    wechatOAuthEnabled.value = isWeChatWebOAuthEnabled(settings)
    backendModeEnabled.value = settings.backend_mode_enabled
    oidcOAuthEnabled.value = settings.oidc_oauth_enabled
    oidcOAuthProviderName.value = settings.oidc_oauth_provider_name || 'OIDC'
    githubOAuthEnabled.value = settings.github_oauth_enabled
    googleOAuthEnabled.value = settings.google_oauth_enabled
    backendModeEnabled.value = settings.backend_mode_enabled
    passwordResetEnabled.value = settings.password_reset_enabled
    applyLoginAgreementSettings(settings)
  } catch (error) {
    console.error('Failed to load public settings:', error)
    loginAgreementEnabled.value = false
    agreementAccepted.value = true
  } finally {
    publicSettingsLoaded.value = true
  }
})

// ==================== Login Agreement ====================

function applyLoginAgreementSettings(settings: {
  login_agreement_enabled?: boolean
  login_agreement_mode?: string
  login_agreement_updated_at?: string
  login_agreement_revision?: string
  login_agreement_documents?: LoginAgreementDocument[]
}): void {
  const documents = Array.isArray(settings.login_agreement_documents)
    ? settings.login_agreement_documents.filter((doc) => doc.title?.trim())
    : []
  loginAgreementDocuments.value = documents
  loginAgreementEnabled.value = settings.login_agreement_enabled === true && documents.length > 0
  loginAgreementMode.value = settings.login_agreement_mode === 'checkbox' ? 'checkbox' : 'modal'
  loginAgreementUpdatedAt.value = settings.login_agreement_updated_at || ''
  loginAgreementRevision.value =
    settings.login_agreement_revision ||
    `${loginAgreementUpdatedAt.value}:${documents.map((doc) => `${doc.id}:${doc.title}`).join('|')}`

  agreementAccepted.value = !loginAgreementEnabled.value || hasAcceptedLoginAgreement(loginAgreementRevision.value)
  showAgreementModal.value =
    loginAgreementEnabled.value && !agreementAccepted.value && loginAgreementMode.value !== 'checkbox'
}

function hasAcceptedLoginAgreement(revision: string): boolean {
  if (!revision) {
    return false
  }
  try {
    const raw = localStorage.getItem(LOGIN_AGREEMENT_STORAGE_KEY)
    if (!raw) {
      return false
    }
    const parsed = JSON.parse(raw) as { revision?: string }
    return parsed.revision === revision
  } catch {
    return false
  }
}

function acceptLoginAgreement(): void {
  if (loginAgreementRevision.value) {
    localStorage.setItem(
      LOGIN_AGREEMENT_STORAGE_KEY,
      JSON.stringify({
        revision: loginAgreementRevision.value,
        accepted_at: new Date().toISOString()
      })
    )
  }
  agreementAccepted.value = true
  showAgreementModal.value = false
}

function rejectLoginAgreement(): void {
  localStorage.removeItem(LOGIN_AGREEMENT_STORAGE_KEY)
  agreementAccepted.value = false
  showAgreementModal.value = false
  appStore.showWarning('未同意最新条款前，无法输入账号密码或使用快捷登录。')
}

// ==================== Turnstile Handlers ====================

function onTurnstileVerify(token: string): void {
  turnstileToken.value = token
  errors.turnstile = ''
}

function onTurnstileExpire(): void {
  turnstileToken.value = ''
  errors.turnstile = t('auth.turnstileExpired')
}

function onTurnstileError(): void {
  turnstileToken.value = ''
  errors.turnstile = t('auth.turnstileFailed')
}

// ==================== Validation ====================

function validateForm(): boolean {
  // Reset errors
  errors.email = ''
  errors.password = ''
  errors.turnstile = ''

  let isValid = true

  if (agreementGateActive.value) {
    appStore.showWarning('请先阅读并同意最新条款后再登录。')
    if (loginAgreementMode.value !== 'checkbox') {
      showAgreementModal.value = true
    }
    return false
  }

  // Email validation
  if (!formData.email.trim()) {
    errors.email = t('auth.emailRequired')
    isValid = false
  } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(formData.email)) {
    errors.email = t('auth.invalidEmail')
    isValid = false
  }

  // Password validation
  if (!formData.password) {
    errors.password = t('auth.passwordRequired')
    isValid = false
  } else if (formData.password.length < 6) {
    errors.password = t('auth.passwordMinLength')
    isValid = false
  }

  // Turnstile validation
  if (turnstileEnabled.value && !turnstileToken.value) {
    errors.turnstile = t('auth.completeVerification')
    isValid = false
  }

  return isValid
}

// ==================== Form Handlers ====================

async function handleLogin(): Promise<void> {
  // Clear previous error
  errorMessage.value = ''

  // Validate form
  if (!validateForm()) {
    return
  }

  isLoading.value = true

  try {
    // Call auth store login
    const response = await authStore.login({
      email: formData.email,
      password: formData.password,
      turnstile_token: turnstileEnabled.value ? turnstileToken.value : undefined
    })

    // Check if 2FA is required
    if (isTotp2FARequired(response)) {
      const totpResponse = response as TotpLoginResponse
      totpTempToken.value = totpResponse.temp_token || ''
      totpUserEmailMasked.value = totpResponse.user_email_masked || ''
      show2FAModal.value = true
      isLoading.value = false
      return
    }

    // Show success toast
    clearAllAffiliateReferralCodes()
    appStore.showSuccess(t('auth.loginSuccess'))

    await applyLoginRedirect(resolveLoginRedirect())
  } catch (error: unknown) {
    // Reset Turnstile on error
    if (turnstileRef.value) {
      turnstileRef.value.reset()
      turnstileToken.value = ''
    }

    errorMessage.value = extractI18nErrorMessage(error, t, 'auth.errors', t('auth.loginFailed'))

    // Also show error toast
    appStore.showError(errorMessage.value)
  } finally {
    isLoading.value = false
  }
}

// ==================== 2FA Handlers ====================

async function handle2FAVerify(code: string): Promise<void> {
  if (totpModalRef.value) {
    totpModalRef.value.setVerifying(true)
  }

  try {
    await authStore.login2FA(totpTempToken.value, code)

    // Close modal and show success
    show2FAModal.value = false
    clearAllAffiliateReferralCodes()
    appStore.showSuccess(t('auth.loginSuccess'))

    await applyLoginRedirect(resolveLoginRedirect())
  } catch (error: unknown) {
    const err = error as { message?: string; response?: { data?: { message?: string } } }
    const message = err.response?.data?.message || err.message || t('profile.totp.loginFailed')

    if (totpModalRef.value) {
      totpModalRef.value.setError(message)
      totpModalRef.value.setVerifying(false)
    }
  }
}

function handle2FACancel(): void {
  show2FAModal.value = false
  totpTempToken.value = ''
  totpUserEmailMasked.value = ''
}
</script>

<style scoped>
.veyra-login-page {
  min-height: 100vh;
  display: grid;
  grid-template-rows: auto 1fr;
  color: #171512;
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.64), rgba(244, 238, 224, 0.48)),
    #fbfaf7;
}

.veyra-login-header {
  min-height: 72px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  padding: 22px clamp(18px, 4vw, 52px);
}

.veyra-brand {
  color: inherit;
  text-decoration: none;
}

.veyra-brand strong {
  display: block;
  font-family: Georgia, 'Times New Roman', serif;
  font-size: 22px;
  line-height: 1;
  font-weight: 400;
}

.veyra-brand span,
.veyra-eyebrow {
  color: #777066;
  font-size: 12px;
  line-height: 1.4;
}

.veyra-eyebrow {
  margin: 0;
  text-transform: uppercase;
}

.veyra-back-link,
.veyra-text-link {
  color: #496f5a;
  font-size: 13px;
  font-weight: 650;
  text-decoration: none;
}

.veyra-back-link {
  min-height: 34px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0 13px;
  border: 1px solid rgba(73, 111, 90, 0.2);
  border-radius: 999px;
  background: rgba(255, 255, 252, 0.68);
}

.veyra-login-main {
  width: min(1120px, calc(100% - 32px));
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(360px, 440px);
  align-items: center;
  gap: clamp(30px, 6vw, 86px);
  margin: 0 auto;
  padding: 34px 0 76px;
}

.veyra-login-copy h1 {
  max-width: 660px;
  margin: 12px 0 18px;
  font-size: clamp(42px, 6vw, 78px);
  line-height: 1.02;
  font-weight: 300;
  letter-spacing: 0;
}

.veyra-login-copy p:not(.veyra-eyebrow) {
  max-width: 560px;
  margin: 0;
  color: #6f685f;
  font-size: 16px;
  line-height: 1.8;
}

.veyra-login-notes {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 26px;
}

.veyra-login-notes span {
  min-height: 32px;
  display: inline-flex;
  align-items: center;
  padding: 0 12px;
  border: 1px solid rgba(130, 143, 121, 0.22);
  border-radius: 999px;
  color: #4d6b58;
  background: rgba(130, 143, 121, 0.1);
  font-size: 13px;
  font-weight: 650;
}

.veyra-login-panel {
  padding: 28px;
  border: 1px solid rgba(33, 31, 27, 0.1);
  border-radius: 18px;
  background: rgba(255, 255, 252, 0.82);
  box-shadow: 0 26px 70px rgba(66, 58, 45, 0.12);
  backdrop-filter: blur(18px);
}

.veyra-panel-head {
  padding-bottom: 22px;
  border-bottom: 1px solid rgba(33, 31, 27, 0.09);
}

.veyra-panel-head h2 {
  margin: 8px 0 6px;
  color: #171512;
  font-family: Georgia, 'Times New Roman', serif;
  font-size: 30px;
  line-height: 1.1;
  font-weight: 400;
}

.veyra-panel-head span {
  color: #777066;
  font-size: 14px;
}

.veyra-form {
  display: grid;
  gap: 16px;
  padding-top: 22px;
}

.veyra-field {
  display: grid;
  gap: 8px;
  color: #4f4a43;
  font-size: 13px;
  font-weight: 650;
}

.veyra-input-wrap {
  position: relative;
}

.veyra-input-icon {
  position: absolute;
  top: 50%;
  left: 14px;
  width: 18px;
  height: 18px;
  color: #8b8378;
  transform: translateY(-50%);
  pointer-events: none;
}

.veyra-input {
  width: 100%;
  min-height: 48px;
  padding: 0 14px 0 44px;
  border: 1px solid rgba(33, 31, 27, 0.14);
  border-radius: 12px;
  outline: none;
  color: #171512;
  background: rgba(255, 255, 255, 0.78);
  font-size: 15px;
  transition: border-color 160ms ease, box-shadow 160ms ease, background 160ms ease;
}

.veyra-input:focus {
  border-color: rgba(73, 111, 90, 0.52);
  background: #fff;
  box-shadow: 0 0 0 4px rgba(73, 111, 90, 0.1);
}

.veyra-input-error {
  border-color: rgba(173, 73, 55, 0.58);
}

.veyra-input-password {
  padding-right: 48px;
}

.veyra-password-toggle {
  position: absolute;
  top: 0;
  right: 0;
  width: 46px;
  height: 48px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  color: #777066;
  background: transparent;
  cursor: pointer;
}

.veyra-form-row {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  min-height: 18px;
}

.veyra-error {
  margin: 0;
  padding: 10px 12px;
  border: 1px solid rgba(173, 73, 55, 0.22);
  border-radius: 10px;
  color: #93392c;
  background: rgba(173, 73, 55, 0.08);
  font-size: 13px;
}

.veyra-turnstile {
  overflow: hidden;
}

.veyra-submit {
  min-height: 48px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: 0;
  border-radius: 12px;
  color: #fff;
  background: #2f5a45;
  font-size: 15px;
  font-weight: 750;
  cursor: pointer;
  transition: transform 160ms ease, opacity 160ms ease, background 160ms ease;
}

.veyra-submit:not(:disabled):hover {
  background: #274c3b;
  transform: translateY(-1px);
}

.veyra-submit:disabled {
  cursor: not-allowed;
  opacity: 0.58;
}

.veyra-spinner {
  width: 18px;
  height: 18px;
  animation: veyra-spin 1s linear infinite;
}

.veyra-oauth-block {
  display: grid;
  gap: 12px;
  padding-top: 2px;
}

.veyra-divider {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
  gap: 10px;
  color: #8b8378;
  font-size: 12px;
}

.veyra-divider::before,
.veyra-divider::after {
  height: 1px;
  content: '';
  background: rgba(33, 31, 27, 0.12);
}

.veyra-login-footer {
  margin: 18px 0 0;
  color: #777066;
  font-size: 14px;
  text-align: center;
}

@keyframes veyra-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 900px) {
  .veyra-login-main {
    grid-template-columns: 1fr;
    align-items: start;
    gap: 28px;
    padding-top: 18px;
  }

  .veyra-login-copy h1 {
    font-size: clamp(38px, 12vw, 58px);
  }
}

@media (max-width: 560px) {
  .veyra-login-header {
    padding: 18px 16px;
  }

  .veyra-login-main {
    width: calc(100% - 24px);
    padding-bottom: 38px;
  }

  .veyra-login-panel {
    padding: 20px;
    border-radius: 14px;
  }
}
</style>

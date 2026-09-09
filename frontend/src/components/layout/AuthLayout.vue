<template>
  <main class="auth-page" data-testid="auth-layout">
    <div class="auth-background" aria-hidden="true">
      <span class="auth-background-orb auth-background-orb-primary"></span>
      <span class="auth-background-orb auth-background-orb-secondary"></span>
      <span class="auth-background-grid"></span>
    </div>

    <div class="auth-shell">
      <header class="auth-header">
        <div class="auth-brand-lockup">
          <span class="auth-brand-mark" aria-hidden="true">
            <img v-if="siteLogo" :src="siteLogo" :alt="`${siteName} logo`" />
            <span v-else>{{ siteInitial }}</span>
          </span>
          <div class="auth-brand-copy">
            <h1>{{ siteName }}</h1>
            <p>{{ siteSubtitle }}</p>
          </div>
        </div>
        <span class="auth-brand-rule" aria-hidden="true"></span>
      </header>

      <div class="auth-layout">
        <section class="auth-intro" :aria-label="siteName">
          <div class="auth-intro-index" aria-hidden="true">01</div>
          <div class="auth-intro-line" aria-hidden="true"></div>
          <p class="auth-intro-label">{{ siteSubtitle }}</p>
          <p class="auth-intro-copy">
            {{ siteName }}
          </p>
          <div class="auth-intro-signal" aria-hidden="true">
            <span></span>
            <span></span>
            <span></span>
            <span></span>
          </div>
        </section>

        <section class="auth-panel">
          <div class="auth-panel-content">
            <slot />
          </div>
        </section>
      </div>

      <footer class="auth-footer">
        <div class="auth-footer-links">
          <slot name="footer" />
        </div>
        <p>&copy; {{ currentYear }} {{ siteName }}. All rights reserved.</p>
      </footer>
    </div>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteInitial = computed(() => siteName.value.trim().slice(0, 1).toUpperCase() || 'S')
const siteLogo = computed(() =>
  sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true })
)
const siteSubtitle = computed(
  () => appStore.cachedPublicSettings?.site_subtitle || 'Subscription to API Conversion Platform'
)

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
.auth-page {
  --auth-ink: #181915;
  --auth-muted: #84857d;
  --auth-paper: #fbfaf7;
  --auth-surface: rgba(255, 255, 252, 0.78);
  --auth-line: rgba(24, 25, 21, 0.14);
  --auth-sage: #828f79;
  --auth-sage-deep: #68755f;

  position: relative;
  min-height: 100vh;
  overflow: hidden;
  color: var(--auth-ink);
  background: var(--auth-paper);
  font-family: -apple-system, BlinkMacSystemFont, 'SF Pro Text', 'Segoe UI', 'PingFang SC', sans-serif;
  -webkit-font-smoothing: antialiased;
}

.auth-background,
.auth-background-grid,
.auth-background-orb {
  pointer-events: none;
  position: absolute;
}

.auth-background {
  inset: 0;
  overflow: hidden;
}

.auth-background-grid {
  inset: 0;
  opacity: 0.55;
  background-image:
    linear-gradient(rgba(24, 25, 21, 0.035) 1px, transparent 1px),
    linear-gradient(90deg, rgba(24, 25, 21, 0.035) 1px, transparent 1px);
  background-size: 72px 72px;
  mask-image: linear-gradient(to bottom, black, transparent 78%);
}

.auth-background-orb {
  border-radius: 999px;
  filter: blur(2px);
}

.auth-background-orb-primary {
  top: -18rem;
  right: -14rem;
  width: 34rem;
  height: 34rem;
  background: rgba(130, 143, 121, 0.16);
}

.auth-background-orb-secondary {
  bottom: -24rem;
  left: -18rem;
  width: 42rem;
  height: 42rem;
  background: rgba(130, 143, 121, 0.1);
}

.auth-shell {
  position: relative;
  z-index: 1;
  display: flex;
  flex-direction: column;
  width: min(1080px, 100%);
  min-height: 100vh;
  margin: 0 auto;
  padding: 32px 26px 42px;
}

.auth-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
}

.auth-brand-lockup {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.auth-brand-mark {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  width: 42px;
  height: 42px;
  overflow: hidden;
  border: 1px solid var(--auth-line);
  border-radius: 12px;
  background: rgba(255, 255, 252, 0.72);
  color: var(--auth-sage-deep);
  font-family: Georgia, 'Times New Roman', serif;
  font-size: 20px;
  font-weight: 400;
  box-shadow: 0 10px 30px rgba(24, 25, 21, 0.06);
}

.auth-brand-mark img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: contain;
}

.auth-brand-copy {
  min-width: 0;
}

.auth-brand-copy h1,
.auth-brand-copy p {
  overflow: hidden;
  margin: 0;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.auth-brand-copy h1 {
  font-family: Georgia, 'Times New Roman', serif;
  font-size: 20px;
  line-height: 1.05;
  font-weight: 400;
}

.auth-brand-copy p {
  max-width: min(40vw, 300px);
  margin-top: 5px;
  color: var(--auth-muted);
  font-size: 12px;
  line-height: 1.4;
}

.auth-brand-rule {
  width: min(30vw, 260px);
  margin-top: 20px;
  border-top: 1px solid var(--auth-line);
}

.auth-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(360px, 0.82fr);
  align-items: center;
  gap: clamp(36px, 7vw, 96px);
  flex: 1;
  padding: clamp(58px, 9vw, 116px) 0 clamp(48px, 8vw, 104px);
}

.auth-intro {
  position: relative;
  min-height: 300px;
  padding: 26px 0 28px;
  border-top: 1px solid var(--auth-line);
  border-bottom: 1px solid var(--auth-line);
}

.auth-intro-index {
  position: absolute;
  top: 24px;
  right: 0;
  color: var(--auth-sage-deep);
  font-family: Georgia, 'Times New Roman', serif;
  font-size: 14px;
  letter-spacing: 0.12em;
}

.auth-intro-line {
  width: 64px;
  height: 2px;
  margin-bottom: 28px;
  background: var(--auth-sage);
}

.auth-intro-label {
  max-width: 520px;
  margin: 0 0 20px;
  color: var(--auth-muted);
  font-size: 13px;
  line-height: 1.6;
  overflow-wrap: anywhere;
}

.auth-intro-copy {
  max-width: 580px;
  margin: 0;
  color: var(--auth-ink);
  font-family: Georgia, 'Times New Roman', serif;
  font-size: clamp(34px, 5.4vw, 68px);
  line-height: 1.05;
  font-weight: 400;
  letter-spacing: -0.035em;
  overflow-wrap: anywhere;
}

.auth-intro-signal {
  display: flex;
  align-items: flex-end;
  gap: 5px;
  height: 36px;
  margin-top: 40px;
}

.auth-intro-signal span {
  display: block;
  width: 4px;
  background: var(--auth-sage);
  opacity: 0.64;
}

.auth-intro-signal span:nth-child(1) {
  height: 12px;
}

.auth-intro-signal span:nth-child(2) {
  height: 24px;
}

.auth-intro-signal span:nth-child(3) {
  height: 36px;
}

.auth-intro-signal span:nth-child(4) {
  height: 18px;
}

.auth-panel {
  width: 100%;
  min-width: 0;
  border: 1px solid var(--auth-line);
  border-radius: 16px;
  background: var(--auth-surface);
  box-shadow: 0 24px 70px rgba(24, 25, 21, 0.08);
  backdrop-filter: blur(18px);
}

.auth-panel-content {
  min-width: 0;
  padding: clamp(22px, 4vw, 38px);
}

.auth-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px 24px;
  color: var(--auth-muted);
  font-size: 12px;
  line-height: 1.5;
}

.auth-footer p {
  margin: 0;
}

.auth-footer-links:empty {
  display: none;
}

:global(.dark) .auth-page {
  --auth-ink: #f2f4ef;
  --auth-muted: #9da59a;
  --auth-paper: #131613;
  --auth-surface: rgba(29, 34, 29, 0.82);
  --auth-line: rgba(226, 233, 223, 0.16);
  --auth-sage: #9aaa8d;
  --auth-sage-deep: #b8c5ad;
}

:global(.dark) .auth-brand-mark {
  background: rgba(29, 34, 29, 0.78);
}

:global(.dark) .auth-background-grid {
  background-image:
    linear-gradient(rgba(226, 233, 223, 0.035) 1px, transparent 1px),
    linear-gradient(90deg, rgba(226, 233, 223, 0.035) 1px, transparent 1px);
}

@media (max-width: 880px) {
  .auth-layout {
    grid-template-columns: 1fr;
    gap: 28px;
    padding-top: 58px;
  }

  .auth-intro {
    min-height: 0;
    padding: 22px 0 24px;
  }

  .auth-intro-copy {
    max-width: 720px;
    font-size: clamp(32px, 8vw, 54px);
  }

  .auth-intro-signal {
    margin-top: 26px;
  }

  .auth-panel {
    max-width: 640px;
    margin: 0 auto;
  }
}

@media (max-width: 560px) {
  .auth-shell {
    padding: 24px 18px 30px;
  }

  .auth-brand-rule {
    display: none;
  }

  .auth-brand-copy p {
    max-width: 56vw;
  }

  .auth-layout {
    gap: 22px;
    padding: 48px 0 42px;
  }

  .auth-intro {
    padding: 18px 0 20px;
  }

  .auth-intro-index {
    top: 18px;
  }

  .auth-intro-copy {
    font-size: clamp(30px, 10vw, 46px);
  }

  .auth-panel-content {
    padding: 22px 18px;
  }

  .auth-footer {
    align-items: flex-start;
    flex-direction: column;
  }
}

@media (prefers-reduced-motion: reduce) {
  .auth-page *,
  .auth-page *::before,
  .auth-page *::after {
    scroll-behavior: auto !important;
    transition-duration: 0.01ms !important;
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
  }
}
</style>

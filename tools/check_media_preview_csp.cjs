#!/usr/bin/env node
// Optional real-browser regression; no running service, credentials or network needed.
// Requires an existing Playwright install and Chromium (see the F10 migration note).
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')

const root = path.resolve(__dirname, '..')
const configSource = fs.readFileSync(path.join(root, 'backend/internal/config/config.go'), 'utf8')
const policyMatch = configSource.match(/^const DefaultCSPPolicy = (".*")$/m)
assert.ok(policyMatch, 'Read the real DefaultCSPPolicy, not a copied policy fixture')
const nonce = 'fixtureOnlyNonce0123456789'
const defaultPolicy = JSON.parse(policyMatch[1]).replaceAll('__CSP_NONCE__', `'nonce-${nonce}'`)
const video = fs.readFileSync(path.join(__dirname, 'fixtures/media-preview.mp4'))

// 0.5 seconds of silent PCM, generated locally rather than fetched from a service.
const audio = Buffer.alloc(44 + 8000)
audio.write('RIFF', 0)
audio.writeUInt32LE(audio.length - 8, 4)
audio.write('WAVEfmt ', 8)
audio.writeUInt32LE(16, 16)
audio.writeUInt16LE(1, 20)
audio.writeUInt16LE(1, 22)
audio.writeUInt32LE(8000, 24)
audio.writeUInt32LE(16000, 28)
audio.writeUInt16LE(2, 32)
audio.writeUInt16LE(16, 34)
audio.write('data', 36)
audio.writeUInt32LE(8000, 40)

const origin = 'http://media-preview.example.test'
const audioData = `data:audio/wav;base64,${audio.toString('base64')}`
const videoData = `data:video/mp4;base64,${video.toString('base64')}`

async function main() {
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.CHROMIUM_EXECUTABLE ? { executablePath: process.env.CHROMIUM_EXECUTABLE } : {})
  })
  const context = await browser.newContext({ serviceWorkers: 'block' })
  let sequence = 0

  async function fixturePage(policy = defaultPolicy) {
    const page = await context.newPage()
    const pageURL = `${origin}/case-${++sequence}`
    const networkRequests = []
    // Everything is fulfilled locally; unexpected requests fail rather than escape.
    await page.route('**/*', async (route) => {
      const url = route.request().url()
      networkRequests.push(url)
      if (url === pageURL) {
        await route.fulfill({
          status: 200,
          headers: { 'content-security-policy': policy, 'content-type': 'text/html' },
          body: `<!doctype html><html><body><script nonce="${nonce}">
            window.nonceWorked = true;
            window.violations = [];
            document.addEventListener('securitypolicyviolation', (event) => {
              window.violations.push({ directive: event.effectiveDirective, blockedURI: event.blockedURI });
            });
          </script></body></html>`
        })
      } else if (new URL(url).pathname === '/fixture.wav') {
        await route.fulfill({ status: 200, contentType: 'audio/wav', body: audio })
      } else if (new URL(url).pathname === '/fixture.mp4') {
        await route.fulfill({ status: 200, contentType: 'video/mp4', body: video })
      } else {
        await route.abort()
      }
    })
    await page.goto(pageURL)
    assert.equal(await page.evaluate(() => window.nonceWorked), true, 'nonce script must execute')
    return { page, networkRequests }
  }

  async function checkMedia(label, kind, url, allowed, policy = defaultPolicy) {
    const { page, networkRequests } = await fixturePage(policy)
    try {
      const result = await page.evaluate(async ({ kind, url }) => {
        const media = document.createElement(kind)
        media.controls = true
        media.muted = true
        media.preload = 'auto'
        document.body.append(media)
        const outcome = new Promise((resolve) => {
          const timer = setTimeout(() => resolve('timeout'), 5000)
          media.addEventListener('playing', () => { clearTimeout(timer); resolve('playing') }, { once: true })
          media.addEventListener('error', () => { clearTimeout(timer); resolve('error') }, { once: true })
        })
        media.src = url
        void media.play().catch(() => {})
        return outcome
      }, { kind, url })
      if (allowed) {
        assert.equal(result, 'playing', `${label}: real media playback`)
        assert.deepEqual(await page.evaluate(() => window.violations), [], `${label}: no CSP violation`)
      } else {
        assert.equal(result, 'error', `${label}: media must not play`)
        await page.waitForFunction(() => window.violations.some((item) => item.directive === 'media-src'))
        assert.ok(!networkRequests.includes(url), `${label}: blocked before a remote request`)
      }
      console.log(`PASS ${label}`)
    } finally {
      await page.close()
    }
  }

  try {
    await checkMedia('inline audio', 'audio', audioData, true)
    await checkMedia('inline video', 'video', videoData, true)
    await checkMedia('same-origin audio', 'audio', `${origin}/fixture.wav`, true)
    await checkMedia('same-origin video', 'video', `${origin}/fixture.mp4`, true)
    await checkMedia('official video origin', 'video', 'https://vidgen.x.ai/fixture.mp4', true)
    await checkMedia('unknown HTTPS origin', 'video', 'https://relay.example.test/fixture.mp4', false)
    await checkMedia('deceptive host suffix', 'video', 'https://vidgen.x.ai.attacker.invalid/fixture.mp4', false)
    await checkMedia('unapproved subdomain', 'video', 'https://sub.vidgen.x.ai/fixture.mp4', false)
    await checkMedia('unapproved port', 'video', 'https://vidgen.x.ai:444/fixture.mp4', false)
    await checkMedia('insecure cross-origin HTTP', 'video', 'http://vidgen.x.ai/fixture.mp4', false)

    // Keep the script harness's nonce but use custom media/default restrictions.
    const nonceScript = `script-src 'nonce-${nonce}'`
    await checkMedia('custom media none blocks inline', 'audio', audioData, false,
      `default-src 'self'; ${nonceScript}; media-src 'none'`)
    await checkMedia('custom default fallback blocks inline', 'audio', audioData, false,
      `default-src 'self'; ${nonceScript}`)
    await checkMedia('custom host does not gain official host', 'video', 'https://vidgen.x.ai/fixture.mp4', false,
      `default-src 'self'; ${nonceScript}; media-src https://preview.example.test`)
    await checkMedia('custom explicit host still works', 'video', 'https://preview.example.test/fixture.mp4', true,
      `default-src 'self'; ${nonceScript}; media-src https://preview.example.test`)

    const { page } = await fixturePage()
    try {
      await page.evaluate(() => {
        const inline = document.createElement('script')
        inline.textContent = 'window.unapprovedInline = true'
        document.body.append(inline)
        const data = document.createElement('script')
        data.src = 'data:text/javascript,window.unapprovedData=true'
        document.body.append(data)
        const frame = document.createElement('iframe')
        frame.src = 'https://vidgen.x.ai/frame'
        document.body.append(frame)
      })
      await page.waitForFunction(() =>
        window.violations.filter((item) => item.directive === 'script-src-elem').length >= 2 &&
        window.violations.some((item) => item.directive === 'frame-src'))
      assert.equal(await page.evaluate(() => Boolean(window.unapprovedInline || window.unapprovedData)), false)
      console.log('PASS nonce preserved; unapproved inline/data scripts and video-host frames blocked')
    } finally {
      await page.close()
    }
  } finally {
    await browser.close()
  }
}

main().catch((error) => {
  console.error(error)
  process.exitCode = 1
})

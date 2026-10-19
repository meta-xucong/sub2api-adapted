# Veyra local-only browser acceptance

This suite runs the real `SecurityHeaders` middleware, default nonce CSP,
`PortalMiddleware`, portal assets, and Veyra routes in Chromium. Authentication
and tickets are synthetic and local. It does not start the production service,
connect to PostgreSQL/Redis, or contact a model, billing, authentication, Alchemy,
or Video service.

## Invocation and CI

The PR-only `.github/workflows/veyra-browser.yml` checks out the exact PR head,
uses only `contents: read`, disables persisted checkout credentials, builds the
frontend from its frozen pnpm lockfile, and uploads receipts/screenshots for seven
days. It has no push, release, deployment, or manual-dispatch trigger. It uses
Ubuntu 24.04 x86_64, the Go version from `backend/go.mod`, Node 20, pnpm 9, and
Python 3.12. GitHub Actions are pinned to immutable commit SHAs.

On an authorized machine with those tools installed, prepare dependencies using
the workflow's setup commands. Python packages are wheel/hash-locked in
`requirements-ci.txt`; Playwright 1.62.0 installs its corresponding Chromium
revision. This is a networked dependency-setup phase. Then, from a clean checkout
with freshly built `backend/internal/web/dist`, run:

```sh
python tools/veyra-browser/run.py \
  --expected-sha "$(git rev-parse HEAD)" \
  --output "${RUNNER_TEMP:-/tmp}/veyra-browser-evidence-$(date +%s)"
```

The evidence directory must be new and outside the checkout. The runner requires
at least 900 MiB free, uses `GOMAXPROCS=2`, `-p=1`, `GOMEMLIMIT=3GiB`, and disables
Go proxy/sumdb/toolchain downloads for the acceptance phase. It injects the Go
fixture using an overlay, leaving tracked source and generated frontend assets
unchanged. Only its own process groups are terminated during cleanup.

## Browser network boundary

- Four httptest servers listen on `127.0.0.1` with dynamically allocated ports.
- Every browser context registers routing before opening a page. Only the exact
  four fixture origins may continue to the network. All other HTTP(S) requests,
  including real-looking Video/Alchemy URLs, are fulfilled locally.
- Every WebSocket is routed and closed without calling `connect_to_server`.
  An attempted WebSocket fails the case because the portal requires none.
- Service workers and downloads are disabled. Chromium background-network
  features and remote hostname resolution are disabled. Each case uses a fresh
  browser context. The fixture/browser environment uses a new HOME and an
  explicit allowlist, excluding GitHub tokens, proxies, and application secrets.
- Synthetic authentication is injected only on the loopback fixture origins.
  Callback payloads contain synthetic in-memory tickets, never real credentials.
- The runner independently checks every recorded routing decision. Receipts are
  application-level interception evidence, not an operating-system packet capture
  or a claim that dependency installation was offline.

## The 22 positive cases

1. Desktop root renders under the default nonce CSP.
2. Desktop Video launch POSTs only to the exact trusted callback.
3. CSP blocks an attacker-origin form.
4. CSP blocks a sibling-host form.
5. CSP blocks a wrong-path form.
6. CSP blocks a callback path-suffix form.
7. CSP blocks an HTTP-downgrade form.
8. CSP blocks a wrong-port form.
9. Nonportal pages retain self-only `form-action`.
10. Mobile root navigation retains query and fragment.
11. Mobile `desktop=1` preserves the desktop portal.
12. Desktop brand navigation returns to `/`.
13. Desktop return auto-launches Alchemy `/`.
14. Desktop return auto-launches Alchemy `/h5`.
15. Desktop return auto-launches Video by POST.
16. Mobile return auto-launches Alchemy `/`.
17. Mobile return auto-launches Alchemy `/h5`.
18. Mobile return auto-launches Video by POST.
19. Mobile console return reaches the downstream SPA handler.
20. Both flags disabled preserve downstream SPA handling.
21. Bridge disabled preserves downstream SPA handling.
22. Portal disabled preserves downstream SPA handling.

Disabled/console cases use a downstream HTML sentinel. They prove middleware
pass-through, not rendering of the production Vue SPA. The freshly built embedded
frontend assets are fingerprinted, but this suite primarily executes the separate
embedded Veyra portal assets. Monetary/database behavior and real third-party
SSO are outside this suite.

## Source binding and outputs

`receipt.json` identifies `veyra-portable-browser-ci-v1`, exact checked-out
commit/tree, complete tracked-source fingerprint, tool versions, commands,
resource limits, exit codes, network decisions, case summary, cleanup, and SHA256
of each evidence artifact. `source-before.json` and `source-after.json` enumerate
all tracked files, compare their blob hashes/modes to HEAD, and hash portal assets
and freshly generated embedded frontend assets. A clean checkout and identical
pre/post source/assets are required for success.

`results.json` retains each case's outcome, raw assertion failure, browser
requests, routing decisions, intercepted synthetic payloads, CSP events, console
messages, page errors, final URL, and screenshot. `server.log` and `browser.log`
remain available if startup fails before case results exist. Success requires 22
unique passing cases, zero failures, verified interception, zero process exit
errors, unchanged source/assets, closed owned process groups, and all fixture ports
closed. Screenshots and
receipts are uploaded even if the job fails after the runner starts.

Do not label a workflow or infrastructure error as a browser acceptance pass.
Do not substitute an older commit's receipt for a later commit. Adding this suite
creates a new source commit; its CI receipt must name that exact commit.

## Provenance and limits

This portable runner is separate from the earlier cloud runner. The earlier cloud
attempt on application commit `d57bbd8130be1f1b2a6f8d9c3c9940be4e9edba4`
completed zero of 22 browser cases because Chromium's process-singleton Unix
socket failed with OS `EPERM`. It supplies no browser acceptance evidence. Its
immutable receipts are retained separately and must not be overwritten or
presented as a CI run.

The Go fixture is byte-identical to that prepared cloud fixture. The 22 application
case bodies are retained; portability changes select Playwright-managed Chromium
and add WebSocket denial plus receipt validation. An optional negative-CSP case
remains in the browser script for a separately reviewed controlled overlay, but
the workflow and runner execute only the 22 positive cases. No negative-control
result is claimed.

This initial portable proposal has received static preparation checks only.
Independent review and its first actual CI run are required before calling the
browser gate complete.

Official dependency/API references:

- [Playwright 1.62.0](https://pypi.org/project/playwright/1.62.0/)
- [pyee 13.0.1](https://pypi.org/project/pyee/13.0.1/)
- [greenlet 3.5.5](https://pypi.org/project/greenlet/3.5.5/)
- [typing_extensions 4.16.0](https://pypi.org/project/typing-extensions/4.16.0/)
- [Playwright Python CI](https://playwright.dev/python/docs/ci)
- [BrowserContext WebSocket routing](https://playwright.dev/python/docs/api/class-browsercontext#browser-context-route-web-socket)
- [WebSocketRoute behavior](https://playwright.dev/python/docs/api/class-websocketroute)

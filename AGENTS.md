# Sub2API Adaptation Agent Notes

This repository is the private adaptation branch for `Wei-Shaw/sub2api`.

- Private remote: `origin` -> `meta-xucong/sub2api-adapted`
- Official upstream: `upstream` -> `Wei-Shaw/sub2api`
- Main custom branch: `custom/main`
- Production VPS container: `sub2api`

## Update Policy

When updating from the official upstream, keep the private adaptation workflow intact:

1. Fetch upstream changes first.
2. Merge or rebase onto `custom/main` only after checking local status.
3. Preserve local custom patches when they still apply, especially OpenAI OAuth failover, Kimi stability, Gemini routing, image generation, server health, pricing, and operations dashboard changes.
4. Commit every retained custom patch to `origin/custom/main` before deploying.
5. Do not treat production-only account data or runtime state as source code.

## Patch Lifecycle

Treat local changes as a private patch layer, not as permanent forks of upstream
code.

During every upstream update:

1. List custom commits with `git log --oneline upstream/main..custom/main`.
2. Compare upstream changes for equivalent fixes before preserving a custom
   patch.
3. Drop or shrink any custom patch that upstream has fully absorbed.
4. Keep only patches that still solve a production-specific problem.
5. Update `docs/CUSTOM_PATCHES.md` whenever a custom patch is added, removed,
   split, narrowed, or made obsolete by upstream.
6. Prefer deleting obsolete compatibility code over carrying duplicate logic.

The goal is to keep `custom/main` close to official upstream while preserving
the production behavior that upstream does not yet cover.

## Deployment Strategy

Do not rebuild and reinstall everything from scratch unless the cache is broken or the source tree is unavailable.

Preferred VPS workflow:

1. Keep a persistent source checkout on the VPS, for example `/opt/sub2api/src`.
2. Update that checkout with `git fetch` plus checkout/reset to the intended private commit.
3. Build with Docker BuildKit so dependency layers are reused.
4. Use a unique image tag for every deployment:
   - `sub2api-adapted:custom-main-<commit>`
5. Start the new container with the same environment, network, ports, ulimits, and `/app/data` mount as the current container.
6. Keep the previous container renamed as the latest rollback point.
7. Tag the new image as `sub2api-adapted:custom-main` only after the new container is healthy.

If the persistent source checkout is not available, uploading a source tarball is allowed as a fallback, but avoid making that the default path.

## Cache Policy

Build cache is valuable and should be preserved by default.

- Do not run `docker builder prune` after every deployment.
- Do not remove Go, Node, pnpm, or Docker build cache just because a deployment succeeded.
- Clean build cache only when disk usage is high or the cache is known to be corrupted.
- Suggested cleanup threshold: prune old build cache when `/` is above 80% usage; urgently prune when above 90%.
- Prefer targeted cleanup:
  - Remove temporary source archives and build directories.
  - Remove old rollback containers except the latest one.
  - Remove dangling images.
  - Keep active images and useful BuildKit cache.

If Dockerfile changes are being made, prefer cache-friendly build steps, including BuildKit cache mounts for Go module/build cache and pnpm store cache where practical.

## Data Safety

Never destroy or recreate production data during a code update.

Protected VPS paths and services:

- `/opt/sub2api/deploy/data`
- PostgreSQL container and volume/data directory
- Redis container and volume/data directory
- Account credentials, API keys, OAuth tokens, model mappings, pricing data, and usage records

Before replacing the app container, inspect the current container and preserve:

- Environment variables
- Port bindings
- Docker network
- Restart policy
- Ulimits
- `/app/data` mount

Do not run destructive commands such as `docker compose down -v`, `docker volume rm`, database resets, or data directory deletion unless the user explicitly asks for that operation.

## Verification

Minimum verification before deploy:

1. `git status --short` is understood and unrelated changes are not reverted.
2. `git diff --check` passes.
3. Formatting is checked for touched Go files.
4. Relevant targeted tests are attempted.
5. If tests are too slow or the local toolchain is unavailable, a production Docker build must complete successfully before deployment.

Minimum verification after deploy:

1. New `sub2api` container reaches `running/healthy`.
2. `curl http://127.0.0.1:8080/health` returns OK.
3. Run one behavior check related to the patch.
4. Check recent logs for unexpected 5xx errors.
5. Confirm disk usage is acceptable.
6. Confirm `origin/custom/main` contains the deployed commit.

For image upload changes, include a synthetic invalid upload check. A fake file declared as `image/png` should return a clear `400 invalid_request_error`, not a misleading `502`.

## Cleanup After Deploy

After a successful deployment:

- Delete temporary tarballs and temporary build directories.
- Keep only the latest `sub2api-prev-*` rollback container.
- Keep the deployed image and its stable tag.
- Avoid full cache pruning unless disk thresholds require it.
- Report final disk usage and the rollback container name.

## Failure Handling

If the new container does not become healthy:

1. Stop and remove the failed new container.
2. Rename the latest rollback container back to `sub2api`.
3. Restore restart policy.
4. Start the rollback container.
5. Report the failure with the relevant logs.

Do not continue cleanup until rollback safety is confirmed.

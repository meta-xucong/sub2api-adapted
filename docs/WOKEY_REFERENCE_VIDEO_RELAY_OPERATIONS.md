# Wokey Reference Video Relay Operations

## Pre-deploy

1. Run the targeted Wokey media tests and the full `internal/service` test
   package.
2. Build a Linux amd64 binary with the exact Git commit in version metadata.
3. Record the artifact SHA256.
4. Upload the artifact to `/tmp` on each VPS and verify the remote SHA256 before
   replacing anything.

## Deployment

For each VPS independently:

1. Confirm `sub2api` is running and healthy.
2. Copy `/app/sub2api` to a timestamped backup directory.
3. Copy the candidate to a temporary path inside the container, chmod 755, and
   atomically move it to `/app/sub2api`.
4. Restart the container and wait for both Docker health and `/health`.
5. Verify version, commit, and SHA256.

The second VPS is not changed until the first has passed verification.

## Rollback

If restart, health, version, or SHA verification fails, restore the timestamped
backup binary, restart, and require healthy status. Keep the failed candidate
and backup metadata for diagnosis.

## Smoke Checks

- JSON I2V using `image.url`.
- JSON R2V using two relay URLs and `mode=multimodal_reference`.
- Confirm upstream request is multipart with ordered `image[]` parts.
- Confirm no new `reference_images[0].url host is not allowed` errors.
- Confirm ordinary text-to-video and video status/content remain healthy.

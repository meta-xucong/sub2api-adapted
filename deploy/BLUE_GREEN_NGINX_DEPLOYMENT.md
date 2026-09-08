# Blue-Green Nginx Deployment

Use this procedure when a Sub2API image must be upgraded without cutting off
long-running Responses SSE, Compact, image, or video requests. It is portable:
replace the example names and ports for the target host, but do not put host
credentials, API keys, account data, or production health records in this
repository.

## Layout

- The current container continues on its original loopback port, for example
  `127.0.0.1:8080`.
- Start the candidate image with the same read-only configuration and data
  dependencies on a second loopback port, for example `127.0.0.1:8081`.
- Keep Nginx in front of both containers. Switch only new requests to the
  candidate after its protocol smoke tests pass.
- Retain the old container and image through a defined verification window.
  Image rollback is not a database rollback; preserve a database backup before
  applying a release with migrations.

Do not expose either application port directly to the Internet.

## Candidate Validation

Before changing Nginx, verify the candidate through its loopback port with an
administrator-owned test key:

1. `GET /health` returns healthy.
2. A streamed `/v1/responses` request ends with `response.completed`.
3. `/v1/responses/compact` returns a real compaction item, not only HTTP 200.
4. Test enabled optional protocols such as image generation or Fast tier using
   their own dedicated administrator test key.

Do not use a customer API key for an SSH, curl, deployment, or calibration
smoke test. See `docs/OPERATOR_TEST_KEY_RUNBOOK.md`.

## Nginx Switch

Back up the active Nginx site file **outside** `sites-enabled` before editing
it. A backup file left inside an included directory creates a duplicate vhost;
Nginx then ignores one of the server blocks and can silently route traffic to
the wrong release.

Point the normal location to the candidate loopback port. Keep proxy buffering
disabled and preserve the existing long read and send timeouts for SSE:

```nginx
location / {
    proxy_pass http://127.0.0.1:8081;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_read_timeout 86400s;
    proxy_send_timeout 86400s;
}
```

During a frontend upgrade, browser tabs can retain hashes for JavaScript files
from the prior release. Keep the old container running and route a missing
candidate asset to it temporarily:

```nginx
location /assets/ {
    proxy_pass http://127.0.0.1:8081;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_intercept_errors on;
    error_page 404 = @legacy_assets;
}

location @legacy_assets {
    internal;
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_buffering off;
}
```

Run `nginx -t` before `systemctl reload nginx`. A reload is graceful: existing
connections remain served by old Nginx workers while new requests use the
candidate upstream.

## Post-Switch Verification

Verify through the public HTTPS vhost, not only the loopback port:

1. Health endpoint and authenticated `/v1/models`.
2. Responses SSE terminal event.
3. Compact terminal compaction item.
4. Any enabled Fast, image, or video path.
5. The expected login page and one active vhost for the domain.

Update the Compose image reference and candidate port mapping only after these
checks pass. Rename the old container to a rollback name; do not stop it until
the verification window ends.

## Rollback

If the candidate fails a protocol or public-vhost check:

1. Restore the backed-up Nginx file and validate it with `nginx -t`.
2. Reload Nginx so new requests return to the old loopback port.
3. Keep the failed candidate stopped for inspection; do not delete its logs.
4. Restore the previous Compose reference only after Nginx has returned to the
   old container.

If the release includes database migrations, use a tested database restore or
a compensating migration. Switching only the image is not sufficient to undo
schema changes.

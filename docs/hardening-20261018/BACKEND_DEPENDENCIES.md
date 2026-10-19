# Backend dependency and toolchain changes

Official comparison `Wei-Shaw/sub2api@b0a02a3304a0a78cdd003ddda1dd23ba02c03084` still pins x/image0.41.0 and klauspost/compress1.18.2. These are advisory-backed bounded upgrades, not claimed upstream Sub2API ports.

- `golang.org/x/image`:0.41.0 →0.45.0. Published fixes cover [GO-2026-6222](https://pkg.go.dev/vuln/GO-2026-6222), [GO-2026-5061](https://pkg.go.dev/vuln/GO-2026-5061), and [GO-2026-4961](https://pkg.go.dev/vuln/GO-2026-4961). The last is architecture-specific; do not imply it was reproduced on this64-bit host. Existing WebP registrations remain; no decoder replacement.
- `github.com/klauspost/compress`:1.18.2 →1.18.7, the patch floor for [GO-2026-5841](https://pkg.go.dev/vuln/GO-2026-5841). The vulnerable `s2.NewDict` package was not imported/reachable in the symbol scan, so this is module hygiene, not proof of an exploitable current application path.
- CI golangci-lint:v2.13 →v2.15.0 from the [official release](https://github.com/golangci/golangci-lint/releases/tag/v2.15.0). Verified release checksum and binary version (built with Go1.27.0). This addresses the old tool's inability to consume Go1.27.2 exports; no lint suppressions or disabled checks are added.
- Application Go toolchain stays1.27.2. Cloud tests used the official archive with its published checksum verified. Compiler/test cache and lint cache reside in the workspace; no machine-wide Go configuration was changed.

## Residual module advisory

[GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) flags the unmaintained `golang.org/x/crypto/openpgp` package. No fixed version is listed. The application uses other x/crypto packages; the vulnerable OpenPGP package is not imported in the inspected build. Do not remove the entire cryptography module or claim all of x/crypto is vulnerable based on this finding. A future OpenPGP import needs a maintained replacement and separate review.

The first symbol-level scan after x/image upgrade returned exit0 with zero reachable findings; two module-only findings were then inventoried, leading to the compress patch. Final source/build scans and full tests must be repeated after the final freeze; PROGRESS.md and the acceptance receipt identify their status.

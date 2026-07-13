# Smart Router Module

`backend/internal/smartrouter` is the provider-neutral Smart Router module.

The `core` package does not depend on Sub2API service types. It accepts generic
lane snapshots and returns an ordered lane plan. Sub2API integration lives in
the service adapter layer so the same core can later be reused by a sidecar
gateway.

The optional adaptive policy is documented in
`docs/SMART_ROUTER_ADAPTIVE_POLICY.md`. Its core plugins are provider-neutral;
the default feature flag is off until an operator explicitly enables it.

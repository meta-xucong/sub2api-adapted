# Sub2API Smart Router Design

This note records the product direction for a reusable Sub2API routing patch.
It is intentionally provider-neutral: a deployment may use aicodexvip, aiai,
404token, or any other upstream, but the router should only care about lanes,
capabilities, cost, load, health, and recovery policy.

Implementation planning lives in `docs/SMART_ROUTER_DEVELOPMENT.md`.

## Goal

Smart Router should reduce repeated high-frequency hits to the same weak lane
and spread requests across available lanes without losing the ability to prefer
cheaper upstreams.

The target behavior is:

- cheap lanes receive more traffic, but not 100%;
- flapping lanes are cooled temporarily, not permanently disabled;
- failed requests do not fan out through every possible lane;
- recovery is automatic and gradual;
- adding a fifth or tenth lane is a policy/config change, not custom code.

## Current Image Edit Transient Cooldown Patch

The local patch adding `gateway.image_edit_transient_cooldown_seconds` is a good
first primitive for Smart Router.

It solves a narrow but important problem:

- `/v1/images/edits` can fail transiently with `403`, `408`, `500`, `502`,
  `503`, or `504`;
- immediately selecting the same failed account for the next image edit request
  amplifies instability;
- a short per-account cooldown lets the next request move to another healthy
  account without permanently disabling the failed account.

This patch is useful because it acts at the correct time: after an upstream
failover error and before the next scheduling decision. It is also compatible
with the earlier image-lane recovery work because both avoid treating provider
flaps as permanent account failures.

However, by itself it is not the full Smart Router:

- it only covers OpenAI image edits;
- it uses one global duration instead of per-lane policy;
- it does not understand source groups;
- it does not perform weighted distribution;
- it does not gradually ramp a recovered lane back into traffic;
- it does not distinguish cheap opportunistic lanes from expensive stable lanes.

So it should be kept, but eventually absorbed into a generic capability-level
cooldown policy.

## Router Concepts

### Lane

A lane is one schedulable upstream path. It may be a Sub2API account, an
upstream Sub2API endpoint, or a provider account behind another Sub2API.

Suggested fields:

- `lane_id`
- `account_id`
- `name`
- `source_group`
- `capabilities`
- `model_patterns`
- `cost_multiplier`
- `base_weight`
- `max_concurrency`
- `group_max_concurrency`
- `max_attempts_per_request`
- `cooldown_policy`
- `probe_policy`

### Capability

Health must be tracked by capability, not only by account.

Initial capabilities:

- `chat`
- `responses`
- `image_generation`
- `image_edit`
- `embedding`

The same account may be healthy for chat but unhealthy for image edit.

### Source Group

Lanes from the same supplier or upstream pool should share a `source_group`.
This prevents one request from repeatedly trying `plus`, `pro`, and `fallback`
when they are all backed by the same exhausted source.

Within a single user request, after one lane in a source group fails with a
source-level error, the router should avoid trying more lanes from the same
source group unless policy explicitly allows it.

## Selection Algorithm

Eligible lanes are filtered by:

- requested model;
- requested capability;
- group membership;
- account active/schedulable status;
- temporary cooldown state;
- lane and source-group concurrency.

Each remaining lane receives an effective score:

```text
score =
  base_weight
  * health_score
  * cost_factor
  * latency_factor
  * availability_factor
  / load_factor
```

The scheduler then performs weighted selection from the highest viable lanes.

This is deliberately different from hard priority fallback. Hard priority sends
all traffic to the cheapest lane until it breaks. Smart Router should prefer the
cheap lane while still keeping fallback lanes warm.

## Failure Classification

Failures should update lane state, not blindly disable accounts.

Recommended default classes:

| Class | Examples | Default action |
| --- | --- | --- |
| client_error | `400`, unsupported model | no cooldown, return or try mapped lane |
| auth_forbidden | `401`, persistent auth `403` | stronger cooldown, operator alert |
| transient_forbidden | image `403`, upstream forbidden flap | capability cooldown |
| rate_limited | `429` | cooldown plus concurrency reduction |
| upstream_5xx | `500`, `502`, `503` | short cooldown |
| timeout | `504`, request timeout | health penalty, optional cooldown |
| context_cancelled | client cancelled | usually no provider penalty |

The current `image_edit_transient_cooldown_seconds` patch maps image edit
`403/408/500/502/503/504` to a short account cooldown. In Smart Router terms,
that becomes:

```text
capability = image_edit
failure_class = transient_or_upstream
action = temp_unschedule_lane
duration = policy value
```

## Cooldown And Recovery

Cooldown should be temporary and recoverable:

```text
failure
-> capability-level cooldown
-> skip lane for real traffic
-> probe after cooldown
-> restore small weight
-> ramp up after successful real traffic
```

Recovery should not instantly restore full traffic. A good default is:

- first recovery: 5% of target weight;
- after several successes: 25%;
- after a quiet window: 100% target weight.

## Request Attempt Budget

To avoid high-frequency multi-line fanout:

- image requests should usually try at most 2 lanes;
- chat/responses requests should usually try at most 2 or 3 lanes;
- after a source-group-level failure, skip other lanes in that group;
- do not retry a cooled lane inside the same request.

This is important when a user configures five or more backup lanes. More lanes
should improve availability, not multiply upstream pressure.

## Deployment Modes

### Core Patch Mode

Patch Sub2API directly. This gives the best visibility into accounts, groups,
models, logs, and temporary scheduling state.

Use this for deployments that can build a custom Sub2API image.

### Sidecar Gateway Mode

Run an independent OpenAI-compatible gateway in front of one or more Sub2API
instances. This is easier to sell to users who do not want to patch code, but it
has less account-level visibility.

The routing engine should be shared between both modes where possible.

## Compatibility With Two-Layer Sub2API

If installed on both downstream and upstream Sub2API instances, the behavior is
compatible:

- downstream Smart Router protects user experience and avoids repeatedly hitting
  a known-bad upstream endpoint;
- upstream Smart Router protects provider accounts and performs supplier-level
  cooling, probing, and recovery.

Each layer only needs to manage its own lanes.

## MVP Scope

The first reusable commercial patch should include:

- lane max concurrency;
- source-group max concurrency;
- capability-level temporary cooldown;
- failure classification;
- weighted selection using cost, health, and load;
- automatic probe/ramp recovery;
- request attempt budgets;
- docs and example policies.

The existing image edit transient cooldown patch should be retained as an early
implementation of capability-level temporary cooldown, then generalized instead
of duplicated.

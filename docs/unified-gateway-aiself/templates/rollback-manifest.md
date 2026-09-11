# Rollback manifest template

## Scope

- rollout id:
- environment:
- source/image revision:
- feature gate:
- new hostname/path:
- owner:

## New objects allowed to disable

| object type | exact identifier | disable order | verification |
| --- | --- | --- | --- |
| feature gate | NEW_GATE_ID | 1 | old endpoint still healthy |
| API key | NEW_KEY_REF | 2 | old keys unchanged |
| composite group | NEW_GROUP_ID | 3 | old groups unchanged |
| route | NEW_ROUTE_ID | 4 | no new route in old catalog |
| pricing profile | NEW_PROFILE_ID | 5 | historical settlements readable |
| proxy entry | NEW_PROXY_REF | 6 | old `/v1` unchanged |

## Protected objects

- existing API keys:
- existing groups:
- existing accounts and account_groups:
- existing routes/prices:
- existing usage/billing entries:

## Post-rollback checks

- [ ] old `/health` and `/v1/models` pass
- [ ] old text smoke pass
- [ ] old billing diff is zero or explained
- [ ] no new request reaches old group
- [ ] pending media tasks have an explicit settlement disposition
- [ ] logs/metrics show new gate disabled

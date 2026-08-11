set -euo pipefail

psql_cmd() {
  docker exec sub2api-postgres psql -U sub2api -d sub2api "$@"
}

echo '== YeToken accounts =='
psql_cmd -P pager=off -c "
select
  id,
  name,
  status,
  schedulable,
  priority,
  concurrency,
  to_char(last_used_at at time zone 'Asia/Shanghai','YYYY-MM-DD HH24:MI:SS') as last_used_sh,
  to_char(temp_unschedulable_until at time zone 'Asia/Shanghai','YYYY-MM-DD HH24:MI:SS') as temp_until_sh,
  left(coalesce(temp_unschedulable_reason,''),160) as temp_reason,
  left(coalesce(error_message,''),160) as error_message
from accounts
where deleted_at is null and name ilike '%YeToken%'
order by id;
"

echo '== Image usage by YeToken accounts since 2026-08-06 00:00 SH =='
psql_cmd -P pager=off -c "
select
  u.id,
  to_char(u.created_at at time zone 'Asia/Shanghai','YYYY-MM-DD HH24:MI:SS') as created_sh,
  u.request_id,
  u.account_id,
  a.name as account_name,
  u.group_id,
  g.name as group_name,
  u.model,
  u.requested_model,
  u.upstream_model,
  u.duration_ms,
  u.image_count,
  u.image_input_size,
  u.image_output_size,
  u.image_size_source
from usage_logs u
join accounts a on a.id=u.account_id
left join groups g on g.id=u.group_id
where u.created_at >= timestamptz '2026-08-05 16:00:00+00'
  and (a.name ilike '%YeToken%' or g.name ilike '%image2%' or u.model ilike '%image%')
order by u.created_at desc
limit 80;
"

echo '== Long image usage rows since 2026-08-05 00:00 SH =='
psql_cmd -P pager=off -c "
select
  to_char(u.created_at at time zone 'Asia/Shanghai','YYYY-MM-DD HH24:MI:SS') as created_sh,
  u.request_id,
  u.account_id,
  a.name as account_name,
  coalesce(g.name,'') as group_name,
  u.model,
  u.duration_ms,
  u.image_count,
  u.image_input_size,
  u.image_output_size
from usage_logs u
join accounts a on a.id=u.account_id
left join groups g on g.id=u.group_id
where u.created_at >= timestamptz '2026-08-04 16:00:00+00'
  and (u.model ilike '%image%' or u.image_count > 0)
  and coalesce(u.duration_ms,0) >= 120000
order by u.duration_ms desc, u.created_at desc
limit 80;
"

echo '== Available error tables =='
psql_cmd -P pager=off -c "
select tablename
from pg_tables
where schemaname='public'
  and (tablename ilike '%error%' or tablename ilike '%ops%' or tablename ilike '%monitor%')
order by tablename;
"
# end
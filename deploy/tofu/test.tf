# Pre-prod (multica-test) cloud resources.
#
# Kept in a separate file from alb_acl.tf / rds.tf so the entire test
# environment can be torn down in one diff (`git rm deploy/tofu/test.tf
# && tofu apply`) without disturbing prod plans.
#
# Resources:
#   * alicloud_alb_acl.multica_test            — IP whitelist for the test ALB
#   * alicloud_alb_acl_entry_attachment.whitelist_test
#       — same CIDR set as prod (reuses local.whitelist_cidrs from alb_acl.tf)
#   * alicloud_db_instance.multica_test        — separate RDS PG 18 instance,
#       same engine / class / storage / category as prod but Postpaid so it
#       can be torn down freely
#   * alicloud_db_account.app_test             — Super-privileged `multica` user
#   * alicloud_db_database.multica_test        — `multica` database (same name
#       as prod so DATABASE_URL stays symmetric across environments)
#
# Outputs `test_acl_id` and `test_database_url` are consumed by
# deploy/k8s/overlays/test/ — see that overlay's README for how to wire them.

# ---------------------------------------------------------------------------
# ACL — identical CIDR set to prod, separate policy
# ---------------------------------------------------------------------------
#
# Separate ACL means test admin tweaks never accidentally widen prod's
# surface, while referencing the same `local.whitelist_cidrs` keeps the two
# policies in lockstep until the day they need to diverge.

resource "alicloud_alb_acl" "multica_test" {
  acl_name          = "multica-test-whitelist"
  resource_group_id = null

  # No prevent_destroy here — test should be cleanly tearable.
}

resource "alicloud_alb_acl_entry_attachment" "whitelist_test" {
  for_each = toset(local.whitelist_cidrs)

  acl_id      = alicloud_alb_acl.multica_test.id
  entry       = each.value
  description = "lilith-whitelist"
}

# ---------------------------------------------------------------------------
# RDS — independent PG 18 instance, same spec as prod
# ---------------------------------------------------------------------------
#
# Same engine / class / storage / category as prod so test exercises the
# same code paths a prod rollout will. Two deliberate deviations:
#   * Postpaid (按量付费) — test envs are torn up and down often; Prepaid
#     would mean a month-long billing commit per teardown.
#   * Placed in the *other* vSwitch (cn-shanghai-l) — random spread across
#     AZs is fine for test and avoids both instances being subject to the
#     same single-AZ maintenance window.

resource "alicloud_db_instance" "multica_test" {
  engine         = "PostgreSQL"
  engine_version = var.rds_engine_version
  # Test stays on the cheap end. Hardcoded (not var.rds_instance_class /
  # var.rds_storage_gb) because those vars track prod's current scale; using
  # them here would propose an unwanted test upgrade whenever prod scales.
  instance_type    = "pg.n2.2c.2m" # 2c / 4g
  instance_storage = 100           # GB
  category         = "HighAvailability"

  # Prepaid + auto-renew to match prod's billing posture. Test can still
  # be torn down — the 1-month period limits the financial commit to ~1 cycle
  # of waste if you destroy mid-period.
  instance_charge_type = "Prepaid"
  period               = var.rds_period_months
  auto_renew           = var.rds_auto_renew
  auto_renew_period    = var.rds_period_months

  instance_name              = "multica-test"
  db_instance_storage_type   = "cloud_essd"
  auto_upgrade_minor_version = "Auto"

  # Pinned to cn-shanghai-l — the other AZ from prod (which sits in
  # cn-shanghai-n). vswitch_id is ForceNew; explicit beats dynamic.
  vswitch_id = "vsw-uf63wwiqokc71da0ghfv0"

  security_ips = ["10.159.0.0/16"]

  tags = merge(local.common_tags, { "env" = "test" })

  lifecycle {
    ignore_changes = [parameters]
  }
}

resource "alicloud_db_account" "app_test" {
  db_instance_id   = alicloud_db_instance.multica_test.id
  account_name     = var.rds_account_name
  account_password = var.rds_account_password
  account_type     = "Super"
}

resource "alicloud_db_database" "multica_test" {
  instance_id    = alicloud_db_instance.multica_test.id
  data_base_name = "multica"
  character_set  = "UTF8"

  depends_on = [alicloud_db_account.app_test]
}

# ---------------------------------------------------------------------------
# Tair (Redis-compatible) sharded cluster — for the WS realtime relay
# ---------------------------------------------------------------------------
#
# Multica's server (server/cmd/server/main.go) switches from in-memory WS hub
# to a Redis Streams relay when REDIS_URL is set, which is exactly what we
# want to exercise on test: multi-pod fanout via XADD / XREAD. Tair sharded
# cluster picked over single-node so test reproduces prod-shaped behaviour
# once prod also adopts Redis (multi-replica plan, see overlays/test/README).
#
# Modeled after the existing reference instance r-uf6djfydidx2c4o5y2.
# I lack kvstore:Describe* perms on that instance — fill in the exact SKU
# fields below to match before `tofu apply`, otherwise the defaults provision
# a small Tair RDB cluster (2 shards, ~1GB each, ~80-120 RMB/month Postpaid).

resource "alicloud_kvstore_instance" "tair_test" {
  db_instance_name = "multica-test-tair"

  # Tair Memory Edition (a.k.a. Performance Enhanced / amber), 4GB cluster
  # (2GB per shard × 2 shards). The prod reference r-uf6djfydidx2c4o5y2
  # uses tair.rdb.cluster.sharding.common which is no longer sellable in
  # cn-shanghai — DescribeAvailableResource returns zero matches in any AZ.
  # This amber SKU is the closest live equivalent: same cluster topology,
  # same total capacity, same multithread perf bucket, just with the older
  # alicloud_kvstore_instance resource family.
  instance_class = "redis.amber.logic.sharding.2g.2db.0rodb.6proxy.multithread"
  instance_type  = "Redis"
  # amber (Tair Memory Enhanced) cluster SKUs only support 5.0 in cn-shanghai-l;
  # DescribeAvailableResource lists no 6.0/7.0 for any amber.logic.sharding class.
  # Multica's realtime relay uses Redis Streams (XADD/XREAD), introduced in 5.0,
  # so this is functionally sufficient.
  engine_version = "5.0"
  shard_count    = 2

  # Subscription (包年包月) + auto-renew, matching prod's reference billing
  # posture. var.rds_period_months (default 1) keeps the financial commit
  # short; bump via tfvars if you want longer commits.
  payment_type      = "PrePaid"
  period            = var.rds_period_months
  auto_renew        = var.rds_auto_renew ? "true" : "false"
  auto_renew_period = var.rds_period_months

  # Same AZ as the test RDS (cn-shanghai-l), but in multica's VPC.
  # NOTE: the reference instance r-uf6djfydidx2c4o5y2 lives in
  # vpc-uf6nn0knmpggl9py0bco9 (tsh-plat2ops); we host test Tair in multica's
  # own VPC so the test pods can reach it without cross-VPC peering.
  zone_id    = "cn-shanghai-l"
  vswitch_id = "vsw-uf63wwiqokc71da0ghfv0"

  # Open mode: VPC-internal endpoint authenticated by password only, no
  # per-IP whitelist (matches the reference). Endpoint is private to the
  # VPC; no public access is exposed by default.
  vpc_auth_mode = "Open"

  # Tair's own IP whitelist (separate from ALB ACL). Default is 127.0.0.1
  # which blocks every K8s pod. Open to the K8s pod CIDR so the realtime
  # relay can XADD / XREAD; the password still gates actual reads.
  security_ips = ["10.159.0.0/16"]

  # Password for the default account. Override via TF_VAR_redis_password.
  password = var.redis_password

  tags = merge(local.common_tags, { "env" = "test" })

  lifecycle {
    ignore_changes = [
      maintain_start_time,
      maintain_end_time,
      parameters,
    ]
  }
}

# ---------------------------------------------------------------------------
# Outputs — drive the K8s overlay
# ---------------------------------------------------------------------------

output "test_acl_id" {
  description = <<EOT
Paste into the multica-test overlay:
  * deploy/k8s/overlays/test/kustomization.yaml — AlbConfig patch
      /spec/listeners/1/aclConfig/aclRelations/0/aclId
  * deploy/k8s/overlays/test/ingress-patch.yaml — annotation acl-id
EOT
  value       = alicloud_alb_acl.multica_test.id
}

output "test_database_url" {
  description = "Ready-to-paste DATABASE_URL for the multica-test namespace Secret."
  # URL-encode '@' in the password so the userinfo segment parses cleanly in
  # pgx / go-redis (RFC 3986 reserves '@' as the userinfo↔host separator).
  # Extend the replace() chain if you ever add other reserved chars.
  # sslmode=disable — test RDS has SSL turned off and traffic is VPC-internal
  # only (private IP); enabling SSL there is a separate ModifyDBInstanceSSL
  # call. Match the actual instance posture so pgx doesn't TLS-handshake into
  # a refusal.
  value     = "postgres://${alicloud_db_account.app_test.account_name}:${replace(var.rds_account_password, "@", "%40")}@${alicloud_db_instance.multica_test.connection_string}:${alicloud_db_instance.multica_test.port}/${alicloud_db_database.multica_test.data_base_name}?sslmode=disable"
  sensitive = true
}

output "test_redis_url" {
  description = <<EOT
Ready-to-paste REDIS_URL for the multica-test namespace Secret. Set it so
the server (main.go) routes WS fanout through the Tair sharded cluster
instead of the in-memory hub — required for multi-replica testing.
EOT
  value       = "redis://:${replace(var.redis_password, "@", "%40")}@${alicloud_kvstore_instance.tair_test.connection_domain}:6379"
  sensitive   = true
}

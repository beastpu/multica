# Prod-only cloud resources beyond what alb_acl.tf / rds.tf already track.
#
# Why a separate file: alb_acl.tf and rds.tf describe resources that were
# imported retroactively from a manually-provisioned prod. New prod resources
# we intend to manage from day one go here so the "this is IaC-owned"
# boundary stays obvious.

# ---------------------------------------------------------------------------
# Prod Tair (Redis Memory Enhanced) — sharded cluster for WS realtime fanout
# ---------------------------------------------------------------------------
#
# Why we need it: server/cmd/server/main.go switches from in-memory WS hub to
# Redis Streams when REDIS_URL is set. Without a Redis backend prod has to
# stay at replicas=1, which is the constraint called out in
# deploy/k8s/base/server-deployment.yaml. Provisioning this Tair lets prod
# scale horizontally once REDIS_URL is wired into the prod Secret.
#
# Spec mirrored from the legacy reference instance r-uf6djfydidx2c4o5y2:
#   * tair.rdb.cluster.sharding.common is no longer sellable in cn-shanghai
#     (DescribeAvailableResource returns zero matches), so we use the closest
#     live equivalent — redis.amber.* (Tair Memory Enhanced multithread).
#   * Capacity: 2 shards × 2GB = 4GB, identical to the reference instance.
#   * Engine 5.0 — amber cluster SKUs only support 5.0 in cn-shanghai today,
#     which is fine because Multica's relay uses Redis Streams (introduced
#     in 5.0).
#
# Differences from test:
#   * Co-located in cn-shanghai-n (same AZ as prod RDS) instead of -l, so
#     server→Redis traffic stays within an AZ.
#   * PrePaid period defaults to var.rds_period_months (1 month); bump in
#     tfvars to commit longer once you're confident in the spec.
#   * prevent_destroy = true. Test can be torn down freely; prod cannot.
#   * Password reuses var.redis_password (same as test); user explicitly
#     accepted that test/prod share auth.

resource "alicloud_kvstore_instance" "tair_prod" {
  db_instance_name = "multica-prod-tair"

  instance_class = "redis.amber.logic.sharding.2g.2db.0rodb.6proxy.multithread"
  instance_type  = "Redis"
  engine_version = "5.0"
  shard_count    = 2

  payment_type      = "PrePaid"
  period            = var.rds_period_months
  auto_renew        = var.rds_auto_renew ? "true" : "false"
  auto_renew_period = var.rds_period_months

  zone_id    = "cn-shanghai-n"
  vswitch_id = "vsw-uf6cmsr8x8tx31pvst0qd" # cn-shanghai-n, same AZ as prod RDS

  vpc_auth_mode = "Open"

  # Tair's own IP allowlist (separate from any ALB ACL). Default is
  # 127.0.0.1 which would block every pod; opening to the K8s pod CIDR lets
  # the realtime relay XADD/XREAD. The instance password still gates reads.
  security_ips = ["10.159.0.0/16"]

  password = var.redis_password

  tags = merge(local.common_tags, { "env" = "prod" })

  lifecycle {
    # Prod Tair carries WS relay state for every connected client; deleting
    # it mid-flight would disconnect everyone. Force destroy via console.
    prevent_destroy = true

    ignore_changes = [
      maintain_start_time,
      maintain_end_time,
      parameters,
    ]
  }
}

output "prod_redis_url" {
  description = <<EOT
Ready-to-paste REDIS_URL for the prod multica-secrets Secret. After applying
this resource, patch the prod Secret and then you can raise the prod
multica-server replica count above 1 (currently capped at 1 by the in-memory
hub in deploy/k8s/base/server-deployment.yaml).

  TEST_REDIS_URL="$(tofu output -raw prod_redis_url)"
  kubectl -n multica patch secret multica-secrets --type=json -p="[{...}]"
EOT
  # URL-encode '@' so userinfo parses cleanly in go-redis (RFC 3986).
  value     = "redis://:${replace(var.redis_password, "@", "%40")}@${alicloud_kvstore_instance.tair_prod.connection_domain}:6379"
  sensitive = true
}

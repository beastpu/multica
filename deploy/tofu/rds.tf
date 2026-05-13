# Aliyun RDS PostgreSQL for Multica.
#
# Engine notes:
#   * pg_bigm (Chinese bigram FTS, used by migration 032) and pgvector are
#     preinstalled on Aliyun RDS PG — no extra extension install step.
#     Migrations run `CREATE EXTENSION IF NOT EXISTS ...` themselves.
#   * The instance is VPC-attached and placed in a vSwitch of the ACK cluster
#     VPC so pods can reach it without leaving the VPC.
#   * Charge type is Prepaid (包年包月) for predictable billing — adjust via
#     var.rds_period_months / var.rds_auto_renew.

resource "alicloud_db_instance" "multica" {
  engine           = "PostgreSQL"
  engine_version   = var.rds_engine_version
  instance_type    = var.rds_instance_class
  instance_storage = var.rds_storage_gb
  category         = "HighAvailability"

  instance_charge_type = "Prepaid"
  period               = var.rds_period_months
  auto_renew           = var.rds_auto_renew
  auto_renew_period    = var.rds_period_months

  instance_name            = "multica-${var.env}"
  db_instance_storage_type = "cloud_essd"

  # Auto minor-version upgrades (matches cloud setting). Aliyun rolls patch
  # releases during the maintenance window; opt-in by default.
  auto_upgrade_minor_version = "Auto"

  # Pinned to cn-shanghai-n where the existing instance was provisioned.
  # vswitch_id is ForceNew — using `local.vswitches[0]` would re-order on a
  # data-source refresh and tofu would propose to recreate prod.
  vswitch_id = "vsw-uf6cmsr8x8tx31pvst0qd"

  # Whole pod CIDR in one entry — matches the cloud-side default group that
  # was set when the instance was provisioned. Listing per-vSwitch CIDRs
  # would be more granular but every vSwitch in this VPC sits inside /16.
  security_ips = ["10.159.0.0/16"]

  tags = local.common_tags

  lifecycle {
    ignore_changes = [
      # Aliyun console sometimes mutates these after creation (maintenance
      # window etc.). Leave alone unless explicitly changed here.
      parameters,
      # Prepaid period is set at create time; Aliyun returns the *remaining*
      # subscription length on subsequent reads, which trips tofu into
      # wanting to "renew" every plan. Ignore once imported.
      period,
      auto_renew_period,
    ]
  }
}

resource "alicloud_db_account" "app" {
  db_instance_id   = alicloud_db_instance.multica.id
  account_name     = var.rds_account_name
  account_password = var.rds_account_password
  account_type     = "Super" # PostgreSQL on Aliyun RDS uses Super for the primary app user.

  lifecycle {
    # The live password was set manually before this resource was imported
    # and may not match var.rds_account_password (the default "Lilith@123").
    # Ignore here so plan doesn't silently propose a password reset on every
    # apply — rotate via ResetAccountPassword + a manual update of the var.
    ignore_changes = [account_password]
  }
}

resource "alicloud_db_database" "multica" {
  instance_id    = alicloud_db_instance.multica.id
  data_base_name = var.rds_database_name
  character_set  = "UTF8"

  # Account-level privilege binding happens via alicloud_db_account_privilege
  # for non-Super accounts. The Super account above already has full access.
  depends_on = [alicloud_db_account.app]
}

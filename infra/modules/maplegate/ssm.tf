locals {
  param_prefix = "/${var.name}"
}

# 値は手入力するため Terraform は初期プレースホルダのみ作成する
resource "aws_ssm_parameter" "discord_bot_token" {
  name  = "${local.param_prefix}/discord/bot_token"
  type  = "SecureString"
  value = "placeholder"

  lifecycle {
    ignore_changes = [value]
  }
}

resource "aws_ssm_parameter" "discord_app_public_key" {
  name  = "${local.param_prefix}/discord/app_public_key"
  type  = "String"
  value = "placeholder"

  lifecycle {
    ignore_changes = [value]
  }
}

resource "aws_ssm_parameter" "discord_channel_id" {
  name  = "${local.param_prefix}/discord/channel_id"
  type  = "String"
  value = "placeholder"

  lifecycle {
    ignore_changes = [value]
  }
}

resource "aws_ssm_parameter" "server_public_key" {
  name  = "${local.param_prefix}/server_public_key"
  type  = "String"
  value = "pending"

  lifecycle {
    ignore_changes = [value]
  }
}

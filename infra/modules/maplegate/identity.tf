data "aws_ssoadmin_instances" "this" {}

locals {
  identity_store_id = tolist(data.aws_ssoadmin_instances.this.identity_store_ids)[0]
  sso_instance_arn  = tolist(data.aws_ssoadmin_instances.this.arns)[0]
  # The portal URL is the identity store id unless the administrator set an alias.
  start_url = var.identity_center_start_url != "" ? var.identity_center_start_url : "https://${local.identity_store_id}.awsapps.com/start"
}

# Members are added by hand in the Identity Center console: invitations and MFA enrolment are manual anyway, and e-mail addresses stay out of the code and the state.
resource "aws_identitystore_group" "players" {
  identity_store_id = local.identity_store_id
  display_name      = var.players_group_name
  description       = "May request a VPN session"
}

resource "aws_ssoadmin_permission_set" "player" {
  name             = "${var.name}-player"
  instance_arn     = local.sso_instance_arn
  session_duration = var.player_session_duration
}

# Since the Oct 2025 Function URL change a caller needs both actions
data "aws_iam_policy_document" "player" {
  statement {
    actions   = ["lambda:InvokeFunctionUrl"]
    resources = [aws_lambda_function.fn["session"].arn]
    condition {
      test     = "StringEquals"
      variable = "lambda:FunctionUrlAuthType"
      values   = ["AWS_IAM"]
    }
  }
  statement {
    actions   = ["lambda:InvokeFunction"]
    resources = [aws_lambda_function.fn["session"].arn]
    condition {
      test     = "Bool"
      variable = "lambda:InvokedViaFunctionUrl"
      values   = ["true"]
    }
  }
}

resource "aws_ssoadmin_permission_set_inline_policy" "player" {
  instance_arn       = local.sso_instance_arn
  permission_set_arn = aws_ssoadmin_permission_set.player.arn
  inline_policy      = data.aws_iam_policy_document.player.json
}

resource "aws_ssoadmin_account_assignment" "players" {
  instance_arn       = local.sso_instance_arn
  permission_set_arn = aws_ssoadmin_permission_set.player.arn
  principal_id       = aws_identitystore_group.players.group_id
  principal_type     = "GROUP"
  target_id          = data.aws_caller_identity.current.account_id
  target_type        = "AWS_ACCOUNT"
}

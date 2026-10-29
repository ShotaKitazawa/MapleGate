data "aws_caller_identity" "current" {}

locals {
  account_id   = data.aws_caller_identity.current.account_id
  session_name = "gamevpn-session"
  discord_name = "gamevpn-discord"
  janitor_name = "gamevpn-janitor"
  instance_arn = "arn:aws:ec2:us-west-2:${local.account_id}:instance/${aws_instance.vpn.id}"
  param_arn    = "arn:aws:ssm:us-west-2:${local.account_id}:parameter${local.param_prefix}/*"
  discord_arn  = "arn:aws:lambda:us-west-2:${local.account_id}:function:${local.discord_name}"
  lambda_env = {
    TABLE_NAME           = aws_dynamodb_table.gamevpn.name
    INSTANCE_ID          = aws_instance.vpn.id
    PARAM_PREFIX         = local.param_prefix
    WG_PORT              = tostring(var.wg_port)
    SESSION_TTL          = var.session_ttl
    SESSION_EXTEND_EVERY = var.session_extend_every
    HANDSHAKE_IDLE       = "${var.handshake_idle_seconds}s"
  }
  functions = toset(["session", "discord", "janitor", "notify"])
}

data "archive_file" "fn" {
  for_each    = local.functions
  type        = "zip"
  source_file = "${path.module}/../../build/${each.key}/bootstrap"
  output_path = "${path.module}/../../build/${each.key}.zip"
}

data "aws_iam_policy_document" "assume_lambda" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

# None of the functions can run commands on the node: the discord endpoint is public, so it gets no SSM execution rights.
data "aws_iam_policy_document" "common" {
  statement {
    actions = [
      "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:DeleteItem", "dynamodb:Query",
    ]
    resources = [aws_dynamodb_table.gamevpn.arn]
  }
  statement {
    actions   = ["ssm:GetParameter"]
    resources = [local.param_arn]
  }
}

# Only the janitor touches the instance: the other two are reachable from the Internet and only record intent.
data "aws_iam_policy_document" "janitor_instance" {
  statement {
    actions   = ["ec2:StartInstances"]
    resources = [local.instance_arn]
  }
  # Describe* actions do not support resource-level permissions
  statement {
    actions   = ["ec2:DescribeInstances"]
    resources = ["*"]
  }
}

data "aws_iam_policy_document" "discord_self_invoke" {
  statement {
    actions   = ["lambda:InvokeFunction"]
    resources = [local.discord_arn]
  }
}

resource "aws_iam_role" "fn" {
  for_each           = local.functions
  name               = "gamevpn-${each.key}"
  assume_role_policy = data.aws_iam_policy_document.assume_lambda.json
}

# The notifier only reads the bot settings: it has no reason to touch the table.
resource "aws_iam_role_policy" "common" {
  for_each = setsubtract(local.functions, ["notify"])
  role     = aws_iam_role.fn[each.key].id
  policy   = data.aws_iam_policy_document.common.json
}

data "aws_iam_policy_document" "notify" {
  statement {
    actions   = ["ssm:GetParameter"]
    resources = [local.param_arn]
  }
}

resource "aws_iam_role_policy" "notify" {
  role   = aws_iam_role.fn["notify"].id
  policy = data.aws_iam_policy_document.notify.json
}

resource "aws_iam_role_policy" "janitor_instance" {
  role   = aws_iam_role.fn["janitor"].id
  policy = data.aws_iam_policy_document.janitor_instance.json
}

resource "aws_iam_role_policy" "discord_self_invoke" {
  role   = aws_iam_role.fn["discord"].id
  policy = data.aws_iam_policy_document.discord_self_invoke.json
}

resource "aws_iam_role_policy_attachment" "logs" {
  for_each   = local.functions
  role       = aws_iam_role.fn[each.key].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_cloudwatch_log_group" "fn" {
  for_each          = local.functions
  name              = "/aws/lambda/gamevpn-${each.key}"
  retention_in_days = var.log_retention_days
}

locals {
  concurrency = {
    session = var.session_reserved_concurrency
    discord = var.discord_reserved_concurrency
    janitor = var.janitor_reserved_concurrency
    notify  = -1
  }
  timeout = {
    session = 30
    discord = 30
    janitor = 55
    notify  = 30
  }
}

resource "aws_lambda_function" "fn" {
  for_each                       = local.functions
  function_name                  = "gamevpn-${each.key}"
  role                           = aws_iam_role.fn[each.key].arn
  runtime                        = "provided.al2023"
  architectures                  = ["arm64"]
  handler                        = "bootstrap"
  filename                       = data.archive_file.fn[each.key].output_path
  source_code_hash               = data.archive_file.fn[each.key].output_base64sha256
  timeout                        = local.timeout[each.key]
  memory_size                    = 128
  reserved_concurrent_executions = local.concurrency[each.key]

  environment {
    variables = local.lambda_env
  }

  depends_on = [aws_cloudwatch_log_group.fn]
}

# An async approval that is dropped after its retries leaves the session pending, so the button can simply be pressed again.
resource "aws_lambda_function_event_invoke_config" "discord" {
  function_name          = aws_lambda_function.fn["discord"].function_name
  maximum_retry_attempts = 2
}

resource "aws_lambda_function_url" "session" {
  function_name      = aws_lambda_function.fn["session"].function_name
  authorization_type = "AWS_IAM"
}

resource "aws_lambda_function_url" "discord" {
  function_name      = aws_lambda_function.fn["discord"].function_name
  authorization_type = "NONE"
}

# A NONE URL needs both permissions since the Oct 2025 Function URL change; Lambda adds same-named statements itself, so ids here differ to avoid a 409
resource "aws_lambda_permission" "url" {
  for_each               = toset(["discord"])
  statement_id           = "TerraformFunctionUrlPublic"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.fn[each.key].function_name
  principal              = "*"
  function_url_auth_type = "NONE"
}

resource "aws_lambda_permission" "url_invoke" {
  for_each                 = toset(["discord"])
  statement_id             = "TerraformFunctionUrlInvokePublic"
  action                   = "lambda:InvokeFunction"
  function_name            = aws_lambda_function.fn[each.key].function_name
  principal                = "*"
  invoked_via_function_url = true
}

output "session_api_url" {
  value = aws_lambda_function_url.session.function_url
}

output "discord_interactions_url" {
  value       = aws_lambda_function_url.discord.function_url
  description = "Set as the Interactions Endpoint URL in the Discord developer portal"
}

output "instance_id" {
  value = aws_instance.vpn.id
}

# What the client installers are built with; the settings file adds game_processes, which is not infrastructure.
output "client_settings" {
  value = {
    start_url  = local.start_url
    region     = data.aws_region.current.region
    account_id = local.account_id
    role_name  = aws_ssoadmin_permission_set.player.name
    api_url    = trimsuffix(aws_lambda_function_url.session.function_url, "/")
  }
}

output "dist_bucket" {
  value = aws_s3_bucket.artifacts.bucket
}

output "dist_key_macos" {
  value = var.dist_key_macos
}

output "dist_key_windows" {
  value = var.dist_key_windows
}

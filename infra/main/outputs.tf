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

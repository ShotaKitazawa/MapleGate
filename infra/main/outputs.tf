output "session_api_url" {
  value = module.maplegate.session_api_url
}

output "discord_interactions_url" {
  value       = module.maplegate.discord_interactions_url
  description = "Set as the Interactions Endpoint URL in the Discord developer portal"
}

output "instance_id" {
  value = module.maplegate.instance_id
}

output "client_settings" {
  value = module.maplegate.client_settings
}

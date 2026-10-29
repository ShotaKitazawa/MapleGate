variable "wg_port" {
  type    = number
  default = 51820
}

variable "wg_address" {
  type    = string
  default = "10.66.0.1/24"
}

variable "handshake_idle_seconds" {
  type    = number
  default = 600
}

variable "idle_stop_seconds" {
  type    = number
  default = 900
}

variable "boot_grace_seconds" {
  type    = number
  default = 600
}

variable "janitor_reserved_concurrency" {
  type    = number
  default = 1
}

variable "session_reserved_concurrency" {
  type    = number
  default = 2
}

variable "discord_reserved_concurrency" {
  type    = number
  default = 3
}

variable "session_ttl" {
  type    = string
  default = "4h"
}

# A session in use is extended to now + session_ttl once it has run this long without being extended; keep it below session_ttl.
variable "session_extend_every" {
  type    = string
  default = "30m"
}

variable "log_retention_days" {
  type    = number
  default = 14
}

variable "artifacts_bucket" {
  type    = string
  default = "example-artifacts"
}

variable "wg_subnet" {
  type    = string
  default = "10.66.0.0/24"
}

# Session Manager is the only way into the node (no inbound port besides WireGuard); set false once the node is proven.
variable "node_session_manager" {
  type    = bool
  default = true
}

variable "player_session_duration" {
  type    = string
  default = "PT1H"
}

variable "monthly_budget_usd" {
  description = "Monthly cost of the whole account that raises a notice"
  type        = number
  default     = 10
}

variable "alert_email" {
  description = "Also e-mail the alerts here; empty means Discord only. Cost notices should not depend on the Discord path alone."
  type        = string
  default     = ""
}

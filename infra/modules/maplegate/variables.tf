variable "name" {
  description = "Prefix of every named resource (table, roles, functions, budget, permission set) and of the SSM parameters"
  type        = string
  default     = "gamevpn"
}

variable "players_group_name" {
  description = "Identity Center group whose members may request a session"
  type        = string
  default     = "players"
}

variable "build_dir" {
  description = "Directory holding the built binaries: session, discord and janitor each with a bootstrap file, and agent/vpn-agent (`mise run build` writes build/)"
  type        = string
}

# The node is arm64 (the agent and the AMI are), so the type has to be a Graviton one.
variable "instance_type" {
  type    = string
  default = "t4g.nano"
}

variable "subnet_id" {
  description = "Subnet of the node; null picks the first subnet of the default VPC"
  type        = string
  default     = null
}

variable "dist_key_macos" {
  description = "Object the download command for macOS signs a link to; it has to be under dist/"
  type        = string
  default     = "dist/MapleGate.pkg"

  validation {
    condition     = startswith(var.dist_key_macos, "dist/")
    error_message = "The functions may read dist/ and nothing else in the bucket."
  }
}

variable "dist_key_windows" {
  description = "The same for Windows; empty answers that there is no installer yet"
  type        = string
  default     = ""

  validation {
    condition     = var.dist_key_windows == "" || startswith(var.dist_key_windows, "dist/")
    error_message = "The functions may read dist/ and nothing else in the bucket."
  }
}

variable "identity_center_start_url" {
  description = "Sign-in URL of the Identity Center portal; empty builds it from the identity store id, which is right unless an alias was set"
  type        = string
  default     = ""
}

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
  description = "Bucket for the node scripts, the agent and the installers; names are global, so there is no default"
  type        = string
}

variable "wg_subnet" {
  type    = string
  default = "10.66.0.0/24"

  validation {
    condition     = can(regex("^[0-9]+\\.[0-9]+\\.[0-9]+\\.0/24$", var.wg_subnet))
    error_message = "wg_subnet must be a /24 such as 10.66.0.0/24: the addresses are handed out from its first three octets."
  }
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

variable "budget_email" {
  description = "Where the monthly budget notices are e-mailed"
  type        = string
}

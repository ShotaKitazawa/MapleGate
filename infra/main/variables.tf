variable "region" {
  description = "Region of everything; the node's latency to the players follows from it"
  type        = string
}

variable "name" {
  type    = string
  default = "gamevpn"
}

variable "artifacts_bucket" {
  type = string
}

variable "budget_email" {
  type = string
}

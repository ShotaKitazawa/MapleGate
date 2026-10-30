terraform {
  required_version = ">= 1.11"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }

  # The state is local until a backend is named. For a shared one, the bucket made by ../bootstrap works:
  #   backend "s3" {
  #     bucket       = "<state bucket>"
  #     key          = "maplegate/terraform.tfstate"
  #     region       = "<region>"
  #     use_lockfile = true
  #   }
}

provider "aws" {
  region = var.region

  default_tags {
    tags = { Project = var.name }
  }
}

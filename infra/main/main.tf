module "maplegate" {
  source = "../modules/maplegate"

  name             = var.name
  artifacts_bucket = var.artifacts_bucket
  budget_email     = var.budget_email
  build_dir        = "${path.root}/../../build"
}

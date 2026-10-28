resource "aws_s3_bucket" "artifacts" {
  bucket = var.artifacts_bucket
}

resource "aws_s3_bucket_public_access_block" "artifacts" {
  bucket                  = aws_s3_bucket.artifacts.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

locals {
  node_dir   = "${path.module}/../../node"
  node_files = setunion(fileset(local.node_dir, "*.sh"), fileset(local.node_dir, "*.conf"), fileset(local.node_dir, "units/*"))
}

# Node scripts ship through S3 and are fetched on every boot, so editing them never replaces the instance.
resource "aws_s3_object" "node" {
  for_each    = local.node_files
  bucket      = aws_s3_bucket.artifacts.id
  key         = "node/${each.value}"
  source      = "${local.node_dir}/${each.value}"
  source_hash = filemd5("${local.node_dir}/${each.value}")
}

resource "aws_s3_object" "agent" {
  bucket      = aws_s3_bucket.artifacts.id
  key         = "agent/vpn-agent"
  source      = "${path.module}/../../build/agent/vpn-agent"
  source_hash = filemd5("${path.module}/../../build/agent/vpn-agent")
}

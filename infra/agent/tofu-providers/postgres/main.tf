# Terraform configuration for CloudNativePG cluster
# This assumes you have a Kubernetes cluster and the CloudNativePG operator installed

terraform {
  required_version = ">= 1.0"

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.23"
    }
  }
}

provider "kubernetes" {}

locals {
  cluster_name = lower("${var.environment_config.name}-${var.service_name}-${var.dependency_name}")
  namespace    = var.environment_config.namespace
  instances    = var.provider_user_input.instances
  postgres_version = lookup(var.dependency_config,"version","17")
  storage_size = "2Gi"
  pg_user    = substr(replace(lower(var.service_name), "/[^a-z0-9_]/", "_"), 0, 63)
  pg_db_name = substr(replace(lower(var.service_name), "/[^a-z0-9_]/", "_"), 0, 63)
}

# PostgreSQL Cluster Resource
resource "kubernetes_manifest" "postgres_cluster" {
  manifest = {
    apiVersion = "postgresql.cnpg.io/v1"
    kind       = "Cluster"

    metadata = {
      name      = local.cluster_name
      namespace = local.namespace
    }

    spec = {
      instances = local.instances

      imageName = "ghcr.io/cloudnative-pg/postgresql:${local.postgres_version}"

      bootstrap = {
        initdb = {
          database = local.pg_db_name
          owner    = local.pg_user
          secret = {
            name = "${local.cluster_name}-app-user"
          }
        }
      }

      storage = {
        size         = local.storage_size
      }


      resources = {
        requests = {
          memory = "1Gi"
          cpu    = "500m"
        }
        limits = {
          memory = "2Gi"
          cpu    = "1"
        }
      }

      affinity = {
        enablePodAntiAffinity = true
        topologyKey           = "kubernetes.io/hostname"
      }
    }
  }
  wait {
    condition {
      type   = "Ready"
      status = "True"
    }
  }

  timeouts {
    create = "15m"
  }
}

resource "random_password" "postgres_password" {
  length  = 24
  special = false
}

# Secret for application user (you'll need to provide actual credentials)
resource "kubernetes_secret" "app_user" {
  metadata {
    name      = "${local.cluster_name}-app-user"
    namespace = local.namespace
  }

  data = {
    username = local.pg_user
    password = random_password.postgres_password.result
  }

  type = "kubernetes.io/basic-auth"
}

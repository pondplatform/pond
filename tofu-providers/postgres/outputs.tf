

output "host" {
  description = "Service name for primary (read-write) connection"
  value       = "${local.cluster_name}-rw.${local.namespace}.svc.cluster.local"
}

output "port" {
    value = 5432
}

output "username" {
  value = local.pg_user
}

output "password" {
  value = random_password.postgres_password.result
  sensitive = true
}

output "database" {
  value = local.pg_db_name
}

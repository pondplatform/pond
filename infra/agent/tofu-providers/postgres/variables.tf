variable "service_name" {
  type = string
  nullable = false
}

variable "dependency_name" {
  type = string
  nullable = false
}

variable "dependency_config" {
  nullable = false

}

variable "provider_user_input" {
  nullable = false
}

variable "environment_config" {
  nullable = false
}

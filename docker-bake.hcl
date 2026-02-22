variable "TAG" {
  default = "latest"
}

variable "REGISTRY" {
  default = ""
}

variable "IMAGE_NAME" {
  default = "ripple"
}

function "full_image_name" {
  params = []
  result = REGISTRY != "" ? "${REGISTRY}/${IMAGE_NAME}" : IMAGE_NAME
}

group "default" {
  targets = ["ripple"]
}

target "ripple" {
  dockerfile = "Dockerfile"
  context    = "."
  tags = [
    "${full_image_name()}:${TAG}",
    "${full_image_name()}:latest",
  ]
  platforms = ["linux/amd64", "linux/arm64"]
}

target "local" {
  inherits = ["ripple"]
  platforms = ["linux/amd64"]
  output   = ["type=docker"]
}

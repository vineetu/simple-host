variable "RELEASE_TAG" {
  default = ""
}

variable "RELEASE_COMMIT" {
  default = ""
}

variable "RELEASE_IMAGE_DIR" {
  default = ""
}

variable "RELEASE_PLATFORMS" {
  default = "linux/amd64,linux/arm64"
}

group "default" {
  targets = ["host", "hack", "standalone"]
}

target "host" {
  context = "."
  target = "host"
  platforms = split(",", RELEASE_PLATFORMS)
  args = {
    VERSION = RELEASE_TAG
    COMMIT = RELEASE_COMMIT
  }
  output = ["type=oci,dest=${RELEASE_IMAGE_DIR}/host.tar"]
  attest = ["type=provenance,mode=max"]
}

target "hack" {
  inherits = ["host"]
  target = "hack"
  output = ["type=oci,dest=${RELEASE_IMAGE_DIR}/hack.tar"]
}

target "standalone" {
  context = "."
  dockerfile = "deploy/hack/standalone/Dockerfile"
  platforms = split(",", RELEASE_PLATFORMS)
  contexts = {
    hack-base = "target:hack"
  }
  args = {
    SIMPLE_HOST_IMAGE = "hack-base"
  }
  output = ["type=oci,dest=${RELEASE_IMAGE_DIR}/standalone.tar"]
  attest = ["type=provenance,mode=max"]
}

packer {
  required_plugins {
    digitalocean = {
      version = ">= 1.4.0"
      source = "github.com/digitalocean/digitalocean"
    }
  }
}

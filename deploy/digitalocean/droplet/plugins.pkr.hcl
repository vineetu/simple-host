# The DigitalOcean builder is a Packer plugin (Packer 1.7+). `packer init` on
# this file installs it; template.json (JSON, as in DigitalOcean's
# droplet-1-clicks repository) cannot declare it itself.
packer {
  required_plugins {
    digitalocean = {
      version = ">= 1.4.1"
      source  = "github.com/digitalocean/digitalocean"
    }
  }
}

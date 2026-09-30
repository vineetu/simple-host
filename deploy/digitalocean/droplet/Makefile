# Simple Host 1-Click image for DigitalOcean. Run from this directory.
# build needs DIGITALOCEAN_API_TOKEN; validate and test do not.
.PHONY: init validate build test pins lint

init:
	packer init plugins.pkr.hcl

validate: init
	DIGITALOCEAN_API_TOKEN=$${DIGITALOCEAN_API_TOKEN:-validate-only} packer validate template.json

build: init validate
	packer build template.json

# The pin in template.json and first-login.sh matches the setup page's.
pins:
	bash test/pins_test.sh

lint:
	bash -n scripts/*.sh files/opt/simple-host-setup/first-login.sh files/var/lib/cloud/scripts/per-instance/001_onboot test/*.sh
	sh -n files/etc/update-motd.d/99-one-click
	shellcheck scripts/010-docker.sh scripts/011-simple-host.sh files/opt/simple-host-setup/first-login.sh files/var/lib/cloud/scripts/per-instance/001_onboot files/etc/update-motd.d/99-one-click test/*.sh

# The first-login setup in an ubuntu:24.04 container, installer stubbed.
test: pins lint
	bash test/first-login_test.sh

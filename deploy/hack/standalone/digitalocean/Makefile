.PHONY: init validate build
init:
	packer init plugins.pkr.hcl
validate: init
	DIGITALOCEAN_API_TOKEN=$${DIGITALOCEAN_API_TOKEN:-validate-only} packer validate template.json
build: validate
	packer build template.json

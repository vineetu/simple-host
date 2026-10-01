"""Adapt only network names/ports for a disposable local Coolify proxy check."""
import os
from pathlib import Path
import sys
import yaml

source,output=sys.argv[1:]
data=yaml.safe_load(Path(source).read_text())
network='sh-hack-package-proxy'
data['networks']['coolify']['name']=network
labels=data['services']['app']['labels']
data['services']['app']['labels']=[v.replace('traefik.docker.network=coolify','traefik.docker.network='+network) for v in labels]
data['services']['proxy']={
    'image':os.environ.get('TEST_PROXY_IMAGE','traefik:v3.6.7'),
    'mem_limit':'256m',
    'command':['--providers.docker=true','--providers.docker.exposedbydefault=false',
               '--entrypoints.http.address=:80','--entrypoints.https.address=:443',
               '--entrypoints.https.allowacmebypass=true'],
    'volumes':['/var/run/docker.sock:/var/run/docker.sock:ro'],
    'networks':['coolify'],
    'ports':['127.0.0.1:18474:80','127.0.0.1:18475:443'],
    'depends_on':{'app':{'condition':'service_healthy'}},
}
Path(output).write_text(yaml.safe_dump(data,sort_keys=False))

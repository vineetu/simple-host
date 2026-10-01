"""Disposable full-platform fixture: no external mail, account or API calls.

This deliberately uses admin-created test accounts. Browser email-code sign-in
and production ACME issuance are separate integration checks.
"""
import datetime
import json
from pathlib import Path
import subprocess
import sys

root=Path(sys.argv[1]); mode=sys.argv[2]
env=dict((k,json.loads(v)) for k,v in (s.split('=',1) for s in (root/'.env').read_text().splitlines()))
domain=env['SITE_DOMAIN']; admin=env['ADMIN_API_KEY']
port=env['HTTPS_BIND'].rsplit(':',1)[1]
def call(method,path,data=None,key=admin,host=domain):
    args=['curl','--silent','--show-error','--fail-with-body','--noproxy','*',
          '--cacert',str(root/'root.crt'),'--resolve',f'{host}:{port}:127.0.0.1',
          '-X',method,'-H','Content-Type: application/json',f'https://{host}:{port}{path}']
    if key: args+=['-H','X-API-Key: '+key]
    if data is not None: args+=['--data-binary','@-']
    p=subprocess.run(args,input=json.dumps(data) if data is not None else None,text=True,capture_output=True)
    if p.returncode:
        raise RuntimeError(f'{method} {path} failed: {p.stdout[:500]} {p.stderr[:200]}')
    try: return json.loads(p.stdout)
    except json.JSONDecodeError: return p.stdout

def refuses_certificate(host):
    # No --fail: an issued certificate followed by an HTTP 404 is a failure
    # here. Unknown/unpublished names must fail at the TLS handshake itself.
    result=subprocess.run(['curl','--silent','--show-error','--max-time','8',
        '--noproxy','*','--cacert',str(root/'root.crt'),
        '--resolve',f'{host}:{port}:127.0.0.1',f'https://{host}:{port}/'],capture_output=True)
    assert result.returncode != 0, 'certificate issued for '+host

slug='package-smoke'
path='/v1/hack/events/'+slug
if mode=='create':
    account=call('POST','/v1/admin/users',{'emails':['package-participant@example.test']})['created'][0]
    participant=account['api_key']
    now=datetime.datetime.now(datetime.timezone.utc)
    event=call('POST','/v1/hack/events',{
        'slug':slug,'title':'Package smoke','organiser_name':'Fixture',
        'organisation':'Local verification','contact_email':'fixture@example.test',
        'purpose':'Verify standalone installation','expected_participants':3,
        'starts_at':now.isoformat(),'ends_at':(now+datetime.timedelta(days=1)).isoformat(),'time_zone':'UTC'})
    call('POST',path+'/stage',{'stage':'open'})
    event=call('GET',path)
    call('POST','/v1/hack/join/'+event['organiser']['join_code'],
         {'display_name':'Participant','accept_coc':True},participant)
    team=call('POST',path+'/teams',{'name':'Fixture team'},participant)
    team_key=call('POST',path+'/key',{},participant)['key']
    team_slug=team['slug']
    refuses_certificate(team_slug+'.'+slug+'.'+domain)
    published=call('PUT','/v1/sites/'+team_slug+'/files?create=1',
                   {'files':{'index.html':'<!doctype html><title>Fixture</title>Persisted Hack project'}},team_key)
    fixture={'team_slug':team_slug,'site_url':published.get('url')}
    (root/'fixture.json').write_text(json.dumps(fixture))
else:
    fixture=json.loads((root/'fixture.json').read_text())
    event=call('GET',path)
    assert event['event']['title']=='Package smoke'
team_host=fixture['team_slug']+'.'+slug+'.'+domain
assert 'Persisted Hack project' in call('GET','/',key=None,host=team_host)
assert 'Package smoke' in call('GET','/',key=None,host=slug+'.'+domain)
assert call('GET','/v1/hack/events')[0]['slug']==slug
refuses_certificate('unknown-event.'+domain)
refuses_certificate('extra.'+team_host)
print('PASS: '+mode+' — event, member, team key, published team site, event host; unknown/deeper host certificates refused')

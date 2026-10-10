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
    call('PUT',path+'/entry',{'title':'Persistent entry','description':'Upgrade fixture entry'},participant)
    refuses_certificate(team_slug+'.'+slug+'.'+domain)
    published=call('PUT','/v1/sites/'+team_slug+'/files?create=1',
                   {'files':{'index.html':'<!doctype html><title>Fixture</title>Persisted Hack project'}},team_key)
    fixture={'team_slug':team_slug,'site_url':published.get('url'),'participant':participant,'team_key':team_key}
    call('PATCH',path,{'tagline':'Persistent event setting'})
    (root/'fixture.json').write_text(json.dumps(fixture))
    (root/'fixture.json').chmod(0o600)
else:
    fixture=json.loads((root/'fixture.json').read_text())
    event=call('GET',path)
    assert event['event']['title']=='Package smoke'
assert call('GET',path)['event']['tagline']=='Persistent event setting'
assert call('GET',path+'/entry',key=fixture['participant'])['entry']['title']=='Persistent entry'
sites=call('GET','/v1/sites',key=fixture['team_key'])
assert len(sites)==1 and sites[0]['name']==fixture['team_slug']
team_host=fixture['team_slug']+'.'+slug+'.'+domain
assert 'Persisted Hack project' in call('GET','/',key=None,host=team_host)
assert 'Package smoke' in call('GET','/',key=None,host=slug+'.'+domain)
assert call('GET','/v1/hack/events')[0]['slug']==slug
refuses_certificate('unknown-event.'+domain)
refuses_certificate('extra.'+team_host)
print('PASS: '+mode+' — event, member, team key, published team site, event host; unknown/deeper host certificates refused')

if mode=='presentation':
    home=call('GET','/',key=None)
    started=call('GET','/get-started',key=None)
    for text in [home,started]:
        assert 'simple-hack.app' not in text
        assert 'fonts.googleapis.com' not in text
        assert text.count('class="sh-header sh-hack"')==1
        assert text.count('class="sh-footer"')==1
        assert '/hack-ink.css?v=' in text
    for control in ['film','replayBtn','playBtn','scrub','soundBtn','clipSlot']:
        assert 'id="'+control+'"' in home
    assert home.count('class="cap" data-i=')==8
    assert 'Pick your AI' in started and 'Try one of these' in started
    assert 'https://'+domain+'/mcp' in started
    assert 'id="other-installs"' in started
    for font in ['caveat.woff2','kalam-regular.woff2']:
        response=subprocess.run(['curl','--silent','--show-error','--fail','--noproxy','*','--cacert',str(root/'root.crt'),'--resolve',f'{domain}:{port}:127.0.0.1',f'https://{domain}:{port}/fonts/{font}'],capture_output=True)
        assert response.returncode==0 and response.stdout.startswith(b'wOF2')
    for name in ['run-hackathon','join-hackathon','judge-hackathon','website-deploy','website-deploy-builder']:
        text=call('GET','/v1/skills/'+name+'/SKILL.md',key=None)
        assert 'simple-hack.app' not in text and domain in text
    assert domain in call('GET','/llms.txt',key=None)
    assert 'simple-hack.app' not in json.dumps(call('GET','/openapi.json',key=None))
    print('PASS: film, ink, shared navigation, instance connector/examples/API/skills, local fonts')

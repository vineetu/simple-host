#!/usr/bin/env node
// Manual TLS, existing workload identity and KMS values through the actual browser and chart.
// NODE_PATH=/opt/pw/node_modules SH_ENTERPRISE_CHART=/path/to/chart node scripts/e2e-setup-kubernetes-options.js <base> <output-directory>
const {chromium}=require('playwright');
const {execFileSync}=require('child_process');
const fs=require('fs');
const path=require('path');
const base=process.argv[2], dir=process.argv[3], chart=process.env.SH_ENTERPRISE_CHART;
if(!base || !dir || !chart) throw new Error('Provide base, output directory and SH_ENTERPRISE_CHART');
fs.mkdirSync(dir,{recursive:true});
const valuesFile=path.join(dir,'manual-values.yaml'), caFile=path.join(dir,'render-only-ca.crt');
fs.writeFileSync(caFile,'render-only CA placeholder\n');
(async()=>{
 const browser=await chromium.launch();const page=await browser.newPage();
 await page.goto(base+'/setup?product=enterprise');
 await page.getByRole('button',{name:'Next',exact:true}).click();
 await page.evaluate(()=>{window.shSetup.applyBasic('certs','manual');window.shSetup.applyBasic('creds','identity');window.shSetup.refresh([]);});
 for(const [k,v]of Object.entries({host:'sites.example.com',admins:'a@example.com',issuer:'https://login.example.com',clientId:'id',endpoint:'https://objects.example.com',region:'test-1',bucketName:'sites',dbHost:'postgres',context:"team's $(do-not-run)",namespace:'test-space'})) await page.fill('#f-'+k,v);
 await page.evaluate(async()=>{await window.shSetup.ready();window.shSetup.apply('BACKUP_SSE','aws:kms');});
 await page.getByRole('button',{name:'Show my files',exact:true}).click();
 await page.waitForSelector('#files pre', { state: 'attached' });
 // A KMS key is free text in Advanced; the assistant may not set it. Set it through the real form below.
 await page.getByRole('button',{name:'Back',exact:true}).click();
 await page.getByRole('button',{name:'Back',exact:true}).click();
 await page.locator('label.choice:has(input[name=mode][value=advanced])').click();
 await page.getByRole('button',{name:'Next',exact:true}).click();
 await page.getByRole('button',{name:'Next: every setting',exact:true}).click();
 for(let i=0;i<30;i++){
  const field=page.locator('#f-BACKUP_SSE_KEY_ID');if(await field.count())await field.fill('alias/test-key');
  const show=page.getByRole('button',{name:'Show my files',exact:true});if(await show.count()){await show.click();break;}
  await page.locator('.nav .btn.solid').click();
 }
 await page.waitForSelector('#files pre', { state: 'attached' });
 const output=await page.locator('#files pre').allTextContents();
 fs.writeFileSync(valuesFile,output[0]);
 execFileSync('bash',['-n'],{input:output[2]});
 const docs=execFileSync('helm',['template','test',chart,'-n','test-space','-f',valuesFile,'--set-file','postgres.external.caCert='+caFile],{encoding:'utf8'});
 fs.writeFileSync(path.join(dir,'manual-render.yaml'),docs);
 if(/BACKUP_STORAGE_ACCESS_KEY_ID=/.test(output[1])||/kind: ClusterIssuer/.test(docs)||/name: owner-hosts/.test(docs)||!/BACKUP_SSE_KEY_ID: "alias\/test-key"/.test(docs))throw new Error('manual/identity/KMS mismatch');
 console.log('PASS: manual TLS, existing identity, KMS typed mapping, shell quoting and actual Helm rendering');
 await browser.close();
})().catch(e=>{console.error(e);process.exit(1)});

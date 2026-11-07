// Exercise the bundled bass component through the shared shell and action queue.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile, mkdir} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
const {chromium} = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = new URL('../', import.meta.url);
const feature = {id:'fixture.bass',component:'bass-offset',included:true,experimental:true,actions:['fixture.bass.set']};
let discovery = true, offline = false, failure = false, calls = [], revision = 0;
const state = {available:true,power:'On',mode:'Standard',volume:12,muted:false,input:'TV / ARC',csrf:'fixture',sound:{bass:2,center:0,atmos:1,rear:2,rear_docked:true,night:false},wifi:{},extensions:{'fixture.bass':{offset_db:0,status:'original'}}};
const snapshot = () => ({...state,revision:++revision});
const streams = new Set();
const timer = setInterval(() => {
  for (const res of streams) {
    if (offline) {res.destroy();streams.delete(res);continue;}
    res.write('data: '+JSON.stringify(snapshot())+'\n\n');
  }
},100);
const server = createServer(async (req,res) => {
  if (offline) {req.socket.destroy();return;}
  if (req.url === '/api/features') {
    if (!discovery) {res.writeHead(404);res.end();return;}
    res.setHeader('Content-Type','application/json');
    res.end(JSON.stringify({schema:1,model:'fixture',name:'Fixture soundbar',actions:['bass_next','power'],extensions:[feature]}));return;
  }
  if (req.url === '/api/events') {
    res.writeHead(200,{'Content-Type':'text/event-stream'});
    streams.add(res);res.on('close',()=>streams.delete(res));
    res.write('data: '+JSON.stringify(snapshot())+'\n\n');return;
  }
  if (req.url === '/api/state') {res.setHeader('Content-Type','application/json');res.end(JSON.stringify(snapshot()));return;}
  if (req.url === '/api/action' && req.method === 'POST') {
    let raw='';for await (const data of req) raw+=data;
    const command=JSON.parse(raw);calls.push(command);
    assert.equal(req.headers['x-csrf-token'],'fixture');
    await new Promise(resolve=>setTimeout(resolve,150));
    res.setHeader('Content-Type','application/json');
    if(failure){res.writeHead(409);res.end(JSON.stringify({error:'Unable to save offset',state:snapshot()}));return;}
    assert.equal(command.action,'fixture.bass.set');
    state.extensions['fixture.bass']={offset_db:command.parameters.offset_db,status:'waiting'};
    res.end(JSON.stringify(snapshot()));return;
  }
  const name = {'/':'index.html','/app.js':'app.js','/style.css':'style.css'}[req.url] || (/^\/components\/[a-z][a-z0-9-]*\.js$/.test(req.url)?req.url.slice(1):null);
  if(!name){res.writeHead(404);res.end();return;}
  res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'text/html');
  res.end(await readFile(new URL('apps/remote/www/'+name,root)));
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const browser=await chromium.launch({headless:true});
const errors=[];let checks=0;
const check=(condition,message)=>{assert.ok(condition,message);checks++;};
try {
  const page=await browser.newPage({viewport:{width:390,height:844},locale:'en-US',reducedMotion:'reduce'});
  page.on('pageerror',error=>errors.push(error.message));
  const url='http://127.0.0.1:'+server.address().port;
  await page.goto(url);
  await page.waitForFunction(()=>!document.querySelector('#bass-offset').disabled);
  check(await page.locator('[data-component=bass-offset]').isVisible(),'declared component is visible');
  check(await page.locator('#bass-offset option').count()===13,'thirteen half-decibel settings');
  check(await page.locator('#bass-offset').inputValue()==='0','zero default');
  check(await page.locator('#bass-offset option').evaluateAll(options=>options[0].value==='3' && options[12].value==='-3'),'descending bounded range');
  await page.locator('#bass-offset').selectOption('-3');
  await page.waitForTimeout(350);
  check(calls.length===0 && await page.locator('#bass-offset').inputValue()==='-3','live snapshots preserve unsaved draft without writes');
  check((await page.locator('#bass-offset-state').textContent()).includes('Select Save offset'),'draft is identified');
  await page.locator('#bass-offset-save').click();
  await page.waitForFunction(()=>document.querySelector('#message-text').textContent==='Bass offset saved for startup.');
  check(JSON.stringify(calls[0])==='{"action":"fixture.bass.set","parameters":{"offset_db":-3}}','declared namespace and typed parameters, no shared command field');
  check((await page.locator('#bass-offset-state').textContent()).includes('Waiting for supported Standard playback'),'save does not claim a DSP write');
  state.power='Standby';
  await page.waitForFunction(()=>document.querySelector('#power-label').textContent==='Turn on');
  check(await page.locator('#bass-offset-save').isEnabled(),'save available in standby');
  await page.locator('#bass-offset').selectOption('0');await page.locator('#bass-offset-save').click();
  await page.waitForFunction(()=>document.querySelector('#bass-offset-state').textContent.startsWith('0.0 dB set.'));
  check(calls[1].parameters.offset_db===0,'zero can be explicitly saved to restore stock');
  state.extensions['fixture.bass']={offset_db:0,status:'paused',error:'Readback failed'};
  await page.waitForFunction(()=>document.querySelector('#bass-offset-state').textContent.includes('Readback failed'));
  check((await page.locator('#bass-offset-state').textContent()).includes('paused'),'pause and reason are visible');
  state.extensions['fixture.bass']={offset_db:-1.5,status:'waiting',error:'WAITING_FOR_FRONT_EQ'};
  await page.waitForFunction(()=>document.querySelector('#bass-offset-state').textContent.includes('Front EQ'));
  check((await page.locator('#bass-offset-state').textContent())==='-1.5 dB set. Saved for startup. Paused while a Front EQ is applied. Turn off the Front EQ to use the bass offset.','Front EQ pause explained in words, without the generic wait');
  state.extensions['fixture.bass']={offset_db:-1.5,status:'waiting',error:'WAITING_FOR_STANDARD_PLAYBACK'};
  await page.waitForFunction(()=>!document.querySelector('#bass-offset-state').textContent.includes('Front EQ'));
  check(!(await page.locator('#bass-offset-state').textContent()).includes('WAITING_FOR'),'wait codes never shown raw');
  check((await page.locator('#bass-offset-state').textContent()).endsWith('Waiting for supported Standard playback.'),'standard playback wait keeps its sentence');
  state.extensions['fixture.bass']={offset_db:-1.5,status:'waiting',error:'WAITING_FOR_SUPPORTED_DSP_MODE'};
  await page.waitForFunction(()=>document.querySelector('#bass-offset-state').textContent.includes('sound mode'));
  check((await page.locator('#bass-offset-state').textContent()).endsWith('Not supported in this sound mode or on this input.'),'unsupported route explained');
  state.extensions['fixture.bass']={offset_db:0,status:'paused',error:'Readback failed'};
  state.setup={calibration:'Measuring'};
  await page.waitForFunction(()=>document.querySelector('#bass-offset').disabled);
  check(await page.locator('#bass-offset-save').isDisabled(),'calibration blocks saving and editing');
  delete state.setup;
  await page.waitForFunction(()=>!document.querySelector('#bass-offset').disabled);
  failure=true;await page.locator('#bass-offset').selectOption('-1.5');await page.locator('#bass-offset-save').click();
  await page.waitForFunction(()=>document.querySelector('#message-text').textContent==='Unable to save offset');
  check(await page.locator('#bass-offset').inputValue()==='-1.5','failed save retains draft for correction or retry');failure=false;
  offline=true;
  await page.waitForFunction(()=>document.querySelector('#connection-label').textContent==='Offline');
  check(await page.locator('#bass-offset-save').isDisabled(),'offline save blocked');
  offline=false;await page.locator('#refresh').click();
  await page.waitForFunction(()=>!document.querySelector('#bass-offset').disabled);
  check(calls.length===3,'disconnect does not replay failed save');
  state.power='On';state.extensions['fixture.bass']={offset_db:-3,status:'waiting'};
  await page.waitForFunction(()=>document.querySelector('#bass-offset').value==='-3');
  await mkdir(new URL('build/',root),{recursive:true});
  for (const width of [320,390,1200]) {
    await page.setViewportSize({width,height:900});
    check(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'no overflow at '+width+'px');
    const select=await page.locator('#bass-offset').boundingBox(),save=await page.locator('#bass-offset-save').boundingBox();
    check(Math.abs(select.width-save.width)<1 && Math.abs(select.height-save.height)<1 && select.height>=44 && Math.abs(select.y-save.y)<1,'matching accessible control sizes at '+width+'px');
    await page.screenshot({path:fileURLToPath(new URL('build/bass-offset-'+width+'.png',root)),fullPage:true});
  }
  await page.locator('#bass-offset').focus();
  check(await page.locator('#bass-offset').evaluate(el=>getComputedStyle(el).outlineStyle!=='none'),'keyboard focus is visible');
  delete state.extensions['fixture.bass'];
  await page.waitForFunction(()=>document.querySelector('#bass-offset').disabled);
  check((await page.locator('#bass-offset-state').textContent())==='Unavailable','missing state disables controls');
  await page.close();
  feature.included=false;
  const excluded=await browser.newPage();await excluded.goto(url);
  await excluded.waitForFunction(()=>document.querySelector('#connection-label').textContent==='Connected');
  check(await excluded.locator('[data-component=bass-offset]').isHidden(),'excluded component stays hidden');await excluded.close();
  discovery=false;
  const legacy=await browser.newPage();await legacy.goto(url);
  await legacy.waitForFunction(()=>document.querySelector('#connection-label').textContent==='Connected');
  check(await legacy.locator('[data-component=bass-offset]').isHidden(),'older servers do not expose an unsupported bass action');await legacy.close();
  check(errors.length===0,'no browser exceptions: '+errors.join(', '));
  console.log(checks+' bass-offset browser checks passed');
} finally {
  await browser.close();clearInterval(timer);for(const res of streams)res.destroy();server.close();
}

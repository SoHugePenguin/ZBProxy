'use strict';
const $ = id => document.getElementById(id);
let state = {enabled:false,total:0,page:1,pages:1}, busy = false, loaded = false, sequence = 0, searchTimer, playerView = 'whitelist', whitelistPicker;
function notice(message, error = false) { const el=$('notice'); el.textContent=message; el.className=error?'notice error':'notice'; el.hidden=!message; }
async function api(path, method='GET', body) {
  const options={method,headers:{'X-ZBProxy-Admin':'1'}};
  if(body!==undefined){options.headers['Content-Type']='application/json';options.body=JSON.stringify(body);}
  const response=await fetch(path,options);
  let data; try{data=await response.json();}catch{throw new Error(response.status===401?'登录已失效，请刷新页面重新登录。':'服务器暂时无法响应，请稍后重试。');}
  if(!response.ok)throw new Error(data.error||'请求失败，请稍后重试。');
  return data;
}
// A paged picker keeps selections across searches without loading the whole registry.
function createPlayerPicker(parent, {selected=[], assigned=new Map(), whitelist=false, onChange=()=>{}}={}) {
  const selection=new Map(selected.map(name=>[name.toLowerCase(),name]));
  const root=document.createElement('div');root.className='player-picker';
  const search=document.createElement('input');search.type='search';search.name='player-search';search.placeholder='搜索已收录玩家…';search.setAttribute('aria-label','搜索已收录玩家');search.maxLength=16;
  const summary=document.createElement('div');summary.className='picker-summary';summary.setAttribute('role','status');
  const chips=document.createElement('div');chips.className='picker-chips';
  const toolbar=document.createElement('div');toolbar.className='picker-toolbar';
  const selectPage=document.createElement('button');selectPage.type='button';selectPage.className='button secondary compact';selectPage.textContent='选择本页可用玩家';
  const list=document.createElement('div');list.className='picker-list';list.setAttribute('role','group');list.setAttribute('aria-label','已收录玩家');
  const pager=document.createElement('div');pager.className='picker-toolbar';
  const previous=document.createElement('button'),next=document.createElement('button'),pageText=document.createElement('span');
  for(const [button,text] of [[previous,'上一页'],[next,'下一页']]){button.type='button';button.className='button secondary compact';button.textContent=text;}
  pager.append(previous,pageText,next);toolbar.append(selectPage);root.append(search,summary,chips,toolbar,list,pager);parent.replaceChildren(root);
  let page=1,pages=1,request=0,timer,available=[];
  const changed=()=>{
    summary.textContent=`已选择 ${selection.size} 位玩家`;
    chips.replaceChildren();
    for(const [key,name] of selection){const chip=document.createElement('button');chip.type='button';chip.className='picker-chip';chip.textContent=`${name} ×`;chip.setAttribute('aria-label',`取消选择 ${name}`);chip.addEventListener('click',()=>{selection.delete(key);changed();sync();});chips.append(chip);}
    onChange([...selection.values()]);
  };
  const sync=()=>list.querySelectorAll('input').forEach(input=>{input.checked=selection.has(input.value.toLowerCase());});
  async function loadPage(target=1){
    const current=++request;previous.disabled=next.disabled=selectPage.disabled=true;list.textContent='正在读取玩家…';available=[];
    try{
      const data=await api(`/api/players?page=${target}&q=${encodeURIComponent(search.value.trim())}`);if(current!==request)return;
      page=data.page;pages=data.pages;pageText.textContent=`${page} / ${pages}`;list.replaceChildren();
      const members=new Set(data.whitelisted.map(name=>name.toLowerCase()));
      for(const name of data.players){
        const key=name.toLowerCase(),reason=whitelist&&members.has(key)?'已在白名单':assigned.get(key);
        const label=document.createElement('label');label.className='picker-option';
        const input=document.createElement('input');input.type='checkbox';input.name='selected-players';input.value=name;input.disabled=!!reason;input.checked=selection.has(key);
        const text=document.createElement('span');text.textContent=name;label.append(input,text);
        if(reason){const note=document.createElement('small');note.textContent=reason;label.append(note);}else available.push(name);
        input.addEventListener('change',()=>{if(input.checked)selection.set(key,name);else selection.delete(key);changed();});list.append(label);
      }
      if(!data.players.length)list.textContent=search.value?'没有匹配的已收录玩家。':'暂无已收录玩家，请先到“玩家收录”添加。';
      previous.disabled=page<=1;next.disabled=page>=pages;selectPage.disabled=!available.length;
    }catch(error){if(current!==request)return;list.textContent=error.message;const retry=document.createElement('button');retry.type='button';retry.className='button secondary compact';retry.textContent='重试';retry.addEventListener('click',()=>loadPage(target));list.append(retry);}
  }
  search.addEventListener('input',()=>{clearTimeout(timer);timer=setTimeout(()=>loadPage(1),250);});
  search.addEventListener('keydown',event=>{if(event.key==='Enter'){event.preventDefault();clearTimeout(timer);loadPage(1);}});
  previous.addEventListener('click',()=>loadPage(page-1));next.addEventListener('click',()=>loadPage(page+1));
  selectPage.addEventListener('click',()=>{for(const name of available)selection.set(name.toLowerCase(),name);changed();sync();});
  changed();loadPage();
  return {values:()=>[...selection.values()]};
}
function controls() {
  $('toggle').disabled=busy||!loaded;
  $('add').disabled=busy;
  $('whitelist-add').disabled=busy||!whitelistPicker?.values().length;
  $('refresh').disabled=busy;
  $('previous').disabled=busy||!loaded||state.page<=1;
  $('next').disabled=busy||!loaded||state.page>=state.pages;
  document.querySelectorAll('.remove').forEach(button=>button.disabled=busy);
}
function empty(message) {const tr=document.createElement('tr'),td=document.createElement('td');td.colSpan=3;td.className='empty';td.textContent=message;tr.append(td);$('players').replaceChildren(tr);}
function render(data) {
  loaded=true;state=data;
  $('count').textContent=data.total.toLocaleString();
  $('mode').textContent=data.enabled?'已启用':'未启用';
  $('mode').className=data.enabled?'badge active':'badge';
  $('toggle').setAttribute('aria-checked',String(data.enabled));
  $('toggle').setAttribute('aria-label',data.enabled?'关闭白名单保护':'启用白名单保护');
  $('policy-description').textContent=data.enabled?(data.total?'仅允许白名单内玩家通过代理登录。':'当前白名单为空，所有新登录都会被拒绝。'):'当前不额外限制玩家名，可从已收录玩家中添加白名单后开启保护。';
  $('page-label').textContent=`${data.page} / ${data.pages}`;
  $('result-count').textContent=$('search').value.trim()?`找到 ${data.matched.toLocaleString()} 位玩家`:`共 ${data.total.toLocaleString()} 位玩家 · 每页 50 人`;
  const fragment=document.createDocumentFragment(),members=new Set(data.whitelisted.map(name=>name.toLowerCase()));
  for(const name of data.players){
    const tr=document.createElement('tr'),nameCell=document.createElement('td'),nameWrap=document.createElement('div'),avatar=document.createElement('span'),text=document.createElement('span'),statusCell=document.createElement('td'),status=document.createElement('span'),actionCell=document.createElement('td'),remove=document.createElement('button');
    nameWrap.className='player-name';avatar.className='avatar';avatar.setAttribute('aria-hidden','true');avatar.textContent=name.slice(0,2).toUpperCase();text.textContent=name;nameWrap.append(avatar,text);nameCell.append(nameWrap);
    statusCell.className='permission-cell';status.className='member-status';status.textContent=members.has(name.toLowerCase())?'白名单成员':'仅收录';statusCell.append(status);
    actionCell.className='action-cell';remove.className='remove';remove.type='button';remove.textContent='移除';remove.setAttribute('aria-label',`移除 ${name}`);remove.addEventListener('click',()=>removePlayer(name));actionCell.append(remove);tr.append(nameCell,statusCell,actionCell);fragment.append(tr);
  }
  $('players').replaceChildren(fragment);
  if(!data.players.length)empty($('search').value.trim()?'没有找到匹配的玩家。':playerView==='players'?'还没有收录玩家，可在右侧批量添加。':'白名单为空，请从已收录玩家中选择添加。');
  controls();
}
async function load(page=state.page) {
  const current=++sequence,view=playerView;
  $('refresh').disabled=true;
  try{const data=await api(`/api/${view==='players'?'players':'whitelist'}?page=${page}&q=${encodeURIComponent($('search').value.trim())}`);if(current===sequence)render(data);}
  catch(error){if(current===sequence){loaded=false;notice(error.message,true);empty('读取失败，请点击刷新重试。');}}
  finally{if(current===sequence)controls();}
}
function loadWhitelistPicker(){whitelistPicker=createPlayerPicker($('whitelist-picker'),{whitelist:true,onChange:()=>queueMicrotask(controls)});controls();}
function showPlayerView(view){
  playerView=view;loaded=false;$('search').value='';clearTimeout(searchTimer);
  $('policy-card').hidden=view!=='whitelist';$('registration-card').hidden=view!=='players';$('whitelist-add-card').hidden=view!=='whitelist';
  $('members-title').textContent=view==='players'?'已收录玩家':'白名单玩家';
  load(1);if(view==='whitelist')loadWhitelistPicker();
}
function confirmAction(title,description,label='确认') {
  const dialog=$('confirm-dialog');$('dialog-title').textContent=title;$('dialog-description').textContent=description;$('confirm-button').textContent=label;dialog.returnValue='';
  return new Promise(resolve=>{dialog.addEventListener('close',()=>resolve(dialog.returnValue==='confirm'),{once:true});dialog.showModal();});
}
async function change(path,method,body,message) {
  if(busy)return;
  busy=true;controls();notice('');
  try{const result=await api(path,method,body);notice(typeof message==='function'?message(result):message);await load();if(playerView==='whitelist')loadWhitelistPicker();return true;}
  catch(error){notice(error.message,true);return false;}
  finally{busy=false;controls();}
}
async function removePlayer(name) {
  if(busy)return;
  const registry=playerView==='players';
  if(!await confirmAction(registry?'移除收录玩家':'移出白名单',registry?`确定移除 ${name} 的收录？其白名单资格也会移除，已有分流仍保留占用，请在玩家分流中调整。`:`确定将 ${name} 移出白名单？收录和分流保留，已连接会话不受影响。`,'确认移除'))return;
  await change(registry?'/api/players':'/api/whitelist','DELETE',{names:[name]},`已移除 ${name}。`);
}
$('add-form').addEventListener('submit',async event=>{
  event.preventDefault();
  const names=[...new Set($('names').value.trim().split(/[\s,，;；]+/).filter(Boolean))];
  if(!names.length){notice('请输入至少一个玩家名。',true);return;}
  const invalid=names.find(name=>!/^[a-zA-Z0-9_]{1,16}$/.test(name));
  if(invalid){notice(`玩家名“${invalid}”无效，请使用 1–16 位英文字母、数字或下划线。`,true);$('names').focus();return;}
  if(await change('/api/players','POST',{names},result=>result.changed?`已收录 ${result.changed} 位玩家，重复名字已跳过。`:'这些玩家已收录，无需重复添加。'))$('names').value='';
});
$('whitelist-add-form').addEventListener('submit',async event=>{event.preventDefault();const names=whitelistPicker?.values()||[];if(!names.length)return;await change('/api/whitelist','POST',{names},result=>`已添加 ${result.changed} 位白名单玩家。`);});
$('toggle').addEventListener('click',async()=>{
  if(busy)return;
  const enabled=!state.enabled;
  const description=enabled?(state.total?'开启后，仅白名单内玩家可以通过 Minecraft 代理登录。':'当前白名单为空。开启后，所有新的 Minecraft 登录都会被拒绝。建议先添加自己的玩家名。'):'关闭后，将不再应用此页面的玩家名限制。原有配置中的访问限制仍然生效。';
  if(!await confirmAction(enabled?'启用白名单保护':'关闭白名单保护',description,enabled?'确认启用':'确认关闭'))return;
  await change('/api/whitelist','PUT',{enabled},enabled?'白名单保护已启用。':'白名单保护已关闭。');
});
$('refresh').addEventListener('click',()=>{notice('');if(!$('whitelist-panel').hidden){load();if(playerView==='whitelist')loadWhitelistPicker();}});
$('previous').addEventListener('click',()=>load(state.page-1));
$('next').addEventListener('click',()=>load(state.page+1));
$('search-form').addEventListener('submit',event=>{event.preventDefault();clearTimeout(searchTimer);load(1);});
$('search').addEventListener('input',()=>{clearTimeout(searchTimer);searchTimer=setTimeout(()=>load(1),250);});
$('names').setAttribute('aria-describedby','names-help');

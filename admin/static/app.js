'use strict';
const $ = id => document.getElementById(id);
let state = {enabled:false,total:0,page:1,pages:1}, busy = false, loaded = false, sequence = 0, searchTimer;
function notice(message, error = false) { const el=$('notice'); el.textContent=message; el.className=error?'notice error':'notice'; el.hidden=!message; }
async function api(path, method='GET', body) {
  const options={method,headers:{'X-ZBProxy-Admin':'1'}};
  if(body!==undefined){options.headers['Content-Type']='application/json';options.body=JSON.stringify(body);}
  const response=await fetch(path,options);
  let data; try{data=await response.json();}catch{throw new Error(response.status===401?'登录已失效，请刷新页面重新登录。':'服务器暂时无法响应，请稍后重试。');}
  if(!response.ok)throw new Error(data.error||'请求失败，请稍后重试。');
  return data;
}
function controls() {
  $('toggle').disabled=busy||!loaded;
  $('add').disabled=busy;
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
  $('policy-description').textContent=data.enabled?(data.total?'仅允许名单内玩家通过代理登录。':'当前名单为空，所有新登录都会被拒绝。'):'当前不额外限制玩家名，添加名单后可开启保护。';
  $('page-label').textContent=`${data.page} / ${data.pages}`;
  $('result-count').textContent=$('search').value.trim()?`找到 ${data.matched.toLocaleString()} 位玩家`:`共 ${data.total.toLocaleString()} 位玩家 · 每页 50 人`;
  const fragment=document.createDocumentFragment();
  for(const name of data.players){
    const tr=document.createElement('tr'),nameCell=document.createElement('td'),nameWrap=document.createElement('div'),avatar=document.createElement('span'),text=document.createElement('span'),statusCell=document.createElement('td'),status=document.createElement('span'),actionCell=document.createElement('td'),remove=document.createElement('button');
    nameWrap.className='player-name';avatar.className='avatar';avatar.setAttribute('aria-hidden','true');avatar.textContent=name.slice(0,2).toUpperCase();text.textContent=name;nameWrap.append(avatar,text);nameCell.append(nameWrap);
    statusCell.className='permission-cell';status.className='member-status';status.textContent='已收录';statusCell.append(status);
    actionCell.className='action-cell';remove.className='remove';remove.type='button';remove.textContent='移除';remove.setAttribute('aria-label',`移除 ${name}`);remove.addEventListener('click',()=>removePlayer(name));actionCell.append(remove);tr.append(nameCell,statusCell,actionCell);fragment.append(tr);
  }
  $('players').replaceChildren(fragment);
  if(!data.players.length)empty($('search').value.trim()?'没有找到匹配的玩家。试试其他名字。':'还没有添加玩家。可在“添加玩家”中粘贴名单。');
  controls();
}
async function load(page=state.page) {
  const current=++sequence;
  $('refresh').disabled=true;
  try{const data=await api(`/api/whitelist?page=${page}&q=${encodeURIComponent($('search').value.trim())}`);if(current===sequence)render(data);}
  catch(error){if(current===sequence){loaded=false;notice(error.message,true);empty('读取失败，请点击“刷新列表”重试。');}}
  finally{if(current===sequence)controls();}
}
function confirmAction(title,description,label='确认') {
  const dialog=$('confirm-dialog');$('dialog-title').textContent=title;$('dialog-description').textContent=description;$('confirm-button').textContent=label;dialog.returnValue='';
  return new Promise(resolve=>{dialog.addEventListener('close',()=>resolve(dialog.returnValue==='confirm'),{once:true});dialog.showModal();});
}
async function change(path,method,body,message) {
  if(busy)return;
  busy=true;controls();notice('');
  try{const result=await api(path,method,body);notice(typeof message==='function'?message(result):message);await load();return true;}
  catch(error){notice(error.message,true);return false;}
  finally{busy=false;controls();}
}
async function removePlayer(name) {
  if(busy)return;
  if(!await confirmAction('移除玩家',`确定将 ${name} 移出白名单？已连接的会话不受影响。`,'确认移除'))return;
  await change('/api/players','DELETE',{names:[name]},`已移除 ${name}。`);
}
$('add-form').addEventListener('submit',async event=>{
  event.preventDefault();
  const names=[...new Set($('names').value.trim().split(/[\s,，;；]+/).filter(Boolean))];
  if(!names.length){notice('请输入至少一个玩家名。',true);return;}
  const invalid=names.find(name=>!/^[a-zA-Z0-9_]{1,16}$/.test(name));
  if(invalid){notice(`玩家名“${invalid}”无效，请使用 1–16 位英文字母、数字或下划线。`,true);$('names').focus();return;}
  if(await change('/api/players','POST',{names},result=>result.changed?`已添加 ${result.changed} 位玩家，重复名字已跳过。`:'这些玩家已在名单中，无需重复添加。'))$('names').value='';
});
$('toggle').addEventListener('click',async()=>{
  if(busy)return;
  const enabled=!state.enabled;
  const description=enabled?(state.total?'开启后，仅名单内玩家可以通过 Minecraft 代理登录。':'当前名单为空。开启后，所有新的 Minecraft 登录都会被拒绝。建议先添加自己的玩家名。'):'关闭后，将不再应用此页面的玩家名限制。原有配置中的访问限制仍然生效。';
  if(!await confirmAction(enabled?'启用白名单保护':'关闭白名单保护',description,enabled?'确认启用':'确认关闭'))return;
  await change('/api/whitelist','PUT',{enabled},enabled?'白名单保护已启用。':'白名单保护已关闭。');
});
$('refresh').addEventListener('click',()=>{notice('');if(!$('whitelist-panel').hidden)load();});
$('previous').addEventListener('click',()=>load(state.page-1));
$('next').addEventListener('click',()=>load(state.page+1));
$('search-form').addEventListener('submit',event=>{event.preventDefault();clearTimeout(searchTimer);load(1);});
$('search').addEventListener('input',()=>{clearTimeout(searchTimer);searchTimer=setTimeout(()=>load(1),250);});
$('names').setAttribute('aria-describedby','names-help');
load(1);

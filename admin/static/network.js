'use strict';
let networkState, networkView = 'outbounds', savingNetwork = false, testingAll = false;
const runningProbes = new Set(), probeResults = new Map();
const clone = value => JSON.parse(JSON.stringify(value));
const icon = name => { const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg'); svg.classList.add('icon'); svg.setAttribute('aria-hidden', 'true'); const use = document.createElementNS(svg.namespaceURI, 'use'); use.setAttribute('href', `/icons.svg#${name}`); svg.append(use); return svg; };
function action(name, title, callback, danger = false) { const b = document.createElement('button'); b.type = 'button'; b.className = `icon-button${danger ? ' danger' : ''}`; b.title = title; b.setAttribute('aria-label', title); b.append(icon(name)); b.addEventListener('click', callback); return b; }
function cell(text, className = '') { const td = document.createElement('td'); td.textContent = text; td.className = className; return td; }
function hostPort(address, port) { return `${address.includes(':') ? `[${address}]` : address}:${port}`; }
function splitAddress(value) { const match = /^(?:\[([^\]]+)\]|([^:]+)):(\d+)$/.exec(value || ''); return match ? [match[1] || match[2], match[3]] : ['', '']; }
const proxyName = value => ({'':'直连', direct:'直连', socks:'SOCKS5', socks5:'SOCKS5', socks4:'SOCKS4', socks4a:'SOCKS4a', http:'HTTP CONNECT'})[value || ''] || value;
function setView(view) {
  networkView = view;
  document.querySelectorAll('[data-view]').forEach(b => { if (b.dataset.view === view) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current'); });
  $('whitelist-panel').hidden = view !== 'whitelist'; $('network-panel').hidden = view === 'whitelist';
  if (view !== 'whitelist') renderNetwork();
}
async function loadNetwork() {
  $('network-load-error').hidden = true;
  try { const next = await api('/api/network'); if (networkState && networkState.revision !== next.revision) { probeResults.clear(); renderProbes(); } networkState = next; renderNetwork(); }
  catch (error) { $('network-load-error').textContent = error.message; $('network-load-error').hidden = false; $('network-summary').textContent = '读取失败'; }
}
function renderNetwork() {
  const labels = {outbounds:['出口线路','添加出口'], services:['入口端口','添加入口'], routes:['玩家分流','添加规则']};
  if (networkView === 'whitelist') return;
  $('network-title').textContent = labels[networkView][0]; $('network-add-label').textContent = labels[networkView][1];
  $('probe-all').hidden = $('probe-mode-label').hidden = networkView !== 'outbounds';
  $('network-add').disabled = !networkState || savingNetwork; $('probe-all').disabled = !networkState || testingAll;
  if (!networkState) return;
  $('outbound-count').textContent = networkState.outbounds.length; $('revision-label').textContent = `配置版本 ${networkState.revision}`;
  const heads = {outbounds:['名称','目标服务器','代理','认证','最近测试','操作'], services:['名称','监听地址','默认出口','传输','操作'], routes:['玩家','匹配方式','入口','指定出口','状态','操作']};
  const tr = document.createElement('tr'); for (const h of heads[networkView]) { const th = document.createElement('th'); th.textContent = h; tr.append(th); } $('network-head').replaceChildren(tr);
  const items = networkView === 'routes' ? networkState.player_routes : networkState[networkView];
  const fragment = document.createDocumentFragment();
  items.forEach((item, index) => {
    const row = document.createElement('tr');
    if (networkView === 'outbounds') {
      const p = item.ProxyOptions || {}, result = probeResults.get(item.Name);
      row.append(cell(item.Name, 'strong'), cell(hostPort(item.TargetAddress || '', item.TargetPort), 'mono'), cell(`${proxyName(p.Type)}${p.Address ? ` · ${p.Address}` : ''}`), cell(p.Username || '无'), cell(runningProbes.has(item.Name) ? '测试中…' : result ? result.AverageLabel : '未测试'));
    } else if (networkView === 'services') row.append(cell(item.Name, 'strong'), cell(hostPort(item.Bind || '0.0.0.0', item.Listen), 'mono'), cell(item.DefaultOutbound || '沿用原有路由'), cell(item.Encryption?.Enabled ? 'Trojan / TLS' : 'TCP'));
    else row.append(cell(item.Value, item.Kind === 'uuid' ? 'mono uuid' : 'strong'), cell(item.Kind === 'uuid' ? 'UUID' : '玩家名'), cell(item.Service || '全部入口'), cell(item.Outbound), cell(item.Enabled ? '已启用' : '已停用', item.Enabled ? 'success-text' : 'muted'));
    const actions = document.createElement('td'); actions.className = 'row-actions';
    if (networkView === 'outbounds') { const b = action('activity', `测试 ${item.Name}`, () => probe(item.Name)); b.disabled = runningProbes.has(item.Name); actions.append(b); }
    const edit = action('pencil', `编辑 ${item.Name || item.Value}`, () => openEditor(networkView, index)); edit.disabled = savingNetwork;
    const remove = action('trash', `删除 ${item.Name || item.Value}`, () => removeNetworkItem(networkView, index), true); remove.disabled = savingNetwork;
    actions.append(edit, remove); row.append(actions); [...row.children].forEach((td, i) => { td.dataset.label = heads[networkView][i]; }); fragment.append(row);
  });
  if (!items.length) { const row = document.createElement('tr'); const td = cell(`暂无${labels[networkView][0]}`, 'empty'); td.colSpan = heads[networkView].length; row.append(td); fragment.append(row); }
  $('network-rows').replaceChildren(fragment); $('network-summary').textContent = `共 ${items.length} ${networkView === 'routes' ? '条规则' : '个' + (networkView === 'services' ? '入口' : '出口')}`;
}
async function saveNetwork(next) {
  if (savingNetwork) throw new Error('配置正在保存，请稍候');
  savingNetwork = true; renderNetwork();
  try { networkState = await api('/api/network', 'PUT', next); probeResults.clear(); renderProbes(); notice('配置已保存并生效。'); renderNetwork(); }
  finally { savingNetwork = false; renderNetwork(); }
}
async function removeNetworkItem(kind, index) {
  const key = kind === 'routes' ? 'player_routes' : kind, item = networkState[key][index];
  if (!await confirmAction('删除配置', `确定删除 ${item.Name || item.Value}？`, '删除')) return;
  const next = clone(networkState); next[key].splice(index, 1);
  try { await saveNetwork(next); } catch (error) { notice(error.message, true); }
}
function field(parent, label, id, type = 'text', value = '', options = {}) {
  const wrap = document.createElement('div'); wrap.className = 'form-field';
  const title = document.createElement('label'); title.htmlFor = id; title.textContent = label;
  const input = document.createElement(type === 'select' ? 'select' : type === 'textarea' ? 'textarea' : 'input'); input.id = id; input.name = id;
  if (input.tagName === 'INPUT') input.type = type;
  if (type === 'select') for (const [v, text] of options.choices || []) { const option = document.createElement('option'); option.value = v; option.textContent = text; input.append(option); }
  if (type === 'checkbox') { input.checked = !!value; wrap.classList.add('check-field'); } else input.value = value ?? '';
  for (const key of ['required','min','max','maxlength','placeholder','autocomplete','pattern']) if (options[key] !== undefined) input.setAttribute(key, options[key]);
  if (type === 'checkbox') wrap.append(input, title); else wrap.append(title, input);
  parent.append(wrap); return input;
}
function group(parent, title) { const section = document.createElement('fieldset'); const legend = document.createElement('legend'); legend.textContent = title; section.append(legend); parent.append(section); return section; }
function openEditor(kind, index = -1) {
  if (!networkState || savingNetwork) return;
  const key = kind === 'routes' ? 'player_routes' : kind, original = index < 0 ? null : networkState[key][index];
  $('editor-title').textContent = `${original ? '编辑' : '添加'}${kind === 'outbounds' ? '出口' : kind === 'services' ? '入口' : '分流规则'}`;
  const parent = $('editor-fields'); parent.replaceChildren(); $('editor-error').hidden = true;
  const choices = networkState.outbounds.map(out => [out.Name, out.Name]).concat([['RESET','拒绝连接 (RESET)'],['REJECT','拒绝连接 (REJECT)']]);
  if (kind === 'outbounds') {
    const item = original || {TargetAddress:'mc.hypixel.net', TargetPort:25565}, p = item.ProxyOptions || {};
    field(parent, '出口名称', 'f-name', 'text', item.Name, {required:true, maxlength:128}); $('f-name').readOnly = !!original;
    const target = group(parent, '目标服务器'); field(target, '服务器地址', 'f-target', 'text', item.TargetAddress, {required:true}); field(target, '端口', 'f-port', 'number', item.TargetPort, {required:true,min:1,max:65535});
    const proxyType = field(parent, '连接方式', 'f-proxy-type', 'select', p.Type || 'direct', {choices:[['direct','直连'],['socks5','SOCKS5'],['http','HTTP CONNECT'],['socks4','SOCKS4'],['socks4a','SOCKS4a']].concat(p.Type === 'socks' ? [['socks','SOCKS5（原配置）']] : [])});
    const proxy = group(parent, '上游代理'), [host, port] = splitAddress(p.Address);
    field(proxy, '代理地址', 'f-proxy-host', 'text', host); field(proxy, '代理端口', 'f-proxy-port', 'number', port, {min:1,max:65535});
    field(proxy, '用户名', 'f-user', 'text', p.Username, {autocomplete:'off'}); field(proxy, networkState.proxy_passwords?.[item.Name] ? '密码（已设置，留空保留）' : '密码', 'f-password', 'password', '', {autocomplete:'new-password'});
    field(proxy, '网络', 'f-network', 'select', p.Network || 'tcp', {choices:[['tcp','自动'],['tcp4','IPv4'],['tcp6','IPv6']]}); field(proxy, '清除已保存密码', 'f-clear-password', 'checkbox', false);
    const update = () => { proxy.hidden = proxyType.value === 'direct'; $('f-proxy-host').required = $('f-proxy-port').required = !proxy.hidden; }; proxyType.addEventListener('change', update); update();
    const mc = group(parent, 'Minecraft'); field(mc, 'MOTD 描述', 'f-motd', 'text', item.Minecraft?.MotdDescription || ''); field(mc, '握手服务器名', 'f-rewrite', 'text', item.Minecraft?.RewrittenHostname || '', {placeholder:'默认使用目标服务器地址'});
  } else if (kind === 'services') {
    const item = original || {Bind:'0.0.0.0',Listen:25565}, enc = item.Encryption || {};
    field(parent, '入口名称', 'f-name', 'text', item.Name, {required:true,maxlength:128}); $('f-name').readOnly = !!original;
    const listen = group(parent, '监听'); field(listen, '监听 IP', 'f-bind', 'text', item.Bind || '0.0.0.0', {required:true}); field(listen, '端口', 'f-port', 'number', item.Listen, {required:true,min:1,max:65535});
    field(parent, '默认出口', 'f-default', 'select', original ? (item.DefaultOutbound || '') : choices[0]?.[0], {choices:[['','沿用原有路由'],...choices]});
    const encrypted = field(parent, '启用 Trojan / TLS', 'f-encrypted', 'checkbox', enc.Enabled);
    const tls = group(parent, 'TLS 配置'); field(tls, '证书文件', 'f-cert', 'text', enc.CertFile); field(tls, '私钥文件', 'f-key', 'text', enc.KeyFile); field(tls, networkState.encryption_passwords?.[item.Name] ? '连接密码（已设置，留空保留）' : '连接密码', 'f-tls-password', 'password', '', {autocomplete:'new-password'});
    const update = () => { tls.hidden = !encrypted.checked; $('f-cert').required = $('f-key').required = encrypted.checked; }; encrypted.addEventListener('change', update); update();
  } else {
    const item = original || {Enabled:true,Kind:'name',Service:''};
    const type = field(parent, '匹配方式', 'f-kind', 'select', item.Kind, {choices:[['name','玩家名'],['uuid','UUID']]});
    field(parent, '玩家名 / UUID', 'f-value', 'text', item.Value, {required:true,autocomplete:'off'});
    const hint = document.createElement('p'); hint.className = 'field-help'; hint.textContent = '旧版客户端（如 1.8.9）请选择玩家名。UUID 匹配仅适用于携带 UUID 的客户端。'; parent.append(hint);
    type.addEventListener('change', () => { $('f-value').maxLength = type.value === 'name' ? 16 : 36; });
    field(parent, '入口', 'f-service', 'select', item.Service, {choices:[['','全部入口'],...networkState.services.map(inbound => [inbound.Name,`${inbound.Name} · ${inbound.Listen}`])]});
    field(parent, '指定出口', 'f-outbound', 'select', item.Outbound || choices[0]?.[0], {choices,required:true}); field(parent, '启用规则', 'f-enabled', 'checkbox', item.Enabled);
  }
  $('editor-form').onsubmit = async event => {
    event.preventDefault(); if (savingNetwork) return;
    const next = clone(networkState), value = id => $(id).value.trim();
    let item = original ? clone(original) : {};
    if (kind === 'outbounds') {
      item.Name = value('f-name'); item.TargetAddress = value('f-target'); item.TargetPort = Number(value('f-port'));
      item.ProxyOptions = {Type:value('f-proxy-type'), Network:value('f-network'), Address: value('f-proxy-type') === 'direct' ? '' : hostPort(value('f-proxy-host'),value('f-proxy-port')), Username:value('f-proxy-type') === 'direct' ? '' : value('f-user'), Password:$('f-password').value};
      if ($('f-clear-password').checked) next.clear_proxy_passwords = [item.Name];
      item.Minecraft = {...(item.Minecraft || {OnlineCount:{Max:20,Online:-1,EnableMaxLimit:false}}), MotdDescription:value('f-motd'), EnableHostnameRewrite:true, RewrittenHostname:value('f-rewrite')};
    } else if (kind === 'services') {
      item.Name = value('f-name'); item.Bind = value('f-bind'); item.Listen = Number(value('f-port')); item.DefaultOutbound = value('f-default');
      if (original?.Encryption || $('f-encrypted').checked || value('f-cert') || value('f-key') || $('f-tls-password').value) item.Encryption = {Enabled:$('f-encrypted').checked,CertFile:value('f-cert'),KeyFile:value('f-key'),Password:$('f-tls-password').value};
    } else item = {Kind:value('f-kind'),Value:value('f-value'),Service:value('f-service'),Outbound:value('f-outbound'),Enabled:$('f-enabled').checked};
    if (index < 0) next[key].push(item); else next[key][index] = item;
    $('editor-save').disabled = true; $('editor-cancel').disabled = $('editor-close').disabled = true; $('editor-error').hidden = true;
    try { await saveNetwork(next); $('editor-dialog').close(); }
    catch (error) { $('editor-error').textContent = error.message; $('editor-error').hidden = false; }
    finally { $('editor-save').disabled = $('editor-cancel').disabled = $('editor-close').disabled = false; }
  };
  $('editor-dialog').showModal();
}
function renderProbes() {
  $('probe-results').hidden = probeResults.size === 0; const fragment = document.createDocumentFragment();
  for (const result of probeResults.values()) {
    const row = document.createElement('tr');
    row.append(cell(result.name), cell(result.mode === 'tcp' ? 'TCP' : 'Minecraft Ping'), cell(`${result.successful} / ${result.samples.length}`, result.successful ? 'success-text' : 'danger-text'), cell(result.AverageLabel), cell(result.samples.map(s => s.error || `${(result.mode === 'tcp' ? s.connect_ms : s.ping_ms).toFixed(1)} ms`).join(' · '), 'probe-detail'), cell(new Date(result.tested_at).toLocaleTimeString())); fragment.append(row);
  }
  $('probe-rows').replaceChildren(fragment);
}
async function probe(name, mode = $('probe-mode').value) {
  if (runningProbes.has(name)) return;
  const revision = networkState.revision;
  runningProbes.add(name); renderNetwork(); $('network-status').textContent = `正在测试 ${name}…`;
  try { const result = await api('/api/probe','POST',{name,mode}); if (networkState.revision !== revision) { notice('测试期间配置已变化，请重新测试。'); return; } result.AverageLabel = result.average_ms == null ? '失败' : `${result.average_ms.toFixed(1)} ms`; probeResults.set(name,result); renderProbes(); }
  catch (error) { notice(`${name}：${error.message}`,true); }
  finally { runningProbes.delete(name); $('network-status').textContent = runningProbes.size ? '线路测试中…' : '测试完成'; renderNetwork(); }
}
$('network-add').addEventListener('click', () => openEditor(networkView));
document.querySelectorAll('[data-view]').forEach(b => b.addEventListener('click', () => setView(b.dataset.view)));
$('editor-close').addEventListener('click', () => $('editor-dialog').close()); $('editor-cancel').addEventListener('click', () => $('editor-dialog').close());
$('editor-dialog').addEventListener('cancel', event => { if (savingNetwork) event.preventDefault(); });
$('refresh').addEventListener('click', () => { if (networkView !== 'whitelist') loadNetwork(); });
$('probe-all').addEventListener('click', async () => { if (!networkState || testingAll) return; testingAll = true; renderNetwork(); const names = networkState.outbounds.map(out => out.Name), mode = $('probe-mode').value; try { for (const name of names) await probe(name, mode); } finally { testingAll = false; renderNetwork(); } });
$('clear-results').addEventListener('click', () => { probeResults.clear(); renderProbes(); renderNetwork(); });
loadNetwork();

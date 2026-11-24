'use strict';
let monitorMode = 'online', monitorPage = 1, monitorPages = 1, monitorSequence = 0, monitorTimer, monitorSearchTimer, monitorAbort;
const monitorVisible = () => !$('monitor-panel').hidden && !document.hidden;
function formatBytes(value) {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']; let unit = 0;
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
  return `${value.toLocaleString(undefined, {maximumFractionDigits:unit ? 1 : 0})} ${units[unit]}`;
}
function formatDuration(ms) {
  const seconds = Math.max(0, Math.floor(ms / 1000)), days = Math.floor(seconds / 86400), hours = Math.floor(seconds / 3600) % 24, minutes = Math.floor(seconds / 60) % 60;
  return `${days ? `${days}天 ` : ''}${hours ? `${hours}时 ` : ''}${minutes}分 ${seconds % 60}秒`;
}
function monitorCell(primary, secondary = '', title = '') {
  const td = cell('', 'monitor-value'), value = document.createElement('span'); value.textContent = primary; td.append(value);
  if (secondary) { const detail = document.createElement('small'); detail.textContent = secondary; td.append(detail); }
  if (title) td.title = title;
  return td;
}
function renderMonitor(data) {
  const s = data.summary;
  $('monitor-online').textContent = `${s.online_players} / ${s.active_sessions}`;
  $('monitor-traffic').textContent = `${formatBytes(s.upload_bytes)} / ${formatBytes(s.download_bytes)}`;
  $('monitor-duration').textContent = formatDuration(s.duration_ms);
  $('monitor-total').textContent = `${s.known_players} / ${s.connections}`;
  $('monitor-storage-error').hidden = !data.persistence_error; $('monitor-storage-error').textContent = data.persistence_error || '';
  const online = data.mode === 'online', labels = online ? ['玩家', '实际入口', '实际出口', '连接时间', '本次时长', '上传 / 下载'] : ['玩家', '最近入口', '最近出口', '最近活动', '累计时长', '累计上传 / 下载', '连接次数'];
  const head = document.createElement('tr'); labels.forEach(label => { const th = document.createElement('th'); th.scope = 'col'; th.textContent = label; head.append(th); }); $('monitor-head').replaceChildren(head);
  const fragment = document.createDocumentFragment(), items = online ? data.sessions : data.players;
  for (const item of items) {
    const row = document.createElement('tr'), time = new Date(online ? item.started_at : item.last_seen);
    row.append(monitorCell(item.name, online ? '转发中' : item.active_sessions ? `${item.active_sessions} 个连接在线` : '已离线', item.uuid ? `客户端 UUID：${item.uuid}` : ''), monitorCell(online ? item.service : item.last_service), monitorCell(online ? item.outbound : item.last_outbound, online ? item.target : ''), monitorCell(time.toLocaleTimeString(), time.toLocaleDateString()), monitorCell(formatDuration(item.duration_ms)), monitorCell(formatBytes(item.upload_bytes), formatBytes(item.download_bytes)));
    if (!online) row.append(cell(item.connections.toLocaleString()));
    [...row.children].forEach((td, i) => { td.dataset.label = labels[i]; }); fragment.append(row);
  }
  if (!items.length) { const row = document.createElement('tr'), td = cell($('monitor-search').value.trim() ? '没有匹配的记录' : online ? '当前没有玩家连接' : '暂无玩家连接记录', 'empty'); td.colSpan = labels.length; row.append(td); fragment.append(row); }
  $('monitor-rows').replaceChildren(fragment);
  monitorPage = data.page; monitorPages = data.pages;
  $('monitor-page').textContent = `${monitorPage} / ${monitorPages}`;
  $('monitor-result-count').textContent = `共 ${data.matched} ${online ? '个连接' : '位玩家'}${data.matched !== data.total ? ` / 总计 ${data.total}` : ''}`;
  $('monitor-previous').disabled = monitorPage <= 1; $('monitor-next').disabled = monitorPage >= monitorPages;
  $('monitor-updated').textContent = `更新于 ${new Date(data.generated_at).toLocaleTimeString()}`;
}
async function loadMonitor(page = monitorPage) {
  clearTimeout(monitorTimer);
  if (!monitorVisible()) return;
  if (monitorAbort) monitorAbort.abort();
  monitorAbort = new AbortController();
  const controller = monitorAbort, timeout = setTimeout(() => controller.abort(new Error('读取连接超时，请检查网络。')), 10000);
  const current = ++monitorSequence;
  $('monitor-updated').textContent = '更新中…';
  try {
    const query = new URLSearchParams({mode:monitorMode, page:String(page), q:$('monitor-search').value.trim()});
    const response = await fetch(`/api/sessions?${query}`, {signal:monitorAbort.signal});
    let data; try { data = await response.json(); } catch { throw new Error(response.status === 401 ? '登录已失效，请刷新页面重新登录。' : '服务器暂时无法响应。'); }
    if (!response.ok) throw new Error(data.error || '读取连接失败');
    if (current !== monitorSequence || !monitorVisible()) return;
    $('monitor-error').hidden = true; renderMonitor(data);
  } catch (error) {
    if (current !== monitorSequence || error.name === 'AbortError') return;
    $('monitor-error').textContent = error.message; $('monitor-error').hidden = false; $('monitor-updated').textContent = '数据已过期，正在重试';
  } finally {
    clearTimeout(timeout);
    if (current === monitorSequence && monitorVisible()) monitorTimer = setTimeout(() => loadMonitor(), 3000);
  }
}
function updateMonitorVisibility() {
  clearTimeout(monitorTimer);
  if (monitorVisible()) loadMonitor();
  else { monitorSequence++; if (monitorAbort) monitorAbort.abort(); }
}
document.addEventListener('network-view-changed', updateMonitorVisibility);
document.addEventListener('visibilitychange', updateMonitorVisibility);
document.querySelectorAll('[data-monitor-mode]').forEach(button => button.addEventListener('click', () => {
  monitorMode = button.dataset.monitorMode; monitorPage = 1;
  document.querySelectorAll('[data-monitor-mode]').forEach(b => b.setAttribute('aria-pressed', String(b === button)));
  loadMonitor(1);
}));
$('refresh').addEventListener('click', () => { if (monitorVisible()) loadMonitor(); });
$('monitor-search-form').addEventListener('submit', event => { event.preventDefault(); clearTimeout(monitorSearchTimer); loadMonitor(1); });
$('monitor-search').addEventListener('input', () => { clearTimeout(monitorSearchTimer); monitorSearchTimer = setTimeout(() => loadMonitor(1), 250); });
$('monitor-previous').addEventListener('click', () => loadMonitor(monitorPage - 1));
$('monitor-next').addEventListener('click', () => loadMonitor(monitorPage + 1));

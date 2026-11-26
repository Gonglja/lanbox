import { api } from './api.js';
import { confirmDialog, toast } from './ui.js';

const $ = (id) => document.getElementById(id);

const GROUPS = [
  {
    title: '上传与存储',
    fields: [
      { key: 'max_file_size_mb', label: '单文件上限', unit: 'MB', min: 1, step: 1 },
      { key: 'max_item_size_mb', label: '单条总大小上限', unit: 'MB', min: 1, step: 1 },
      { key: 'min_free_disk_mb', label: '保留空闲空间', unit: 'MB', min: 0, step: 1 },
      { key: 'max_files_per_item', label: '单条附件数上限', min: 1, step: 1 },
      { key: 'max_text_bytes', label: '文字上限', unit: '字节', min: 1, step: 1 },
    ],
  },
  {
    title: '网络与证书',
    fields: [
      { key: 'listen', label: '监听地址', type: 'text', hint: '如 :443 或 192.168.1.9:443（改后需重启）' },
      { key: 'tls.cert_file', label: '证书文件', type: 'text', hint: '留空则用自签证书' },
      { key: 'tls.key_file', label: '私钥文件', type: 'text', hint: '与证书同时留空或同时填写' },
      { key: 'data_dir', label: '数据目录', type: 'text' },
    ],
  },
  {
    title: '会话',
    fields: [{ key: 'session_ttl_hours', label: '会话有效期', unit: '小时', min: 1, step: 1 }],
  },
  {
    title: '在线更新',
    fields: [
      {
        key: 'update.check',
        label: '自动检查',
        type: 'text',
        hint: 'off / startup / daily，默认 daily；只提示，不会自动升级',
      },
      {
        key: 'update.url',
        label: '更新源',
        type: 'text',
        hint: '留空用 GitHub Release；以 .json 结尾则读取该清单地址',
      },
    ],
  },
  {
    title: '局域网发现',
    fields: [
      { key: 'mdns.enabled', label: '启用 mDNS 通告', type: 'bool' },
      { key: 'mdns.hostname', label: '主机名', type: 'text', hint: '访问地址 https://<主机名>.local:<端口>' },
      { key: 'mdns.instance', label: '实例名', type: 'text', hint: '留空则同主机名' },
    ],
  },
];

const originText = { default: '默认', file: '配置文件', env: '环境变量', web: '网页设置' };

let current = null;

function getValue(obj, path) {
  return path.split('.').reduce((o, k) => (o == null ? undefined : o[k]), obj);
}

function setValue(obj, path, value) {
  const parts = path.split('.');
  let cur = obj;
  for (const k of parts.slice(0, -1)) {
    if (cur[k] == null || typeof cur[k] !== 'object') cur[k] = {};
    cur = cur[k];
  }
  cur[parts[parts.length - 1]] = value;
}

function rowFor(field) {
  const row = document.createElement('div');
  row.className = 'settings-row';

  const label = document.createElement('label');
  label.className = 'settings-label';
  const text = document.createElement('span');
  text.textContent = field.label + (field.unit ? `（${field.unit}）` : '');
  label.append(text);

  const origin = current.origins[field.key] || 'default';
  const badge = document.createElement('span');
  badge.className = 'pill' + (origin === 'web' ? ' pill-warn' : '');
  badge.textContent = originText[origin] || origin;
  badge.title = '该值的来源';
  label.append(badge);
  if ((current.restart_fields || []).includes(field.key)) {
    const rb = document.createElement('span');
    rb.className = 'pill pill-warn';
    rb.textContent = '需重启';
    label.append(rb);
  }

  const wrap = document.createElement('span');
  wrap.className = 'settings-input';

  let input;
  if (field.type === 'bool') {
    input = document.createElement('input');
    input.type = 'checkbox';
    input.checked = Boolean(getValue(current.values, field.key));
  } else {
    input = document.createElement('input');
    input.type = field.type === 'text' ? 'text' : 'number';
    if (field.min !== undefined) input.min = String(field.min);
    if (field.step !== undefined) input.step = String(field.step);
    input.value = getValue(current.values, field.key) ?? '';
  }
  input.id = 'cfg-' + field.key.replace(/\./g, '-');
  input.dataset.key = field.key;
  input.dataset.kind = field.type === 'bool' ? 'bool' : field.type === 'text' ? 'text' : 'number';
  label.htmlFor = input.id;

  if (origin === 'web' && field.type !== 'bool') {
    const baseValue = getValue(current.baseline, field.key);
    if (baseValue !== undefined && baseValue !== '') input.placeholder = `恢复默认：${baseValue}`;
  }

  if (field.type === 'bool') {
    const toggle = document.createElement('label');
    toggle.className = 'check';
    const cap = document.createElement('span');
    cap.textContent = '启用';
    toggle.append(input, cap);
    wrap.append(toggle);
  } else {
    wrap.append(input);
  }

  if (field.hint) {
    const hint = document.createElement('small');
    hint.className = 'hint';
    hint.textContent = field.hint;
    wrap.append(hint);
  }

  row.append(label, wrap);
  return row;
}

function render() {
  const root = $('settings-groups');
  root.replaceChildren();
  for (const group of GROUPS) {
    const box = document.createElement('div');
    box.className = 'settings-group';
    const h = document.createElement('strong');
    h.className = 'group-title';
    h.textContent = group.title;
    box.append(h);
    for (const field of group.fields) box.append(rowFor(field));
    if (group.title === '局域网发现') {
      const st = current.mdns || {};
      const info = document.createElement('div');
      info.className = 'settings-foot';
      info.textContent = st.running
        ? `已通告：https://${st.host}.local:${st.port}（实例 ${st.instance}${
            st.addrs && st.addrs.length ? '，' + st.addrs.join(', ') : ''
          }）`
        : `未通告${st.error ? '：' + st.error : ''}`;
      box.append(info);
    }
    root.append(box);
  }
  const paths = current.paths || {};
  $('settings-hint').textContent =
    `配置文件：${paths.config_file || '-'}；网页覆盖：${paths.web_config || '-'}。` +
    '删除网页覆盖文件即回到配置文件设置。';
}

export async function showSettings() {
  $('settings-error').hidden = true;
  $('settings-hint').textContent = '加载中…';
  current = await api.config();
  render();
  $('btn-restart').hidden = true;
}

function collect() {
  const desired = JSON.parse(JSON.stringify(current.values));
  for (const input of $('settings-groups').querySelectorAll('input[data-key]')) {
    const kind = input.dataset.kind;
    let value;
    if (kind === 'bool') value = input.checked;
    else if (kind === 'number') value = Number(input.value);
    else value = input.value.trim();
    setValue(desired, input.dataset.key, value);
  }
  return desired;
}

async function save() {
  const err = $('settings-error');
  err.hidden = true;
  const btn = $('btn-save-settings');
  btn.disabled = true;
  try {
    const res = await api.saveConfig(collect(), false);
    await showSettings();
    const parts = [];
    const applied = res.applied || [];
    const restartRequired = res.restart_required || [];
    if (applied.length) parts.push(`已生效：${applied.join('、')}`);
    if (restartRequired.length) parts.push(`需重启生效：${restartRequired.join('、')}`);
    $('settings-hint').textContent = parts.join('；') || '没有变化';
    $('btn-restart').hidden = restartRequired.length === 0;
    toast(applied.length || restartRequired.length ? '设置已保存' : '没有变化', 'ok');
  } catch (e) {
    err.textContent = e.message;
    err.hidden = false;
  } finally {
    btn.disabled = false;
  }
}

async function doRestart() {
  const ok = await confirmDialog({
    title: '重启服务？',
    message: '约 1–3 秒不可用，所有人需重新登录。若刚改了监听地址，请在新地址访问。',
    confirmText: '立即重启',
    danger: true,
  });
  if (!ok) return;

  const mask = $('restart-mask');
  const err = $('restart-error');
  err.hidden = true;
  mask.hidden = false;
  try {
    await api.restart();
  } catch (e) {
    err.textContent = e.message;
    err.hidden = false;
    mask.hidden = true;
    return;
  }
  const deadline = Date.now() + 30000;
  const tick = async () => {
    try {
      const h = await api.healthz();
      if (h && h.ok) {
        location.reload();
        return;
      }
    } catch {
      /* 服务还没回来 */
    }
    if (Date.now() > deadline) {
      err.textContent = '服务 30 秒内没有回来，请检查进程与监听地址后手动访问。';
      err.hidden = false;
      return;
    }
    setTimeout(tick, 800);
  };
  setTimeout(tick, 500);
}

export function initSettings() {
  $('btn-save-settings').addEventListener('click', save);
  $('btn-restart').addEventListener('click', doRestart);
}

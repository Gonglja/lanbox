// theme-boot.js 必须在首帧绘制前同步执行，避免主题闪烁（FOUC）。
// 只读取 localStorage 与系统偏好，不依赖主脚本。
(function () {
  var KEY = 'lanbox.theme';
  var COLORS = { light: '#fafafa', dark: '#0f0f0f', paper: '#f5ede4' };
  var pref = 'system';
  try {
    var saved = localStorage.getItem(KEY);
    if (saved === 'light' || saved === 'dark' || saved === 'paper' || saved === 'system') pref = saved;
  } catch (e) {
    /* 隐私模式下 localStorage 不可用，退回 system */
  }
  var theme = pref;
  if (pref === 'system') {
    theme = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }
  var root = document.documentElement;
  root.dataset.theme = theme;
  root.dataset.themePref = pref;
  root.style.colorScheme = theme === 'dark' ? 'dark' : 'light';
  // 侧栏收合状态同样在首帧前写入，避免刷新时先宽后窄地跳一下。
  var rail = 'wide';
  try {
    if (localStorage.getItem('lanbox.sidebar') === 'rail') rail = 'rail';
  } catch (e) {
    /* 隐私模式忽略 */
  }
  root.dataset.sidebar = rail;
  var meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', COLORS[theme] || COLORS.light);
})();

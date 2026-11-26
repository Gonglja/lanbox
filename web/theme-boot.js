// theme-boot.js 必须在首帧绘制前同步执行，避免主题闪烁（FOUC）。
// 只读取 localStorage 与系统偏好，不依赖主脚本。
(function () {
  var KEY = 'lanbox.theme';
  var pref = 'system';
  try {
    var saved = localStorage.getItem(KEY);
    if (saved === 'light' || saved === 'dark' || saved === 'system') pref = saved;
  } catch (e) {
    /* 隐私模式下 localStorage 不可用，退回 system */
  }
  var dark = pref === 'dark';
  if (pref === 'system') {
    dark = !!(window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches);
  }
  var root = document.documentElement;
  root.dataset.theme = dark ? 'dark' : 'light';
  root.dataset.themePref = pref;
  root.style.colorScheme = dark ? 'dark' : 'light';
})();

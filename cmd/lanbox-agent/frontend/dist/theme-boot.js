// theme-boot.js 必须在首帧绘制前同步执行，避免主题闪烁（FOUC）。
// 与服务端 Web UI 一致：同一个 localStorage 键、同一套 4 个偏好值。
(function () {
  var KEY = 'lanbox.theme';
  var VALID = ['system', 'light', 'dark', 'paper'];
  var pref = 'system';
  try {
    var saved = localStorage.getItem(KEY);
    if (VALID.indexOf(saved) !== -1) pref = saved;
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
})();

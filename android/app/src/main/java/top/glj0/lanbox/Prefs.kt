package top.glj0.lanbox

import android.content.Context

/** 少量持久化状态：服务开关与开机自启。 */
object Prefs {
    private const val NAME = "lanbox"
    private const val KEY_ENABLED = "server_enabled"
    private const val KEY_AUTOSTART = "autostart"
    const val PORT = 10725

    private fun sp(c: Context) = c.getSharedPreferences(NAME, Context.MODE_PRIVATE)

    fun enabled(c: Context): Boolean = sp(c).getBoolean(KEY_ENABLED, false)

    fun setEnabled(c: Context, v: Boolean) =
        sp(c).edit().putBoolean(KEY_ENABLED, v).apply()

    fun autostart(c: Context): Boolean = sp(c).getBoolean(KEY_AUTOSTART, false)

    fun setAutostart(c: Context, v: Boolean) =
        sp(c).edit().putBoolean(KEY_AUTOSTART, v).apply()
}

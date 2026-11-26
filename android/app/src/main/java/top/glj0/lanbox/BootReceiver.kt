package top.glj0.lanbox

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.core.content.ContextCompat

/**
 * 开机自启：只有用户显式勾了「开机自启」且上次服务是开着的才拉起。
 *
 * Android 15 起 dataSync 类型的后台任务不允许从 BOOT_COMPLETED 启动，
 * 此时会抛异常——退化为「用户下次打开 App 时自动恢复」，属于预期降级。
 */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED &&
            intent.action != Intent.ACTION_MY_PACKAGE_REPLACED
        ) {
            return
        }
        if (!Prefs.autostart(ctx) || !Prefs.enabled(ctx)) return
        try {
            ContextCompat.startForegroundService(
                ctx,
                Intent(ctx, ServerService::class.java),
            )
        } catch (t: Throwable) {
            Log.w("lanbox", "开机自启被系统拒绝（Android 15+ 的 dataSync 限制）：${t.message}")
        }
    }
}

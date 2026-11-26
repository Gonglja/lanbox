package top.glj0.lanbox

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import java.net.Inet4Address

/**
 * 前台服务：持有 Go 服务端的生命周期与三把锁。
 *
 * 三把锁缺一不可：PARTIAL_WAKE_LOCK 防 CPU 睡眠、WifiLock 防 WiFi 省电降速、
 * MulticastLock 让息屏后仍能收发 mDNS 组播。
 */
class ServerService : Service() {

    private var wake: PowerManager.WakeLock? = null
    private var wifi: WifiManager.WifiLock? = null
    private var multicast: WifiManager.MulticastLock? = null
    private val main = Handler(Looper.getMainLooper())

    override fun onCreate() {
        super.onCreate()
        createChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            Prefs.setEnabled(this, false)
            stopSelf()
            return START_NOT_STICKY
        }
        // 必须在 5 秒内进入前台
        startForegroundCompat(buildNotification("正在启动…", failed = false))
        if (Native.running()) {
            notify(buildNotification(runningText(), failed = false))
            return START_STICKY
        }
        acquireLocks()

        val dataDir = filesDir.absolutePath
        val hostname = "lanbox-" + androidIdSuffix()
        // Go 侧初始化（配置/证书/监听）可能要几百毫秒，放到后台线程避免 ANR
        Thread {
            Native.configure(dataDir, ":${Prefs.PORT}", hostname, BuildConfig.VERSION_NAME, lanIPv4())
            val err = try {
                Native.start()
            } catch (t: Throwable) {
                t.message ?: t.toString()
            }
            main.post {
                if (err.isNullOrEmpty()) {
                    notify(buildNotification(runningText(), failed = false))
                    Log.i(TAG, "服务已启动：${Native.addrs()}")
                } else {
                    Prefs.setEnabled(this, false)
                    notify(buildNotification("启动失败：$err", failed = true))
                    Log.e(TAG, "启动失败：$err")
                    stopSelf()
                }
            }
        }.start()

        return START_STICKY
    }

    override fun onDestroy() {
        try {
            Native.stop()
        } catch (t: Throwable) {
            Log.w(TAG, "停止 Go 服务失败：${t.message}")
        }
        releaseLocks()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    // ---------- 锁 ----------

    private fun acquireLocks() {
        try {
            val pm = getSystemService(POWER_SERVICE) as PowerManager
            wake = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "lanbox:cpu").apply {
                setReferenceCounted(false)
                acquire()
            }
        } catch (t: Throwable) {
            Log.w(TAG, "WakeLock 获取失败：${t.message}")
        }
        try {
            val wm = applicationContext.getSystemService(WIFI_SERVICE) as WifiManager
            wifi = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "lanbox:wifi").apply {
                setReferenceCounted(false)
                acquire()
            }
            multicast = wm.createMulticastLock("lanbox:mdns").apply {
                setReferenceCounted(false)
                acquire()
            }
        } catch (t: Throwable) {
            Log.w(TAG, "WiFi/Multicast 锁获取失败：${t.message}")
        }
    }

    private fun releaseLocks() {
        runCatching { multicast?.takeIf { it.isHeld }?.release() }
        runCatching { wifi?.takeIf { it.isHeld }?.release() }
        runCatching { wake?.takeIf { it.isHeld }?.release() }
        multicast = null
        wifi = null
        wake = null
    }

    // ---------- 通知 ----------

    private fun createChannel() {
        val nm = getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CHANNEL_ID) == null) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_ID, "lanbox 服务", NotificationManager.IMPORTANCE_LOW).apply {
                    description = "文件共享服务的运行状态"
                    setShowBadge(false)
                },
            )
        }
    }

    private fun runningText(): String {
        val first = Native.addrs().lineSequence().firstOrNull().orEmpty()
        val lan = Native.addrs().lineSequence()
            .firstOrNull { !it.contains("127.0.0.1") && !it.contains("[::1]") }
            .orEmpty()
        val shown = lan.ifEmpty { first }
        return if (shown.isEmpty()) "正在运行" else "正在运行 · $shown"
    }

    private fun buildNotification(text: String, failed: Boolean): Notification {
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val stop = PendingIntent.getService(
            this, 1, Intent(this, ServerService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val builder = NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_lanbox)
            .setContentTitle(if (failed) "lanbox 启动失败" else "lanbox")
            .setContentText(text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setOngoing(!failed)
            .setContentIntent(open)
            .setOnlyAlertOnce(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
        if (!failed) {
            builder.addAction(0, "停止", stop)
        }
        return builder.build()
    }

    private fun notify(n: Notification) {
        val nm = getSystemService(NotificationManager::class.java)
        nm.notify(NOTIF_ID, n)
    }

    private fun startForegroundCompat(n: Notification) {
        val type = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC
        } else {
            0
        }
        ServiceCompat.startForeground(this, NOTIF_ID, n, type)
    }

    /**
     * 读当前 WiFi 的 IPv4：Android 上 SELinux 禁止 netlink，Go 的 InterfaceAddrs()
     * 只能看到回环，所以局域网地址必须由 Java 侧提供。
     */
    private fun lanIPv4(): String {
        return try {
            val cm = getSystemService(ConnectivityManager::class.java)
            val lp = cm.getLinkProperties(cm.activeNetwork) ?: return ""
            lp.linkAddresses
                .map { it.address }
                .filterIsInstance<Inet4Address>()
                .firstOrNull { !it.isLoopbackAddress && !it.isLinkLocalAddress }
                ?.hostAddress.orEmpty()
        } catch (t: Throwable) {
            Log.w(TAG, "读取局域网地址失败：${t.message}")
            ""
        }
    }

    /** 用 ANDROID_ID 后四位区分同一局域网里的多台手机。 */
    private fun androidIdSuffix(): String {
        val id = Settings.Secure.getString(contentResolver, Settings.Secure.ANDROID_ID) ?: ""
        val tail = id.takeLast(4).lowercase().filter { it.isLetterOrDigit() }
        return tail.ifEmpty { "0000" }
    }

    companion object {
        private const val TAG = "lanbox"
        private const val CHANNEL_ID = "lanbox_server"
        private const val NOTIF_ID = 1001
        const val ACTION_STOP = "top.glj0.lanbox.STOP"

        fun start(c: Context) {
            val intent = Intent(c, ServerService::class.java)
            androidx.core.content.ContextCompat.startForegroundService(c, intent)
        }
    }
}

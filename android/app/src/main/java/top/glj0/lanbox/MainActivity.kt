package top.glj0.lanbox

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.view.View
import android.widget.Button
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.appcompat.widget.SwitchCompat
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import java.io.File

class MainActivity : AppCompatActivity() {

    private lateinit var switchServer: SwitchCompat
    private lateinit var switchAutostart: SwitchCompat
    private lateinit var status: TextView
    private lateinit var addrs: TextView
    private lateinit var logs: TextView

    private val handler = Handler(Looper.getMainLooper())
    private val tick = object : Runnable {
        override fun run() {
            refresh()
            handler.postDelayed(this, 1500)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        switchServer = findViewById(R.id.switch_server)
        switchAutostart = findViewById(R.id.switch_autostart)
        status = findViewById(R.id.status)
        addrs = findViewById(R.id.addrs)
        logs = findViewById(R.id.logs)

        requestNotificationPermission()
        showBootstrapPasswordIfAny()

        switchServer.setOnCheckedChangeListener { _, checked -> setServerEnabled(checked) }

        switchAutostart.isChecked = Prefs.autostart(this)
        switchAutostart.setOnCheckedChangeListener { _, checked -> Prefs.setAutostart(this, checked) }

        findViewById<Button>(R.id.btn_ui).setOnClickListener {
            if (!Native.running()) {
                toast("先启动服务")
                return@setOnClickListener
            }
            startActivity(Intent(this, WebViewActivity::class.java))
        }
        findViewById<Button>(R.id.btn_copy).setOnClickListener { copyFirstAddress() }
        findViewById<Button>(R.id.btn_battery).setOnClickListener { requestIgnoreBatteryOptimization() }
        findViewById<Button>(R.id.btn_clear).setOnClickListener { confirmClearData() }
    }

    override fun onResume() {
        super.onResume()
        // 状态记忆：上次开着就自动拉起（也覆盖「被系统杀掉后重开」）
        switchServer.isChecked = Prefs.enabled(this)
        if (Prefs.enabled(this) && !Native.running()) {
            ServerService.start(this)
        }
        handler.post(tick)
    }

    override fun onPause() {
        handler.removeCallbacks(tick)
        super.onPause()
    }

    // ---------- 开关 ----------

    private fun setServerEnabled(enabled: Boolean) {
        Prefs.setEnabled(this, enabled)
        if (enabled) {
            if (!isOnWifi()) toast("当前不是 WiFi 网络，其它设备可能访问不到")
            ServerService.start(this)
        } else {
            stopService(Intent(this, ServerService::class.java))
            Thread { Native.stop() }.start()
        }
        refresh()
    }

    private fun refresh() {
        val running = Native.running()
        if (switchServer.isChecked != running && switchServer.isChecked != Prefs.enabled(this)) {
            switchServer.isChecked = Prefs.enabled(this)
        }
        status.text = when {
            running -> "服务运行中"
            Prefs.enabled(this) -> "正在启动…（若长时间无响应，检查是否被系统清理）"
            else -> "服务已停止"
        }
        addrs.text = if (running) Native.addrs() else ""
        addrs.visibility = if (running) View.VISIBLE else View.GONE
        logs.text = Native.logs().lines().takeLast(40).joinToString("\n")
    }

    // ---------- 动作 ----------

    private fun copyFirstAddress() {
        val first = Native.addrs().lineSequence().firstOrNull().orEmpty()
        if (first.isEmpty()) {
            toast("服务未运行")
            return
        }
        val cm = getSystemService(ClipboardManager::class.java)
        cm.setPrimaryClip(ClipData.newPlainText("lanbox", first))
        toast("已复制 $first")
    }

    private fun requestIgnoreBatteryOptimization() {
        val pm = getSystemService(PowerManager::class.java)
        if (pm.isIgnoringBatteryOptimizations(packageName)) {
            toast("已在白名单里")
            return
        }
        val intent = Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS)
            .setData(Uri.parse("package:$packageName"))
        runCatching { startActivity(intent) }
            .onFailure { startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS)) }
    }

    private fun confirmClearData() {
        AlertDialog.Builder(this)
            .setTitle("清空本地数据？")
            .setMessage("会停止服务并删除手机上的全部内容与账号。此操作不可撤销。")
            .setNegativeButton("取消", null)
            .setPositiveButton("清空") { _, _ ->
                Prefs.setEnabled(this, false)
                switchServer.isChecked = false
                stopService(Intent(this, ServerService::class.java))
                Thread {
                    Native.stop()
                    File(filesDir, "lanbox").deleteRecursively()
                    mainHandler().post {
                        toast("已清空")
                        refresh()
                    }
                }.start()
            }
            .show()
    }

    private fun showBootstrapPasswordIfAny() {
        val pw = Native.adminPassword()
        if (pw.isEmpty()) return
        AlertDialog.Builder(this)
            .setTitle("已创建管理员 admin")
            .setMessage("初始密码：$pw\n\n请立即保存；可在网页「账号」里修改。")
            .setPositiveButton("知道了", null)
            .show()
    }

    private fun isOnWifi(): Boolean {
        val cm = getSystemService(ConnectivityManager::class.java)
        val caps = cm.getNetworkCapabilities(cm.activeNetwork) ?: return false
        return caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return
        if (ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) ==
            PackageManager.PERMISSION_GRANTED
        ) {
            return
        }
        ActivityCompat.requestPermissions(this, arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
    }

    private fun toast(msg: String) = Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()

    private fun mainHandler() = handler
}

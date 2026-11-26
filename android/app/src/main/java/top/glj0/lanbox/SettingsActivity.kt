package top.glj0.lanbox

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.drawable.GradientDrawable
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
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.appcompat.widget.SwitchCompat
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import java.io.File

/** 控制台：服务开关、访问地址、保活设置与运行日志。首页是 Web UI，齿轮进这里。 */
class SettingsActivity : AppCompatActivity() {

    private lateinit var switchServer: SwitchCompat
    private lateinit var switchAutostart: SwitchCompat
    private lateinit var status: TextView
    private lateinit var addrs: TextView
    private lateinit var hint: TextView
    private lateinit var logs: TextView
    private lateinit var logsSummary: TextView
    private lateinit var logsChevron: TextView
    private lateinit var dot: View
    private lateinit var actions: View

    private var logsOpen = false
    private val handler = Handler(Looper.getMainLooper())
    private val tick = object : Runnable {
        override fun run() {
            refresh()
            handler.postDelayed(this, 1500)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_settings)

        switchServer = findViewById(R.id.switch_server)
        switchAutostart = findViewById(R.id.switch_autostart)
        status = findViewById(R.id.status)
        addrs = findViewById(R.id.addrs)
        hint = findViewById(R.id.hint)
        logs = findViewById(R.id.logs)
        logsSummary = findViewById(R.id.logs_summary)
        logsChevron = findViewById(R.id.logs_chevron)
        dot = findViewById(R.id.dot)
        actions = findViewById(R.id.actions)

        findViewById<TextView>(R.id.version).text = "v${BuildConfig.VERSION_NAME}"
        findViewById<View>(R.id.btn_back).setOnClickListener { finish() }

        requestNotificationPermission()
        showBootstrapPasswordIfAny()

        switchServer.setOnCheckedChangeListener { _, checked -> setServerEnabled(checked) }
        switchAutostart.isChecked = Prefs.autostart(this)
        switchAutostart.setOnCheckedChangeListener { _, checked -> Prefs.setAutostart(this, checked) }

        findViewById<View>(R.id.btn_ui).setOnClickListener {
            startActivity(Intent(this, MainActivity::class.java))
            finish()
        }
        findViewById<View>(R.id.btn_copy).setOnClickListener { copyFirstAddress() }
        findViewById<View>(R.id.row_battery).setOnClickListener { requestIgnoreBatteryOptimization() }
        findViewById<View>(R.id.row_clear).setOnClickListener { confirmClearData() }
        findViewById<View>(R.id.row_logs).setOnClickListener { toggleLogs() }
    }

    override fun onResume() {
        super.onResume()
        applyWindowInsets(findViewById(R.id.root))
        switchServer.isChecked = Prefs.enabled(this)
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
        val starting = Prefs.enabled(this) && !running
        if (switchServer.isChecked != Prefs.enabled(this) && !starting) {
            switchServer.isChecked = Prefs.enabled(this)
        }
        status.text = when {
            running -> "服务运行中"
            starting -> "正在启动…"
            else -> "服务已停止"
        }
        dot.background = circle(
            when {
                running -> getColor(R.color.ok)
                starting -> getColor(R.color.warn)
                else -> getColor(R.color.text_dim)
            },
        )

        val list = if (running) Native.addrs().lines().filter { it.isNotBlank() } else emptyList()
        val lan = list.filter { !it.contains("127.0.0.1") }
        val loop = list.filter { it.contains("127.0.0.1") }
        addrs.text = (lan + loop.map { "$it（本机）" }).joinToString("\n")
        addrs.visibility = if (list.isEmpty()) View.GONE else View.VISIBLE
        actions.visibility = if (running) View.VISIBLE else View.GONE

        val showHint = running && lan.isEmpty()
        hint.text = "没拿到 WiFi 地址：请确认已连上 WiFi，再关开一次服务"
        hint.visibility = if (showHint) View.VISIBLE else View.GONE

        val lines = Native.logs().lines().filter { it.isNotBlank() }
        logsSummary.text = if (lines.isEmpty()) "暂无日志" else "最近 ${minOf(lines.size, 40)} 行"
        logs.text = if (logsOpen) lines.takeLast(40).joinToString("\n") else ""
    }

    private fun toggleLogs() {
        logsOpen = !logsOpen
        logs.visibility = if (logsOpen) View.VISIBLE else View.GONE
        logsChevron.text = if (logsOpen) "⌃" else "⌄"
        refresh()
    }

    // ---------- 动作 ----------

    private fun copyFirstAddress() {
        val all = Native.addrs().lines().filter { it.isNotBlank() }
        val first = all.firstOrNull { !it.contains("127.0.0.1") } ?: all.firstOrNull()
        if (first.isNullOrBlank()) {
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
                    handler.post {
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
            .setMessage("初始密码：$pw\n\n可在网页「账号」里修改。")
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

    private fun circle(color: Int) = GradientDrawable().apply {
        shape = GradientDrawable.OVAL
        setColor(color)
        setSize(dp(9), dp(9))
    }

    private fun dp(v: Int): Int = (v * resources.displayMetrics.density).toInt()

    private fun applyWindowInsets(root: View) {
        val current = ViewCompat.getRootWindowInsets(root)
        if (current != null) {
            val bars = current.getInsets(
                WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.ime(),
            )
            root.setPadding(0, bars.top, 0, bars.bottom)
        }
        ViewCompat.setOnApplyWindowInsetsListener(root) { v, insets ->
            val bars = insets.getInsets(
                WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.ime(),
            )
            v.setPadding(0, bars.top, 0, bars.bottom)
            insets
        }
    }

    private fun toast(msg: String) = Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()
}

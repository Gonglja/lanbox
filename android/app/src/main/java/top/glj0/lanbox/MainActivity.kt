package top.glj0.lanbox

import android.Manifest
import android.annotation.SuppressLint
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.Uri
import android.net.http.SslCertificate
import android.net.http.SslError
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.util.Log
import android.view.View
import android.webkit.CookieManager
import android.webkit.DownloadListener
import android.webkit.SslErrorHandler
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import androidx.activity.OnBackPressedCallback
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import java.io.File
import java.security.MessageDigest

/**
 * 首页就是 lanbox 的 Web UI：打开 App 自动拉起服务，加载 127.0.0.1:8443。
 *
 * 控制台（服务开关 / 地址 / 日志 / 设置项）挪到了 [SettingsActivity]，右下角齿轮进入。
 */
class MainActivity : AppCompatActivity() {

    private lateinit var web: WebView
    private lateinit var loading: View
    private lateinit var loadingText: TextView
    private lateinit var spinner: ProgressBar

    private var pin: String = ""
    private var loadedUrl: String? = null
    private var waitingForServer = false

    private val handler = Handler(Looper.getMainLooper())
    private val waitTick = object : Runnable {
        override fun run() {
            if (Native.running()) {
                waitingForServer = false
                showContent()
                loadApp()
            } else if (!Prefs.enabled(this@MainActivity)) {
                // 用户在设置里关掉了服务：停在提示页
                waitingForServer = false
                showStopHint()
            } else {
                handler.postDelayed(this, 700)
            }
        }
    }

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        web = findViewById(R.id.web)
        loading = findViewById(R.id.loading)
        loadingText = findViewById(R.id.loading_text)
        spinner = findViewById(R.id.spinner)

        if (BuildConfig.DEBUG) WebView.setWebContentsDebuggingEnabled(true)
        requestNotificationPermission()
        showBootstrapPasswordIfAny()

        with(web.settings) {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = false
            allowContentAccess = false
            cacheMode = WebSettings.LOAD_NO_CACHE
        }
        web.webViewClient = object : WebViewClient() {
            override fun onPageFinished(view: WebView?, url: String?) {
                loadedUrl = url
                if (Native.running()) showContent()
            }

            override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
                val host = error.url?.let { runCatching { Uri.parse(it).host }.getOrNull() }
                val der = SslCertificate.saveState(error.certificate).getByteArray("x509-certificate")
                // 现取指纹：服务可能刚启动完，构造时拿到的还是空串
                val expected = Native.certPin().lowercase()
                val matches = der != null && expected.isNotEmpty() && sha256Hex(der) == expected
                if (host == "127.0.0.1" && matches) {
                    handler.proceed()
                } else {
                    Log.w(TAG, "拒绝证书：host=$host")
                    handler.cancel()
                    toast("证书校验失败，请到设置里关开服务")
                }
            }
        }
        web.setDownloadListener(
            DownloadListener { url, userAgent, cd, mime, _ ->
                Downloads.enqueue(this, url, userAgent, cd, mime)
            },
        )

        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (web.canGoBack()) web.goBack() else finish()
            }
        })

        findViewById<View>(R.id.btn_settings).setOnClickListener {
            startActivity(Intent(this, SettingsActivity::class.java))
        }
        loading.setOnClickListener { restartServerIfStopped() }

        // 打开 App 即当服务端：没开就开
        if (!Prefs.enabled(this)) {
            Prefs.setEnabled(this, true)
            if (!isOnWifi()) toast("当前不是 WiFi 网络，其它设备可能访问不到")
        }
        ServerService.start(this)
        waitingForServer = true
        handler.post(waitTick)
    }

    override fun onResume() {
        super.onResume()
        // 视图挂载后 insets 才可用（onCreate 里读到的是 null）
        applyWindowInsets(findViewById(R.id.root))
        if (BuildConfig.DEBUG) WebView.setWebContentsDebuggingEnabled(true)
        if (Native.running()) {
            showContent()
            if (loadedUrl == null) loadApp() else verifySession()
        } else {
            restartServerIfStopped()
        }
    }

    override fun onPause() {
        handler.removeCallbacks(waitTick)
        super.onPause()
    }

    // ---------- 页面 ----------

    private fun loadApp() {
        pin = Native.certPin().lowercase()
        loadedUrl = null
        web.loadUrl(bootstrapUrl())
        web.visibility = View.VISIBLE
    }

    /** 会话过期（/api/me 401）无感重新引导。 */
    private fun verifySession() {
        web.evaluateJavascript(
            "fetch('/api/me',{credentials:'include'}).then(r=>r.status).catch(()=>0)",
        ) { status ->
            val code = status?.trim('"')?.toIntOrNull() ?: 0
            if (code == 401) web.loadUrl(bootstrapUrl())
        }
    }

    private fun bootstrapUrl(): String {
        val token = Native.loopbackToken()
        return if (token.isEmpty()) {
            "https://127.0.0.1:${Prefs.PORT}/"
        } else {
            "https://127.0.0.1:${Prefs.PORT}/__app/login?t=$token"
        }
    }

    private fun showContent() {
        loading.visibility = View.GONE
        web.visibility = View.VISIBLE
    }

    private fun showStopHint() {
        web.visibility = View.GONE
        loading.visibility = View.VISIBLE
        spinner.visibility = View.GONE
        loadingText.text = "服务已停止\n点这里重新启动，或到设置里调整"
    }

    private fun restartServerIfStopped() {
        if (Native.running()) {
            showContent()
            if (loadedUrl == null) loadApp()
            return
        }
        web.visibility = View.GONE
        loading.visibility = View.VISIBLE
        spinner.visibility = View.VISIBLE
        loadingText.text = "正在启动服务…"
        Prefs.setEnabled(this, true)
        ServerService.start(this)
        if (!waitingForServer) {
            waitingForServer = true
            handler.post(waitTick)
        }
    }

    // ---------- 首次启动 ----------

    private fun showBootstrapPasswordIfAny() {
        val pw = Native.adminPassword()
        if (pw.isEmpty()) return
        AlertDialog.Builder(this)
            .setTitle("已创建管理员 admin")
            .setMessage("初始密码：$pw\n\n已自动登录；可在网页「账号」里修改密码。")
            .setPositiveButton("知道了", null)
            .show()
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

    private fun isOnWifi(): Boolean {
        val cm = getSystemService(ConnectivityManager::class.java)
        val caps = cm.getNetworkCapabilities(cm.activeNetwork) ?: return false
        return caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)
    }

    /**
     * targetSdk 35 在 Android 15 上强制 edge-to-edge：不加 insets 的话，
     * 页面自己的顶栏（汉堡按钮）会被系统状态栏压住。
     */
    private fun applyWindowInsets(root: View) {
        val current = ViewCompat.getRootWindowInsets(root)
        if (current != null) {
            val bars = current.getInsets(
                WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.ime(),
            )
            root.setPadding(0, bars.top, 0, bars.bottom)
        }
        // 键盘/旋转后重新应用
        ViewCompat.setOnApplyWindowInsetsListener(root) { v, insets ->
            val bars = insets.getInsets(
                WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.ime(),
            )
            v.setPadding(0, bars.top, 0, bars.bottom)
            insets
        }
    }

    private fun toast(msg: String) = Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()

    companion object {
        private const val TAG = "lanbox"

        fun sha256Hex(data: ByteArray): String {
            val sum = MessageDigest.getInstance("SHA-256").digest(data)
            return sum.joinToString("") { "%02x".format(it) }
        }
    }
}

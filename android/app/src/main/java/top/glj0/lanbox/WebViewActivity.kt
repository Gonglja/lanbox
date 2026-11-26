package top.glj0.lanbox

import android.annotation.SuppressLint
import android.content.Intent
import android.net.Uri
import android.net.http.SslCertificate
import android.net.http.SslError
import android.os.Bundle
import android.util.Log
import android.webkit.CookieManager
import android.webkit.DownloadListener
import android.webkit.SslErrorHandler
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.OnBackPressedCallback
import androidx.appcompat.app.AppCompatActivity
import java.security.MessageDigest

/**
 * 应用内客户端：加载本机 127.0.0.1:8443 的 Web UI。
 *
 * 证书处理：只对回环地址放行，且必须与 Go 侧给出的证书指纹一致；
 * 其他地址仍走系统 CA 校验（连别人的服务器时该怎么报错就怎么报错）。
 */
class WebViewActivity : AppCompatActivity() {

    private lateinit var web: WebView
    private var pin: String = ""

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        pin = Native.certPin().lowercase()

        web = WebView(this)
        setContentView(web)
        with(web.settings) {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = false
            allowContentAccess = false
            cacheMode = WebSettings.LOAD_NO_CACHE
        }
        web.webViewClient = object : WebViewClient() {
            override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
                val host = error.url?.let { runCatching { Uri.parse(it).host }.getOrNull() }
                val der = SslCertificate.saveState(error.certificate).getByteArray("x509-certificate")
                val matches = der != null && sha256Hex(der) == pin
                if (host == "127.0.0.1" && matches) {
                    handler.proceed()
                } else {
                    Log.w(TAG, "拒绝证书：host=$host pin=$pin")
                    handler.cancel()
                    toast("证书校验失败，请回 App 重开服务")
                }
            }
        }
        web.setDownloadListener(
            DownloadListener { url, userAgent, contentDisposition, mimeType, _ ->
                Downloads.enqueue(this, url, userAgent, contentDisposition, mimeType)
            },
        )

        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (web.canGoBack()) web.goBack() else finish()
            }
        })

        web.loadUrl(bootstrapUrl())
    }

    override fun onResume() {
        super.onResume()
        // 会话过期后（/api/me 401）自动重新引导，用户无感知
        web.evaluateJavascript(
            "fetch('/api/me',{credentials:'include'}).then(r=>r.status).catch(()=>0)",
        ) { status ->
            val code = status?.trim('"')?.toIntOrNull() ?: 0
            if (code == 401) web.loadUrl(bootstrapUrl())
        }
    }

    /** 回环引导登录 URL：用一次性令牌换正常会话 Cookie。 */
    private fun bootstrapUrl(): String {
        val token = Native.loopbackToken()
        return if (token.isEmpty()) {
            "https://127.0.0.1:${Prefs.PORT}/"
        } else {
            "https://127.0.0.1:${Prefs.PORT}/__app/login?t=$token"
        }
    }

    override fun onDestroy() {
        web.destroy()
        super.onDestroy()
    }

    private fun toast(msg: String) =
        android.widget.Toast.makeText(this, msg, android.widget.Toast.LENGTH_SHORT).show()

    companion object {
        private const val TAG = "lanbox"

        fun sha256Hex(data: ByteArray): String {
            val sum = MessageDigest.getInstance("SHA-256").digest(data)
            return sum.joinToString("") { "%02x".format(it) }
        }
    }
}

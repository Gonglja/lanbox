package top.glj0.lanbox

import android.content.Context
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.webkit.CookieManager
import android.widget.Toast
import java.io.File
import java.net.URL
import java.security.KeyStore
import java.security.cert.CertificateFactory
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManagerFactory

/**
 * 应用内下载：WebView 里点附件时由 Kotlin 自己取。
 *
 * 为什么不用系统 DownloadManager：服务端是自签证书，系统信任库不放行。
 * 这里用「只信任本机那一张自签证书」的 SSLContext，加上 WebView 会话 Cookie，
 * 把文件落到 App 的外部下载目录（卸载即清）。
 */
object Downloads {

    private val main = Handler(Looper.getMainLooper())

    fun enqueue(c: Context, url: String, userAgent: String?, contentDisposition: String?, mime: String?) {
        val name = filenameFrom(contentDisposition, url)
        toast(c, "开始下载 $name")
        Thread {
            try {
                val dir = c.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS) ?: c.filesDir
                dir.mkdirs()
                val target = File(dir, name)
                val conn = (URL(url).openConnection() as HttpsURLConnection).apply {
                    sslSocketFactory = pinnedFactory(c).socketFactory
                    setRequestProperty("User-Agent", userAgent ?: "lanbox-android")
                    CookieManager.getInstance().getCookie(url)?.let { setRequestProperty("Cookie", it) }
                    connectTimeout = 15_000
                    readTimeout = 60_000
                }
                try {
                    if (conn.responseCode !in 200..299) {
                        throw IllegalStateException("HTTP ${conn.responseCode}")
                    }
                    conn.inputStream.use { input ->
                        target.outputStream().use { output -> input.copyTo(output) }
                    }
                    toast(c, "已保存到 ${target.absolutePath}")
                } finally {
                    conn.disconnect()
                }
            } catch (t: Throwable) {
                Log.w("lanbox", "下载失败：${t.message}")
                toast(c, "下载失败：${t.message}")
            }
        }.start()
    }

    /** 只信任 filesDir/lanbox/tls/cert.pem 这一张证书。 */
    private fun pinnedFactory(c: Context): SSLContext {
        val certFile = File(c.filesDir, "lanbox/tls/cert.pem")
        val cf = CertificateFactory.getInstance("X.509")
        val cert = certFile.inputStream().use { cf.generateCertificate(it) }
        val ks = KeyStore.getInstance(KeyStore.getDefaultType()).apply {
            load(null, null)
            setCertificateEntry("lanbox", cert)
        }
        val tmf = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply {
            init(ks)
        }
        return SSLContext.getInstance("TLS").apply { init(null, tmf.trustManagers, null) }
    }

    private fun filenameFrom(contentDisposition: String?, url: String): String {
        contentDisposition?.let { cd ->
            val star = Regex("filename\\*=UTF-8''([^;]+)", RegexOption.IGNORE_CASE).find(cd)
            if (star != null) return sanitize(decode(star.groupValues[1]))
            val plain = Regex("filename=\"?([^\";]+)\"?", RegexOption.IGNORE_CASE).find(cd)
            if (plain != null) return sanitize(decode(plain.groupValues[1]))
        }
        val path = runCatching { URL(url).path }.getOrDefault("")
        val base = path.substringAfterLast('/').ifEmpty { "lanbox-file" }
        return sanitize(base)
    }

    private fun decode(s: String): String = runCatching {
        java.net.URLDecoder.decode(s, "UTF-8")
    }.getOrDefault(s)

    private fun sanitize(name: String): String {
        val cleaned = name.replace('/', '_').replace('\\', '_').trim()
        return when {
            cleaned.isEmpty() -> "lanbox-file"
            cleaned == "." || cleaned == ".." -> "lanbox-file"
            else -> cleaned
        }
    }

    private fun toast(c: Context, msg: String) {
        main.post { Toast.makeText(c, msg, Toast.LENGTH_SHORT).show() }
    }
}

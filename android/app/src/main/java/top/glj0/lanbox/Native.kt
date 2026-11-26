package top.glj0.lanbox

/** liblanbox.so 的 JNI 桥（对应 mobile/jni/jni_android.go 里的导出函数）。 */
object Native {
    init {
        System.loadLibrary("lanbox")
    }

    external fun configure(
        dataDir: String,
        listen: String,
        hostname: String,
        version: String,
        lanIP: String,
    )

    /** 启动服务：返回空串表示成功，否则是错误文案。 */
    external fun start(): String

    external fun stop()

    external fun running(): Boolean

    /** 每行一个 https://host:port，首行是 127.0.0.1。 */
    external fun addrs(): String

    external fun certPin(): String

    external fun loopbackToken(): String

    external fun logs(): String

    external fun adminPassword(): String
}

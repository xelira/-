package com.and.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.IpPrefix
import android.net.VpnService
import android.os.Build
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.util.Log
import androidx.core.app.NotificationCompat
import hev.htproxy.TProxyService
import org.json.JSONObject
import java.io.File
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.concurrent.thread

class DpiVpnService : VpnService() {
    private val engineRunning = AtomicBoolean(false)
    private val stopping = AtomicBoolean(false)
    private var tun: ParcelFileDescriptor? = null
    private var proxyThread: Thread? = null
    private var tunnelThread: Thread? = null
    private var heartbeat: Thread? = null

    override fun onBind(intent: Intent?): IBinder? {
        return super.onBind(intent)
    }

    override fun onCreate() {
        super.onCreate()
        createChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        Log.d("AndDpi", "service command ${intent?.action}")
        when (intent?.action) {
            ACTION_START -> {
                startForegroundCompat("Запуск ByeDPI…")
                val config = intent.getStringExtra(EXTRA_CONFIG)
                if (config.isNullOrBlank()) {
                    fail("Пустая конфигурация DPI")
                } else {
                    startEngine(config)
                }
            }
            ACTION_STOP -> stopEngine()
            else -> {
                if (!engineRunning.get()) stopSelf()
            }
        }
        return START_NOT_STICKY
    }

    override fun onRevoke() {
        stopEngine("VPN отозван системой")
    }

    override fun onDestroy() {
        Log.d("AndDpi", "service destroy")
        stopEngine()
        super.onDestroy()
    }

    private fun startEngine(configJSON: String) {
        if (!engineRunning.compareAndSet(false, true)) return
        stopping.set(false)
        DpiBridge.writeStatus(this, false, "starting")
        thread(name = "and-dpi", isDaemon = true) {
            try {
                val cfg = JSONObject(configJSON)
                val port = cfg.optInt("port", 1080)
                val dns = cfg.optString("dns", "1.1.1.1").ifBlank { "1.1.1.1" }
                val ipv6 = cfg.optBoolean("ipv6", true)
                val argsArray = cfg.optJSONArray("args") ?: throw IllegalStateException("нет стратегии ByeDPI")
                val args = Array(argsArray.length()) { argsArray.getString(it) }

                proxyThread = thread(name = "byedpi", isDaemon = true) {
                    val code = NativeProxy.start(arrayOf("ciadpi") + args)
                    if (!stopping.get() && code != 0) {
                        fail("ByeDPI завершился с кодом $code")
                    }
                }

                waitForPort(port, 12_000)
                if (stopping.get()) return@thread

                val builder = Builder()
                    .setSession("& DPI")
                    .addAddress("10.10.10.10", 32)
                    .addRoute("0.0.0.0", 0)
                    .addDisallowedApplication(packageName)
                configureDns(builder, dns)
                if (ipv6) {
                    builder.addAddress("fd00::1", 128)
                    builder.addRoute("::", 0)
                }
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                    builder.setMetered(false)
                }
                builder.setConfigureIntent(
                    PendingIntent.getActivity(
                        this, 0, Intent(this, AndActivity::class.java), PendingIntent.FLAG_IMMUTABLE
                    )
                )
                val fd = builder.establish() ?: throw IllegalStateException("не удалось создать VPN")
                tun = fd

                val cfgFile = File(filesDir, "hev.yml")
                cfgFile.writeText(
                    """
                    tunnel:
                      mtu: 8500
                    misc:
                      task-stack-size: 81920
                    socks5:
                      address: 127.0.0.1
                      port: $port
                      udp: udp
                    """.trimIndent()
                )

                tunnelThread = thread(name = "hev-tunnel", isDaemon = true) {
                    val ok = TProxyService.TProxyStartService(cfgFile.absolutePath, fd.fd)
                    if (!stopping.get() && !ok) {
                        fail("hev-socks5-tunnel завершился с ошибкой")
                    }
                }

                Thread.sleep(150)
                if (stopping.get()) return@thread
                DpiBridge.writeStatus(this, true, "", port)
                updateNotification("ByeDPI работает · порт $port")
                heartbeat = thread(name = "dpi-heartbeat", isDaemon = true) {
                    while (!stopping.get() && engineRunning.get()) {
                        DpiBridge.writeStatus(this, true, "", port)
                        Thread.sleep(5_000)
                    }
                }
            } catch (t: Throwable) {
                fail(t.message ?: "Ошибка запуска DPI")
            }
        }
    }

    private fun configureDns(builder: Builder, fallback: String) {
        val cm = getSystemService(ConnectivityManager::class.java)
        val link = cm.getLinkProperties(cm.activeNetwork)
        val servers = link?.dnsServers.orEmpty().ifEmpty {
            if (fallback.isNotBlank()) listOf(InetAddress.getByName(fallback)) else emptyList()
        }
        for (server in servers) {
            builder.addDnsServer(server)
            // Let DNS bypass the tunnel and use the underlying network directly.
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                val prefix = if (server is Inet4Address) 32 else 128
                builder.excludeRoute(IpPrefix(server, prefix))
            }
        }
    }

    private fun waitForPort(port: Int, timeoutMs: Long) {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            if (stopping.get()) return
            try {
                Socket().use {
                    it.connect(InetSocketAddress("127.0.0.1", port), 250)
                    return
                }
            } catch (_: Exception) {
                Thread.sleep(100)
            }
        }
        throw IllegalStateException("ByeDPI не открыл локальный порт $port")
    }

    @Synchronized
    private fun stopEngine(error: String? = null) {
        if (stopping.getAndSet(true) && !engineRunning.get()) {
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
            return
        }
        runCatching { TProxyService.TProxyStopService() }
        runCatching { NativeProxy.stop() }
        runCatching { tunnelThread?.join(1500) }
        runCatching { proxyThread?.join(1500) }
        runCatching { tun?.close() }
        tun = null
        engineRunning.set(false)
        runCatching { heartbeat?.join(500) }
        DpiBridge.writeStatus(this, false, error ?: "", 0)
        updateNotification(if (error != null) error else "DPI выключен")
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    private fun fail(message: String) {
        DpiBridge.writeStatus(this, false, message)
        stopEngine(message)
    }

    private fun createChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val manager = getSystemService(NotificationManager::class.java)
            val channel = NotificationChannel(CHANNEL_ID, "& DPI", NotificationManager.IMPORTANCE_LOW)
            channel.setShowBadge(false)
            manager.createNotificationChannel(channel)
        }
    }

    private fun notification(text: String): Notification {
        val intent = PendingIntent.getActivity(
            this, 0, Intent(this, AndActivity::class.java), PendingIntent.FLAG_IMMUTABLE
        )
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_stat_and)
            .setContentTitle("& · DPI")
            .setContentText(text)
            .setContentIntent(intent)
            .setOngoing(true)
            .setSilent(true)
            .build()
    }

    private fun startForegroundCompat(text: String) {
        val n = notification(text)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED)
        } else {
            startForeground(NOTIFICATION_ID, n)
        }
    }

    private fun updateNotification(text: String) {
        runCatching {
            val manager = getSystemService(NotificationManager::class.java)
            manager.notify(NOTIFICATION_ID, notification(text))
        }
    }

    companion object {
        const val ACTION_START = "com.and.app.dpi.START"
        const val ACTION_STOP = "com.and.app.dpi.STOP"
        const val EXTRA_CONFIG = "com.and.app.dpi.CONFIG"
        private const val CHANNEL_ID = "and-dpi"
        private const val NOTIFICATION_ID = 41
    }
}

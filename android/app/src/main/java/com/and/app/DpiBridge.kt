package com.and.app

import android.Manifest
import android.app.Activity
import android.app.ActivityManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.net.VpnService
import android.util.Log
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import androidx.core.content.ContextCompat
import org.json.JSONObject
import java.io.File
import java.lang.ref.WeakReference

object DpiBridge {
    private const val REQ_VPN = 4101
    private const val REQ_NOTIFICATIONS = 4102
    private const val STATUS_FILE = "dpi-status.json"
    private const val KEEPALIVE_FILE = "proxy-keepalive"

    @Volatile private var activity = WeakReference<Activity>(null)
    @Volatile private var pendingConfig: String? = null

    fun attach(activity: Activity) {
        this.activity = WeakReference(activity)
    }

    fun requestPermissions() {
        val act = activity.get() ?: return
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            act.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            act.requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), REQ_NOTIFICATIONS)
        }
    }

    fun start(context: Context, config: String): Int {
        Log.d("AndDpi", "start requested")
        val prepare = VpnService.prepare(context)
        if (prepare != null) {
            val act = activity.get()
            if (act == null) {
                writeStatus(context, false, "Нет активного окна для запроса VPN")
                return -1
            }
            pendingConfig = config
            act.startActivityForResult(prepare, REQ_VPN)
            writeStatus(context, false, "Ожидается разрешение VPN")
            return 0
        }
        startService(context, config)
        return 1
    }

    fun onActivityResult(requestCode: Int, resultCode: Int) {
        if (requestCode != REQ_VPN) return
        val act = activity.get() ?: return
        if (resultCode == Activity.RESULT_OK) {
            val config = pendingConfig
            pendingConfig = null
            if (config != null) {
                startService(act, config)
            }
        } else {
            pendingConfig = null
            writeStatus(act, false, "denied: разрешение VPN не выдано")
        }
    }

    private fun startService(context: Context, config: String) {
        val intent = Intent(context, DpiVpnService::class.java)
            .setAction(DpiVpnService.ACTION_START)
            .putExtra(DpiVpnService.EXTRA_CONFIG, config)
        ContextCompat.startForegroundService(context, intent)
        maybeRequestBatteryExemption(context)
    }

    fun stop(context: Context) {
        Log.d("AndDpi", "stop requested")
        val intent = Intent(context, DpiVpnService::class.java).setAction(DpiVpnService.ACTION_STOP)
        runCatching { context.startService(intent) }
            .onFailure {
                Log.w("AndDpi", "startService stop failed, falling back to stopService", it)
                context.stopService(Intent(context, DpiVpnService::class.java))
            }
    }

    fun isRunning(context: Context): Boolean {
        val status = runCatching { readStatus(context) }.getOrNull() ?: return false
        if (!status.optBoolean("running", false)) return false
        val updated = status.optLong("updated", 0)
        if (System.currentTimeMillis() - updated >= 15_000) return false
        val am = context.getSystemService(ActivityManager::class.java) ?: return false
        @Suppress("DEPRECATION")
        return am.getRunningServices(Int.MAX_VALUE).any {
            it.service.className == DpiVpnService::class.java.name && it.pid > 0
        }
    }

    fun status(context: Context): String {
        val file = statusFile(context)
        return if (file.exists()) file.readText() else "{\"running\":false,\"error\":\"\"}"
    }

    fun openUrl(context: Context, url: String) {
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse(url))
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        context.startActivity(intent)
    }

    fun keepAlive(context: Context, enabled: Boolean) {
        val intent = Intent(context, ProxyKeepAliveService::class.java)
        if (enabled) {
            File(context.filesDir, KEEPALIVE_FILE).writeText("1")
            ContextCompat.startForegroundService(context, intent.setAction(ProxyKeepAliveService.ACTION_START))
        } else {
            File(context.filesDir, KEEPALIVE_FILE).delete()
            context.stopService(Intent(context, ProxyKeepAliveService::class.java))
        }
    }

    private fun maybeRequestBatteryExemption(context: Context) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) return
        val marker = File(context.filesDir, "battery-asked")
        if (marker.exists()) return
        val pm = context.getSystemService(PowerManager::class.java) ?: return
        if (pm.isIgnoringBatteryOptimizations(context.packageName)) return
        runCatching { marker.writeText("1") }
        val intent = Intent(
            Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
            Uri.parse("package:${context.packageName}")
        ).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        runCatching { context.startActivity(intent) }
    }

    fun statusFile(context: Context): File = File(context.filesDir, STATUS_FILE)

    fun writeStatus(context: Context, running: Boolean, error: String, port: Int = 1080) {
        runCatching {
            statusFile(context).writeText(
                JSONObject()
                    .put("running", running)
                    .put("error", error)
                    .put("port", port)
                    .put("updated", System.currentTimeMillis())
                    .toString()
            )
        }
    }

    private fun readStatus(context: Context): JSONObject {
        val file = statusFile(context)
        if (!file.exists()) return JSONObject().put("running", false).put("error", "")
        return JSONObject(file.readText())
    }
}

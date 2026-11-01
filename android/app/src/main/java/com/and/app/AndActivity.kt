package com.and.app

import android.content.Intent
import android.util.Log
import android.os.Bundle
import org.golang.app.GoNativeActivity

class AndActivity : GoNativeActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        DpiBridge.attach(this)
        DpiBridge.requestPermissions()
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        DpiBridge.onActivityResult(requestCode, resultCode)
    }

    // Called from Go through the JNI bridge.
    fun startDpi(config: String): Int { Log.d("AndDpi", "JNI startDpi"); return DpiBridge.start(this, config) }
    fun stopDpi() { Log.d("AndDpi", "JNI stopDpi"); DpiBridge.stop(this) }
    fun isDpiRunning(): Boolean = DpiBridge.isRunning(this)
    fun dpiStatus(): String = DpiBridge.status(this)
    fun openUrl(url: String) = DpiBridge.openUrl(this, url)
    fun keepAlive(enabled: Boolean) = DpiBridge.keepAlive(this, enabled)
    fun requestPermissions() = DpiBridge.requestPermissions()
}

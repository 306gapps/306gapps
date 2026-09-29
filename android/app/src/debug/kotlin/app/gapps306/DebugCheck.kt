package app.gapps306

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * Drives the picker headlessly so a device run can be checked from logcat:
 * am broadcast -n app.gapps306/.DebugCheck [--es variant core] [--ez build true] [--es name foo]
 */
class DebugCheck : BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        val done = goAsync()
        CoroutineScope(Dispatchers.IO).launch {
            try {
                Gapps.init(ctx)
                if (Gapps.picker.value.catalog == null) Gapps.loadReleases()
                intent.getStringExtra("variant")?.let { Gapps.applyVariant(it) }
                val p = Gapps.picker.first { !it.loading }
                Log.i(TAG, "releases=${p.releases.map { it.id }} error=${p.error}")
                Log.i(TAG, "release=${p.catalog?.release?.id} selection=${p.selection}")
                Log.i(TAG, "build=${Gapps.build.value} cache=${humanSize(Gapps.cacheSize())}")
                if (intent.getBooleanExtra("build", false)) {
                    BuildService.start(ctx, intent.getStringExtra("name").orEmpty())
                }
            } catch (e: Exception) {
                Log.e(TAG, "check failed", e)
            } finally {
                done.finish()
            }
        }
    }

    private companion object { const val TAG = "gapps306check" }
}

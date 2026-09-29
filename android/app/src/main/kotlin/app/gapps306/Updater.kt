package app.gapps306

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import android.util.Log
import androidx.core.content.FileProvider
import app.gapps306.mobile.Mobile
import app.gapps306.mobile.Progress
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable

// mirrors updateJSON in ../../mobile/update.go
@Serializable
data class Available(
    val version: String,
    val page: String,
    val apk: String,
)

sealed interface UpdateState {
    data object None : UpdateState
    data class Ready(val update: Available) : UpdateState
    data class Downloading(val update: Available, val frac: Float) : UpdateState
    data class Downloaded(val update: Available, val apk: File) : UpdateState
    data class Failed(val update: Available, val message: String) : UpdateState
}

/**
 * Offers a newer release and hands the apk to the package installer.
 *
 * There is no self-installing on Android: the system installer does it, with
 * the user confirming. The apk is signed with the same key as the running
 * build, so it updates in place rather than asking to uninstall first.
 */
object Updater {
    private const val TAG = "gapps306"

    private val _state = MutableStateFlow<UpdateState>(UpdateState.None)
    val state: StateFlow<UpdateState> = _state.asStateFlow()

    suspend fun check(current: String) = withContext(Dispatchers.IO) {
        // Never step on a download already running.
        if (_state.value !is UpdateState.None) return@withContext
        runCatching { Mobile.checkUpdate(current) }
            .onSuccess { raw ->
                if (raw.isNotEmpty()) {
                    _state.value = UpdateState.Ready(json.decodeFromString(raw))
                }
            }
            .onFailure { Log.w(TAG, "update check failed", it) }
    }

    suspend fun download(ctx: Context) = withContext(Dispatchers.IO) {
        val u = when (val s = _state.value) {
            is UpdateState.Ready -> s.update
            is UpdateState.Failed -> s.update
            else -> return@withContext
        }
        _state.value = UpdateState.Downloading(u, 0f)
        val dir = File(ctx.applicationContext.filesDir, "update")
        runCatching {
            Mobile.downloadUpdate(u.apk, dir.path, object : Progress {
                override fun update(line: String, frac: Double) {
                    _state.value = UpdateState.Downloading(u, frac.toFloat())
                }
            })
        }
            .onSuccess { _state.value = UpdateState.Downloaded(u, File(it)) }
            .onFailure {
                Log.w(TAG, "update download failed", it)
                _state.value = UpdateState.Failed(u, it.message ?: it.toString())
            }
    }

    /** True once the user has allowed this app to install packages. */
    fun canInstall(ctx: Context): Boolean =
        Build.VERSION.SDK_INT < Build.VERSION_CODES.O ||
            ctx.packageManager.canRequestPackageInstalls()

    fun permissionIntent(ctx: Context): Intent =
        Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
            Uri.parse("package:${ctx.packageName}"))

    fun installIntent(ctx: Context, apk: File): Intent {
        val uri = FileProvider.getUriForFile(ctx, "${ctx.packageName}.files", apk)
        return Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, "application/vnd.android.package-archive")
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
    }

    fun dismiss() {
        _state.value = UpdateState.None
    }
}

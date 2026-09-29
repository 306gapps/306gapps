package app.gapps306

import android.Manifest
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.FileProvider
import androidx.lifecycle.lifecycleScope
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class MainActivity : ComponentActivity() {
    private var pendingSave: File? = null

    private val saveZip = registerForActivityResult(
        ActivityResultContracts.CreateDocument("application/zip")
    ) { uri ->
        val src = pendingSave ?: return@registerForActivityResult
        if (uri == null) return@registerForActivityResult
        lifecycleScope.launch {
            val ok = withContext(Dispatchers.IO) {
                runCatching {
                    contentResolver.openOutputStream(uri, "w")!!.use { out ->
                        src.inputStream().use { it.copyTo(out, 1 shl 20) }
                    }
                }
            }
            toast(ok.fold({ "Saved ${src.name}" }, { "Could not save: ${it.message}" }))
        }
    }

    private var pendingName = ""

    // the build runs whether or not this is granted; it only decides whether you see it
    private val askNotify = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { BuildService.start(this, pendingName) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        Gapps.init(this)
        if (Gapps.picker.value.releases.isEmpty()) {
            lifecycleScope.launch { Gapps.loadReleases() }
        }
        lifecycleScope.launch { Updater.check(BuildConfig.VERSION_NAME) }
        setContent {
            AppTheme {
                PickerScreen(
                    onBuild = ::startBuild,
                    onInstallUpdate = ::installUpdate,
                    onSave = { r ->
                        pendingSave = File(r.path)
                        saveZip.launch(r.name)
                    },
                    onShare = ::share,
                )
            }
        }
    }

    // Android has no self-install: the system installer does it, and it needs
    // the user to have allowed this app to be a source first.
    private val askInstall = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { installUpdate() }

    private fun installUpdate() {
        val state = Updater.state.value
        if (state !is UpdateState.Downloaded) return
        if (!Updater.canInstall(this)) {
            toast("Allow installing apps from 306Gapps, then tap Install again")
            askInstall.launch(Updater.permissionIntent(this))
            return
        }
        startActivity(Updater.installIntent(this, state.apk))
    }

    private fun startBuild(name: String) {
        pendingName = name
        if (Build.VERSION.SDK_INT >= 33) askNotify.launch(Manifest.permission.POST_NOTIFICATIONS)
        else BuildService.start(this, name)
    }

    private fun share(r: BuildResult) {
        val uri = FileProvider.getUriForFile(this, "$packageName.files", File(r.path))
        val send = Intent(Intent.ACTION_SEND)
            .setType("application/zip")
            .putExtra(Intent.EXTRA_STREAM, uri)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        startActivity(Intent.createChooser(send, r.name))
    }

    private fun toast(s: String) = Toast.makeText(this, s, Toast.LENGTH_LONG).show()
}

@androidx.compose.runtime.Composable
private fun AppTheme(content: @androidx.compose.runtime.Composable () -> Unit) {
    val dark = isSystemInDarkTheme()
    val ctx = LocalContext.current
    val colors = when {
        Build.VERSION.SDK_INT >= 31 -> if (dark) dynamicDarkColorScheme(ctx) else dynamicLightColorScheme(ctx)
        dark -> darkColorScheme()
        else -> lightColorScheme()
    }
    MaterialTheme(colorScheme = colors, content = content)
}

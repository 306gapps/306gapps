package app.gapps306

import android.content.Context
import android.util.Log
import app.gapps306.mobile.Mobile
import app.gapps306.mobile.Progress
import app.gapps306.mobile.Session
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.withContext

data class Picker(
    val releases: List<Release> = emptyList(),
    val catalog: Catalog? = null,
    val selection: Selection = Selection(),
    val loading: Boolean = true,
    val error: String? = null,
)

sealed interface BuildStatus {
    data object Idle : BuildStatus
    data class Running(val line: String, val frac: Float) : BuildStatus
    data class Done(val result: BuildResult) : BuildStatus
    data class Failed(val message: String) : BuildStatus
}

/**
 * Process-wide, because the build outlives the activity: the service runs it,
 * the ui just watches.
 */
object Gapps {
    private const val TAG = "gapps306"

    private lateinit var session: Session
    private lateinit var prefs: android.content.SharedPreferences
    lateinit var outDir: File
        private set

    private val _picker = MutableStateFlow(Picker())
    val picker: StateFlow<Picker> = _picker.asStateFlow()

    private val _build = MutableStateFlow<BuildStatus>(BuildStatus.Idle)
    val build: StateFlow<BuildStatus> = _build.asStateFlow()

    @Synchronized
    fun init(ctx: Context) {
        if (::session.isInitialized) return
        val app = ctx.applicationContext
        outDir = File(app.filesDir, "out")
        prefs = app.getSharedPreferences("picker", Context.MODE_PRIVATE)
        session = Mobile.newSession(
            "",
            File(app.cacheDir, "downloads").path,
            // the signing key lives here; losing it only means a new one next build
            File(app.filesDir, "config").path,
        )
    }

    suspend fun loadReleases() = io {
        _picker.update { it.copy(loading = true, error = null) }
        runCatching { json.decodeFromString<List<Release>>(session.releases()) }
            .onSuccess { rs ->
                _picker.update { it.copy(releases = rs) }
                val want = prefs.getString("release", null)
                val pick = rs.firstOrNull { it.id == want } ?: rs.firstOrNull()
                if (pick != null) loadRelease(pick.id)
                else _picker.update { it.copy(loading = false, error = "No releases published yet") }
            }
            .onFailure { e ->
                Log.w(TAG, "load failed", e)
                _picker.update { it.copy(loading = false, error = e.message) }
            }
    }

    suspend fun loadRelease(id: String) = io {
        _picker.update { it.copy(loading = true, error = null) }
        runCatching { json.decodeFromString<Catalog>(session.load(id)) }
            .onSuccess { c ->
                prefs.edit().putString("release", id).apply()
                _picker.update {
                    it.copy(catalog = c, selection = decode(session.state()), loading = false)
                }
            }
            .onFailure { e ->
                Log.w(TAG, "load failed", e)
                _picker.update { it.copy(loading = false, error = e.message) }
            }
    }

    suspend fun toggle(id: String, on: Boolean) = select { session.toggle(id, on) }
    suspend fun setGroup(id: String, on: Boolean) = select { session.setGroup(id, on) }
    suspend fun setKeepStock(id: String, on: Boolean) = select { session.setKeepStock(id, on) }
    suspend fun applyVariant(id: String) = select { session.applyVariant(id) }

    private suspend fun select(op: () -> String) = io {
        val s = decode(op())
        _picker.update { it.copy(selection = s) }
    }

    /** Runs on the caller's thread for as long as the build takes. */
    private var pendingName = ""

    fun zipName(): String = session.zipName()

    fun runBuild(onProgress: (String, Float) -> Unit) {
        _build.value = BuildStatus.Running("Starting…", 0f)
        // one zip at a time; they're big
        outDir.deleteRecursively()
        var frac = 0f
        _build.value = try {
            val r = session.build(outDir.path, pendingName, object : Progress {
                override fun update(line: String, f: Double) {
                    if (f >= 0) frac = f.toFloat()
                    _build.value = BuildStatus.Running(line, frac)
                    onProgress(line, frac)
                }
            })
            BuildStatus.Done(json.decodeFromString(r))
        } catch (e: Exception) {
            val msg = e.message.orEmpty()
            if (msg.contains("context canceled")) BuildStatus.Idle
            else {
                Log.w(TAG, "build failed", e)
                BuildStatus.Failed(msg.ifEmpty { e.toString() })
            }
        }
    }

    fun starting(name: String) {
        pendingName = name
        _build.value = BuildStatus.Running("Starting…", 0f)
    }

    fun failed(message: String) {
        _build.value = BuildStatus.Failed(message)
    }

    fun cancelBuild() = session.cancel()

    fun dismissBuild() {
        if (_build.value !is BuildStatus.Running) _build.value = BuildStatus.Idle
    }

    fun cacheSize(): Long = session.cacheSize()

    suspend fun clearCache() = io { session.clearCache() }

    private fun decode(s: String) = json.decodeFromString<Selection>(s)

    private suspend fun <T> io(block: suspend () -> T): T = withContext(Dispatchers.IO) { block() }
}

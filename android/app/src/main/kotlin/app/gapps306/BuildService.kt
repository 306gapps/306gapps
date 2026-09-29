package app.gapps306

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.IBinder
import android.os.SystemClock
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import kotlin.concurrent.thread

/** Keeps the process alive while a build downloads, which can take a long while. */
class BuildService : Service() {
    private var worker: Thread? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_CANCEL) {
            Gapps.cancelBuild()
            return START_NOT_STICKY
        }
        if (worker != null) return START_NOT_STICKY

        Gapps.init(this)
        channel()
        ServiceCompat.startForeground(
            this, ONGOING, progress("Starting…", 0f),
            ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
        )

        val nm = getSystemService(NotificationManager::class.java)
        worker = thread(name = "build") {
            var last = 0L
            Gapps.runBuild { line, frac ->
                // the notification manager rate-limits; updates past ~5/s get dropped anyway
                val now = SystemClock.elapsedRealtime()
                if (now - last > 500) {
                    last = now
                    nm.notify(ONGOING, progress(line, frac))
                }
            }
            finished(nm)
            ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
        return START_NOT_STICKY
    }

    private fun finished(nm: NotificationManager) {
        val text = when (val s = Gapps.build.value) {
            is BuildStatus.Done -> s.result.name
            is BuildStatus.Failed -> "Build failed: ${s.message}"
            else -> return
        }
        nm.notify(
            FINISHED,
            NotificationCompat.Builder(this, CHANNEL)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle(if (Gapps.build.value is BuildStatus.Done) "Package ready" else "306Gapps")
                .setContentText(text)
                .setContentIntent(openApp())
                .setAutoCancel(true)
                .build(),
        )
    }

    private fun progress(line: String, frac: Float): Notification {
        val cancel = PendingIntent.getService(
            this, 0, Intent(this, BuildService::class.java).setAction(ACTION_CANCEL),
            PendingIntent.FLAG_IMMUTABLE,
        )
        return NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("Building package")
            .setContentText(line)
            .setProgress(1000, (frac * 1000).toInt(), false)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setContentIntent(openApp())
            .addAction(0, "Cancel", cancel)
            .build()
    }

    private fun openApp() = PendingIntent.getActivity(
        this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE,
    )

    private fun channel() {
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL, "Builds", NotificationManager.IMPORTANCE_LOW)
        )
    }

    companion object {
        private const val CHANNEL = "build"
        private const val ONGOING = 1
        private const val FINISHED = 2
        private const val ACTION_CANCEL = "app.gapps306.CANCEL"

        fun start(ctx: Context, name: String) {
            // show progress now, not once the service gets round to starting
            Gapps.starting(name)
            try {
                ContextCompat.startForegroundService(ctx, Intent(ctx, BuildService::class.java))
            } catch (e: IllegalStateException) {
                // ForegroundServiceStartNotAllowedException, e.g. started from the background
                Gapps.failed(e.message ?: e.toString())
            }
        }
    }
}

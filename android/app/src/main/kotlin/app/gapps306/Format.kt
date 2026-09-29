package app.gapps306

import java.util.Locale

enum class GroupCheck { ALL, SOME, NONE }

fun groupCheck(group: Group, sel: Selection): GroupCheck {
    val optional = group.packages.filterNot { it.required }
    if (optional.isEmpty()) return GroupCheck.ALL
    val on = optional.count { it.id in sel.set }
    return when (on) {
        optional.size -> GroupCheck.ALL
        0 -> GroupCheck.NONE
        else -> GroupCheck.SOME
    }
}

/** The line under a package name: why it's on, or what it is. */
fun packageNote(pkg: Package, sel: Selection, names: Map<String, String>): String {
    if (pkg.required) return "required"
    sel.implied[pkg.id]?.let { by ->
        return "needed by " + by.joinToString(", ") { names[it] ?: it }
    }
    return firstSentence(pkg.summary)
}

fun firstSentence(s: String): String {
    val end = s.indexOf(". ")
    return if (end < 0) s else s.substring(0, end + 1) + " …"
}

fun humanSize(n: Long): String {
    if (n < 1024) return "$n B"
    var div = 1024L
    var exp = 0
    var v = n / 1024
    while (v >= 1024) {
        div *= 1024
        exp++
        v /= 1024
    }
    return String.format(Locale.ROOT, "%.1f %cB", n.toDouble() / div, "KMGT"[exp])
}

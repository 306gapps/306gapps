package app.gapps306

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class FormatTest {
    private val core = Group(
        "core", "Core", size = 3, packages = listOf(
            Package("gmscore", "Play services", size = 1, required = true),
            Package("vending", "Play Store", "The store. Installs apps.", size = 1),
            Package("gsf", "Framework", size = 1),
        )
    )

    @Test
    fun groupCheckIgnoresRequired() {
        assertEquals(GroupCheck.NONE, groupCheck(core, Selection(listOf("gmscore"))))
        assertEquals(GroupCheck.SOME, groupCheck(core, Selection(listOf("gmscore", "vending"))))
        assertEquals(GroupCheck.ALL, groupCheck(core, Selection(listOf("gmscore", "vending", "gsf"))))
    }

    @Test
    fun noteExplainsWhyAPackageIsOn() {
        val names = mapOf("gsa" to "Google app")
        val sel = Selection(listOf("vending"), implied = mapOf("vending" to listOf("gsa", "x")))
        assertEquals("required", packageNote(core.packages[0], sel, names))
        assertEquals("needed by Google app, x", packageNote(core.packages[1], sel, names))
        assertEquals("The store. …", packageNote(core.packages[1], Selection(), names))
    }

    @Test
    fun sizesMatchTheDesktopFormat() {
        assertEquals("512 B", humanSize(512))
        assertEquals("1.0 KB", humanSize(1024))
        assertEquals("230.8 MB", humanSize(242_011_750))
        assertEquals("2.0 GB", humanSize(2_161_595_065))
    }

    @Test
    fun decodesWhatGoSends() {
        val s = json.decodeFromString<Selection>(
            """{"selected":["a","b"],"implied":{"b":["a"]},"count":2,"size":9,"variant":"core","error":""}"""
        )
        assertEquals(setOf("a", "b"), s.set)
        assertEquals(listOf("a"), s.implied["b"])
    }
}

// The updater decodes what mobile/update.go writes; a renamed field there
// would otherwise surface as a silent "no update available".
class UpdateJsonTest {
    @Test
    fun `decodes what the bridge emits`() {
        val raw = """
            {"version":"v0.3.1",
             "page":"https://github.com/306gapps/306gapps/releases/tag/v0.3.1",
             "apk":"https://github.com/306gapps/306gapps/releases/download/v0.3.1/306gapps-v0.3.1-android-arm64.apk",
             "size":11170087}
        """.trimIndent()
        val u = json.decodeFromString<Available>(raw)
        assertEquals("v0.3.1", u.version)
        assertTrue(u.apk.endsWith("-android-arm64.apk"))
    }
}

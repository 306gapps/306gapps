package app.gapps306

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

// mirrors the json in ../../mobile/mobile.go

@Serializable
data class Release(
    val id: String,
    val version: String,
    val api: Int,
    val device: String,
    val build: String,
) {
    val label get() = "Android $version · $device $build"
}

@Serializable
data class Catalog(
    val release: Release,
    val groups: List<Group>,
    val variants: List<Variant>,
) {
    fun names(): Map<String, String> =
        groups.flatMap { it.packages }.associate { it.id to it.name }
}

@Serializable
data class Group(
    val id: String,
    val name: String,
    val summary: String = "",
    val size: Long,
    val packages: List<Package>,
)

@Serializable
data class Package(
    val id: String,
    val name: String,
    val summary: String = "",
    val size: Long,
    val required: Boolean = false,
    val experimental: Boolean = false,
)

@Serializable
data class Variant(val id: String, val name: String, val summary: String = "")

@Serializable
data class Selection(
    val selected: List<String> = emptyList(),
    val implied: Map<String, List<String>> = emptyMap(),
    val count: Int = 0,
    val size: Long = 0,
    val variant: String = "",
    val error: String = "",
) {
    @kotlinx.serialization.Transient
    val set: Set<String> = selected.toSet()
}

@Serializable
data class BuildResult(
    val path: String,
    val name: String,
    val size: Long,
    val files: Int,
    val sha256: String,
)

val json = Json { ignoreUnknownKeys = true }

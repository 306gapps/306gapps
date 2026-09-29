plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

// -PgappsVersion=v0.3.0 from the release workflow, so the apk matches the desktop binaries
val gappsVersion = (findProperty("gappsVersion") as String?)?.removePrefix("v") ?: "0.0.0-dev"
// 0.3.0 -> 300. branch builds and the 0.0.0 default get 1, android rejects 0
val gappsVersionCode = (Regex("""^(\d+)\.(\d+)\.(\d+)""").find(gappsVersion)
    ?.destructured?.let { (a, b, c) -> a.toInt() * 10000 + b.toInt() * 100 + c.toInt() } ?: 1)
    .coerceAtLeast(1)

// release signing only when ci (or you) supplies the keystore; otherwise release builds are unsigned
val keystore = System.getenv("GAPPS_KEYSTORE")?.let(::file)?.takeIf { it.exists() }

android {
    namespace = "app.gapps306"
    compileSdk = 37

    defaultConfig {
        applicationId = "app.gapps306"
        minSdk = 28
        targetSdk = 36
        versionCode = gappsVersionCode
        versionName = gappsVersion
        // core.aar is bound for arm64 only
        ndk { abiFilters += "arm64-v8a" }
    }

    buildFeatures { compose = true }

    signingConfigs {
        if (keystore != null) {
            create("release") {
                storeFile = keystore
                storePassword = System.getenv("GAPPS_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("GAPPS_KEY_ALIAS")
                keyPassword = System.getenv("GAPPS_KEYSTORE_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            if (keystore != null) signingConfig = signingConfigs.getByName("release")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    // built by ../build-core.sh, not checked in
    implementation(files("libs/core.aar"))

    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.material3)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(libs.junit)
}

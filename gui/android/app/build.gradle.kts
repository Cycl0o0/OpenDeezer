plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    namespace = "fr.cyclooo.opendeezer"
    compileSdk = 34

    defaultConfig {
        applicationId = "fr.cyclooo.opendeezer"
        minSdk = 24
        targetSdk = 34
        versionCode = 21
        versionName = "2.2.3"
    }

    // Release signing is driven by env vars set by CI from repo secrets (see
    // .github/workflows/release.yml + docs/ANDROID_SIGNING.md). The keystore is
    // PKCS12; its single password is used for both store and key. Absent locally,
    // so a plain `assembleRelease` on a dev machine just produces an unsigned APK.
    signingConfigs {
        create("release") {
            val ksPath = System.getenv("ANDROID_KEYSTORE_FILE")
            if (ksPath != null && file(ksPath).exists()) {
                storeFile = file(ksPath)
                storeType = "PKCS12"
                storePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("ANDROID_KEY_ALIAS")
                keyPassword = System.getenv("ANDROID_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
            // Sign with the release key only when the keystore env is present (CI
            // with secrets); otherwise leave unsigned so local builds still work.
            if (System.getenv("ANDROID_KEYSTORE_FILE") != null) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
    }

    // Two form factors from one engine: the touch phone/tablet app and a
    // D-pad-driven Android TV app. Shared code lives in src/main; each flavor
    // supplies its own launcher activity + manifest (src/mobile, src/tv).
    flavorDimensions += "device"
    productFlavors {
        create("mobile") {
            dimension = "device"
        }
        create("tv") {
            dimension = "device"
            applicationIdSuffix = ".tv"
            versionNameSuffix = "-tv"
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    buildFeatures {
        compose = true
    }

    packaging {
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
    }
}

dependencies {
    // OpenDeezer Go engine, bound by gomobile (built by CI into libs/odmobile.aar).
    implementation(files("libs/odmobile.aar"))

    val composeBom = platform("androidx.compose:compose-bom:2024.09.03")
    implementation(composeBom)
    androidTestImplementation(composeBom)

    implementation("androidx.core:core-ktx:1.13.1")
    // MediaSessionCompat + MediaStyle notification for the playback service.
    implementation("androidx.media:media:1.7.0")
    implementation("androidx.activity:activity-compose:1.9.2")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.6")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.8.6")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.6")

    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-graphics")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")

    implementation("androidx.navigation:navigation-compose:2.8.0")

    // Foldable posture (FoldingFeature: tabletop/book) for the split layouts.
    implementation("androidx.window:window:1.5.1")

    implementation("io.coil-kt:coil-compose:2.7.0")

    debugImplementation("androidx.compose.ui:ui-tooling")
}

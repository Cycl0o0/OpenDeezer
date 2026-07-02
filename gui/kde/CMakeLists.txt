cmake_minimum_required(VERSION 3.16)
project(opendeezer-kde LANGUAGES CXX C)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# List every Qt component explicitly (Qt >= 6.7.2 requires it).
# DBus powers the MPRIS2 media-controls bridge (system overlay / media keys).
# WebEngineWidgets is used ONLY by the standalone opendeezer-login helper
# (src/loginhelper.cpp) — NOT by the main app/lib, so QtWebEngine never loads
# inside the dlopen'd unified-launcher backend (where it crashed). Needs the
# qt6-webengine-dev package.
# LinguistTools provides lrelease (compiles the committed i18n/*.ts catalogs to
# .qm at build time) and lupdate. On Ubuntu the CMake config + tools ship in the
# qt6-l10n-tools package (add it to the CI apt line alongside qt6-base-dev).
find_package(Qt6 REQUIRED COMPONENTS Widgets Concurrent DBus WebEngineWidgets LinguistTools)
find_package(Threads REQUIRED)
qt_standard_project_setup()            # enables AUTOMOC/UIC/RCC

set(OPENDEEZER_SRCS
    src/main.cpp
    src/mainwindow.cpp
    src/mpris.cpp
    src/settingsdialog.cpp
    src/logindialog.cpp      # login chooser; spawns the opendeezer-login helper
    resources.qrc            # embeds the app icon (opendeezer.png)
)

qt_add_executable(opendeezer-kde ${OPENDEEZER_SRCS} src/standalone.cpp)

# Qt backend for the unified Linux client (gui/linux launcher dlopen's this).
qt_add_library(opendeezer-qt SHARED ${OPENDEEZER_SRCS})
target_include_directories(opendeezer-qt PRIVATE ${CMAKE_SOURCE_DIR}/lib)
target_link_libraries(opendeezer-qt PRIVATE
    Qt6::Widgets Qt6::Concurrent Qt6::DBus
    deezercore Threads::Threads asound ${CMAKE_DL_LIBS} m)

# Standalone login helper: runs the QtWebEngine Deezer login out-of-process so it
# works in the dlopen'd unified launcher and can never crash the main app.
qt_add_executable(opendeezer-login src/loginhelper.cpp)
target_link_libraries(opendeezer-login PRIVATE Qt6::Widgets Qt6::WebEngineWidgets)
install(TARGETS opendeezer-login RUNTIME DESTINATION bin)

# The generated C header (libdeezercore.h) lives in lib/, next to the archive.
target_include_directories(opendeezer-kde PRIVATE ${CMAKE_SOURCE_DIR}/lib)

# The Go engine, built by build.sh into lib/libdeezercore.a.
add_library(deezercore STATIC IMPORTED)
set_target_properties(deezercore PROPERTIES
    IMPORTED_LOCATION ${CMAKE_SOURCE_DIR}/lib/libdeezercore.a)

# Static archives are processed left-to-right, so deezercore MUST precede the
# system libs it pulls in. The Go c-archive (with oto/v3 + ALSA) needs:
#   -lasound (oto's #cgo pkg-config: alsa)  -lpthread -ldl -lm  (Go runtime).
# It does NOT need crypto/curl: the Deezer client + Blowfish are pure Go.
target_link_libraries(opendeezer-kde PRIVATE
    Qt6::Widgets
    Qt6::Concurrent
    Qt6::DBus
    deezercore
    Threads::Threads      # -pthread
    asound
    ${CMAKE_DL_LIBS}      # -ldl
    m
)

install(TARGETS opendeezer-kde RUNTIME DESTINATION bin)

# ---- Translations (Qt Linguist) -------------------------------------------
# main.cpp / loginhelper.cpp install a QTranslator that loads
# ":/i18n/opendeezer_<lang>" for QLocale::system(); English has no catalog and
# falls through to the source strings, and Arabic also flips the layout to RTL.
#
# lrelease compiles each committed i18n/*.ts into opendeezer_<lang>.qm in the
# build tree, and the .qm are embedded under the resource prefix /i18n. This is
# done explicitly (rather than via qt_add_translations) so the SAME six catalogs
# can be shared by all three targets — the standalone exe, the dlopen'd
# unified-launcher lib, and the out-of-process login helper — with a single
# lrelease rule per file (three qt_add_translations calls would each try to
# produce the same .qm and collide). Refresh the catalogs from the code with
# `lupdate src/*.cpp -ts i18n/opendeezer_<lang>.ts` (never needed for a build).
set(OPENDEEZER_TS_FILES
    ${CMAKE_CURRENT_SOURCE_DIR}/i18n/opendeezer_zh_CN.ts
    ${CMAKE_CURRENT_SOURCE_DIR}/i18n/opendeezer_hi.ts
    ${CMAKE_CURRENT_SOURCE_DIR}/i18n/opendeezer_es.ts
    ${CMAKE_CURRENT_SOURCE_DIR}/i18n/opendeezer_fr.ts
    ${CMAKE_CURRENT_SOURCE_DIR}/i18n/opendeezer_ar.ts
    ${CMAKE_CURRENT_SOURCE_DIR}/i18n/opendeezer_ru.ts
)

set(OPENDEEZER_QM_FILES)
foreach(_ts ${OPENDEEZER_TS_FILES})
    get_filename_component(_qm_name ${_ts} NAME_WE)
    set(_qm "${CMAKE_CURRENT_BINARY_DIR}/${_qm_name}.qm")
    add_custom_command(
        OUTPUT ${_qm}
        COMMAND Qt6::lrelease ${_ts} -qm ${_qm}
        DEPENDS ${_ts}
        COMMENT "lrelease ${_qm_name}.qm"
        VERBATIM)
    list(APPEND OPENDEEZER_QM_FILES ${_qm})
endforeach()

# Embed the compiled catalogs into each binary at :/i18n/opendeezer_<lang>.qm.
# BASE = the build dir so the resource alias is just the .qm basename; each call
# uses a unique resource name so the three targets don't clash.
qt_add_resources(opendeezer-kde   "i18n_kde"
    PREFIX "/i18n" BASE "${CMAKE_CURRENT_BINARY_DIR}" FILES ${OPENDEEZER_QM_FILES})
qt_add_resources(opendeezer-qt    "i18n_qt"
    PREFIX "/i18n" BASE "${CMAKE_CURRENT_BINARY_DIR}" FILES ${OPENDEEZER_QM_FILES})
qt_add_resources(opendeezer-login "i18n_login"
    PREFIX "/i18n" BASE "${CMAKE_CURRENT_BINARY_DIR}" FILES ${OPENDEEZER_QM_FILES})

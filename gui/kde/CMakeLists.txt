cmake_minimum_required(VERSION 3.16)
project(opendeezer-kde LANGUAGES CXX C)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# List every Qt component explicitly (Qt >= 6.7.2 requires it).
# DBus powers the MPRIS2 media-controls bridge (system overlay / media keys).
find_package(Qt6 REQUIRED COMPONENTS Widgets Concurrent DBus)
find_package(Threads REQUIRED)
qt_standard_project_setup()            # enables AUTOMOC/UIC/RCC

qt_add_executable(opendeezer-kde
    src/main.cpp
    src/mainwindow.cpp
    src/mpris.cpp
    src/settingsdialog.cpp
    resources.qrc            # embeds the app icon (opendeezer.png)
)

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

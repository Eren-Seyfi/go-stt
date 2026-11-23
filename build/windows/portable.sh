#!/usr/bin/env bash
# Builds a portable (no installer) Windows x64 folder from Linux with MinGW-w64:
#
#   bin/windows/stt-app-windows-x64/
#     stt-app.exe            the app (CPU whisper.cpp via DLLs)
#     whisper.dll, ggml*.dll whisper.cpp
#     llama-server.exe       llama.cpp server, statically linked
#     lib*.dll               MinGW runtime (gcc, stdc++, winpthread, gomp)
#
# Requirements (Arch): mingw-w64-gcc cmake ninja go nodejs npm wails3
# Usage: build/windows/portable.sh [--skip-libs]
set -euo pipefail
# Locale-independent text handling: under tr_TR, ${x,,} lowercases "I" to the
# dotless "ı", so "ADVAPI32.dll" would never match "advapi32.dll".
export LC_ALL=C

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

CROSS=x86_64-w64-mingw32
MINGW_BIN=/usr/$CROSS/bin
TP="$ROOT/third_party"
WHISPER_WIN="$TP/whisper-windows"
LLAMA_WIN="$TP/llama-windows"
OUT="$ROOT/bin/windows/stt-app-windows-x64"
JOBS="$(nproc)"

# Distributed binaries must not use -march=native: target AVX2/FMA/F16C
# (Intel Haswell 2013+, AMD Zen / Excavator), which covers nearly every PC.
CPU_FLAGS=(-DGGML_NATIVE=OFF -DGGML_AVX=ON -DGGML_AVX2=ON -DGGML_FMA=ON -DGGML_F16C=ON -DGGML_BMI2=ON)
TOOLCHAIN=(
  -DCMAKE_SYSTEM_NAME=Windows
  -DCMAKE_C_COMPILER=$CROSS-gcc
  -DCMAKE_CXX_COMPILER=$CROSS-g++
  -DCMAKE_RC_COMPILER=$CROSS-windres
  -DCMAKE_FIND_ROOT_PATH=/usr/$CROSS
  -DCMAKE_FIND_ROOT_PATH_MODE_PROGRAM=NEVER
  -DCMAKE_FIND_ROOT_PATH_MODE_LIBRARY=ONLY
  -DCMAKE_FIND_ROOT_PATH_MODE_INCLUDE=ONLY
  -DCMAKE_BUILD_TYPE=Release
  -DGGML_CCACHE=OFF
)

build_libs() {
  echo "==> whisper.cpp (Windows, CPU, shared)"
  cmake -S "$TP/whisper-src" -B "$TP/whisper-src/build-win" -G Ninja \
    "${TOOLCHAIN[@]}" "${CPU_FLAGS[@]}" \
    -DBUILD_SHARED_LIBS=ON -DGGML_CUDA=OFF \
    -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_EXAMPLES=OFF -DWHISPER_BUILD_SERVER=OFF \
    -DCMAKE_INSTALL_PREFIX="$WHISPER_WIN"
  cmake --build "$TP/whisper-src/build-win" -j"$JOBS"
  cmake --install "$TP/whisper-src/build-win"

  echo "==> llama.cpp llama-server (Windows, CPU, static)"
  # Static so it carries its own ggml and cannot clash with whisper's ggml.dll.
  cmake -S "$TP/llama-src" -B "$TP/llama-src/build-win" -G Ninja \
    "${TOOLCHAIN[@]}" "${CPU_FLAGS[@]}" \
    -DBUILD_SHARED_LIBS=OFF -DGGML_CUDA=OFF \
    -DLLAMA_CURL=OFF -DLLAMA_OPENSSL=OFF \
    -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF \
    -DCMAKE_EXE_LINKER_FLAGS="-static" \
    -DCMAKE_INSTALL_PREFIX="$LLAMA_WIN"
  cmake --build "$TP/llama-src/build-win" -j"$JOBS" --target llama-server
  mkdir -p "$LLAMA_WIN/bin"
  cp "$TP/llama-src/build-win/bin/llama-server.exe" "$LLAMA_WIN/bin/"
}

build_app() {
  echo "==> frontend + bindings"
  wails3 task common:build:frontend

  echo "==> Windows resources (icon, manifest, version info)"
  (cd build && wails3 generate syso -arch amd64 -icon windows/icon.ico \
    -manifest windows/wails.exe.manifest -info windows/info.json \
    -out ../wails_windows_amd64.syso)
  trap 'rm -f "$ROOT"/wails_windows_amd64.syso' EXIT

  echo "==> stt-app.exe"
  mkdir -p "$OUT"
  # whisper.go's own -L points at the Linux libs; MinGW's ld never picks a
  # .so, so the Windows import libraries below are the ones that get used.
  GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=$CROSS-gcc CXX=$CROSS-g++ \
  CGO_CFLAGS="-I$WHISPER_WIN/include" \
  CGO_LDFLAGS="-L$WHISPER_WIN/lib" \
    go build -tags production -trimpath -buildvcs=false \
      -ldflags="-w -s -H windowsgui" -o "$OUT/stt-app.exe" .
}

package() {
  echo "==> collecting DLLs"
  cp "$WHISPER_WIN"/bin/*.dll "$OUT/"
  cp "$LLAMA_WIN/bin/llama-server.exe" "$OUT/"
  for dll in libgcc_s_seh-1.dll libstdc++-6.dll libwinpthread-1.dll libgomp-1.dll; do
    cp "$MINGW_BIN/$dll" "$OUT/"
  done

  # Every DLL any shipped binary imports must be in the folder or be a
  # Windows system DLL.
  echo "==> checking imports"
  local missing=0
  for f in "$OUT"/*.exe "$OUT"/*.dll; do
    while read -r dll; do
      [[ -e "$OUT/$dll" ]] && continue
      [[ -e "$OUT/${dll,,}" ]] && continue
      case "${dll,,}" in
        kernel32.dll|user32.dll|gdi32.dll|advapi32.dll|shell32.dll|ole32.dll|oleaut32.dll|\
        ws2_32.dll|winmm.dll|comdlg32.dll|comctl32.dll|uxtheme.dll|dwmapi.dll|shlwapi.dll|\
        version.dll|bcrypt.dll|crypt32.dll|secur32.dll|ntdll.dll|msvcrt.dll|iphlpapi.dll|\
        dbghelp.dll|setupapi.dll|powrprof.dll|userenv.dll|imm32.dll|winspool.drv|\
        api-ms-win-*|ucrtbase.dll|mswsock.dll|dnsapi.dll|netapi32.dll|wtsapi32.dll|\
        psapi.dll|rpcrt4.dll|shcore.dll|dxgi.dll|d3d11.dll|wininet.dll|winhttp.dll) ;;
        *) echo "  EKSİK: $(basename "$f") -> $dll"; missing=1 ;;
      esac
    done < <($CROSS-objdump -p "$f" | awk '/DLL Name:/{print $3}')
  done
  [[ $missing == 0 ]] && echo "  tüm bağımlılıklar mevcut"

  cat > "$OUT/BENİOKU.txt" <<'EOF'
STT App — Windows taşınabilir sürüm
===================================

Kurulum gerekmez: klasörü istediğiniz yere kopyalayıp stt-app.exe'yi çalıştırın.

- Ayarlar, geçmiş ve modeller bu klasörde tutulur (database.sqlite, models\).
  Klasörü yazma izni olan bir yere koyun (ör. Belgeler, Masaüstü);
  "C:\Program Files" içine koymayın.
- Arayüz Microsoft Edge WebView2 kullanır. Windows 10/11'de genelde kuruludur;
  değilse: https://developer.microsoft.com/microsoft-edge/webview2/
- Konuşma tanıma bu sürümde CPU ile çalışır (AVX2 destekli işlemci gerekir).
- llama-server.exe yerel GGUF modellerini çalıştırmak içindir.

BİLİNEN KISITLAMALAR
- Mikrofon kaydı Windows'ta henüz desteklenmiyor (Linux'a özgü pactl/parecord
  kullanılıyor). Yapay zekâ sohbeti ve dosya ekleme çalışır.
- "Klasörü aç" düğmeleri çalışmaz.
- Uygulama çökerse llama-server.exe açık kalabilir; Görev Yöneticisi'nden kapatın.
EOF

  if command -v zip >/dev/null; then
    (cd "$(dirname "$OUT")" && rm -f "$(basename "$OUT").zip" && zip -qr "$(basename "$OUT").zip" "$(basename "$OUT")")
  else
    (cd "$(dirname "$OUT")" && bsdtar -a -cf "$(basename "$OUT").zip" "$(basename "$OUT")")
  fi
  echo "==> hazır: $OUT"
  echo "           $OUT.zip"
}

[[ "${1:-}" == "--skip-libs" ]] || build_libs
build_app
package

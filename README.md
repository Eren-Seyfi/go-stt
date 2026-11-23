# STT App — Konuşmadan Yazıya + Yapay Zekâ Asistanı

Mikrofondan gelen sesi **tamamen yerelde** (internet gerekmeden) yazıya döken, sesli komutlarla yönetilebilen ve isteğe bağlı olarak yerel ya da bulut tabanlı yapay zekâ modelleriyle sohbet edebilen bir masaüstü uygulaması.

- **Arayüz:** [Wails v3](https://v3.wails.io/) (Go) + Svelte 5 + Tailwind CSS
- **Konuşma tanıma:** [whisper.cpp](https://github.com/ggml-org/whisper.cpp) (cgo ile doğrudan bağlanır, CUDA destekli)
- **Yerel dil modeli:** [llama.cpp](https://github.com/ggml-org/llama.cpp) `llama-server` (uygulama gerektiğinde arka planda başlatır)
- **Veritabanı:** SQLite (`modernc.org/sqlite`, saf Go)

---

## İçindekiler

1. [Özellikler](#özellikler)
2. [Platform desteği](#platform-desteği)
3. [Depo yapısı](#depo-yapısı)
4. [Linux kurulumu (önerilen)](#linux-kurulumu-önerilen)
5. [whisper.cpp ve llama.cpp'yi kaynaktan derleme](#whispercpp-ve-llamacppyi-kaynaktan-derleme)
6. [Windows kurulumu (deneysel)](#windows-kurulumu-deneysel)
7. [macOS](#macos)
8. [Modeller](#modeller)
9. [Yapay zekâ sağlayıcıları](#yapay-zekâ-sağlayıcıları)
10. [Ortam değişkenleri](#ortam-değişkenleri)
11. [Veriler nerede saklanır?](#veriler-nerede-saklanır)
12. [Sorun giderme](#sorun-giderme)
13. [Geliştirici notları](#geliştirici-notları)

---

## Özellikler

- **Yerel konuşma tanıma:** Whisper modelleri (tiny → large-v3-turbo) ile, ses hiçbir sunucuya gönderilmeden yazıya dökülür.
- **GPU hızlandırma:** NVIDIA ekran kartı varsa CUDA ile; yoksa CPU ile çalışır.
- **Ses etkinlik algılama (VAD):** Silero VAD modeli ile sessiz bölümler atlanır.
- **Sesli komutlar:** Kullanıcının tanımladığı ifadelerle
  - *uyandırma* (yazmaya başla),
  - *uyutma* (yazmayı durdur ama dinlemeye devam et),
  - *durdurma* (mikrofonu kapat).
  Eşleştirme Türkçe kurallarına uygundur (I/ı, İ/i, "Ada'ya" gibi ekler, noktalama yok sayılır).
- **Yapay zekâ sohbeti:** Anthropic, OpenAI, Google Gemini, OpenRouter, Groq, Mistral, DeepSeek, xAI, Ollama ve yerel `llama.cpp`.
- **Yerel GGUF modelleri:** Hugging Face'ten doğrudan indirme veya LM Studio'dan içe aktarma.
- **Ek dosyalar:** Sohbete dosya eklenebilir; ses/video dosyaları `ffmpeg` ile çözülür.

---

## Platform desteği

| Özellik | Linux | Windows | macOS |
| --- | --- | --- | --- |
| Kodun derlenmesi | ✅ | ✅ (cgo + MinGW ile) | ⚠️ denenmedi |
| Hazır derlenmiş kütüphaneler (`third_party/whisper`, `llama`) | ✅ x86-64 + CUDA 13 | ❌ yeniden derlenmeli | ❌ yeniden derlenmeli |
| Mikrofon kaydı | ✅ (`pactl` / `parecord`) | ✅ (Windows waveIn API) | ❌ henüz yok |
| Klasör / dosya açma | ✅ (`xdg-open`) | ❌ henüz yok | ❌ henüz yok |
| Yerel `llama-server` | ✅ | ✅ (`llama-server.exe`) | ⚠️ denenmedi |
| Uygulama çökünce `llama-server`'ın da kapanması | ✅ | ❌ elle kapatılmalı | ❌ elle kapatılmalı |

> **Özetle:** Uygulama **Linux için geliştirilmiştir** ve Linux'ta tüm özellikleriyle çalışır. **Windows'ta** konuşma tanıma (mikrofon kaydı dahil) ve yapay zekâ sohbeti çalışır; konuşma tanıma Windows'ta şimdilik yalnızca CPU ile yapılır. Birkaç yardımcı özellik ("klasörü aç" düğmeleri gibi) henüz yalnızca Linux'tadır. macOS için ses kayıt katmanı yoktur.

---

## Depo yapısı

```
stt-app/
├── main.go                 Uygulamanın giriş noktası
├── backend/                Go arka ucu
│   ├── whisper.go          whisper.cpp cgo bağlantısı
│   ├── llama.go            llama-server yönetimi (ortak kod)
│   ├── llama_proc_linux.go     ┐
│   ├── llama_proc_windows.go   ├ işletim sistemine özel süreç yönetimi
│   ├── llama_proc_other.go     ┘
│   ├── audio.go            Ortak ses kodu (seviye ölçer, kayıt arayüzü)
│   ├── audio_pulse.go      Linux mikrofon kaydı (pactl/parecord)
│   ├── audio_windows.go    Windows mikrofon kaydı (winmm waveIn)
│   ├── ai.go               Yapay zekâ sağlayıcıları
│   ├── models.go           Whisper model indirme
│   ├── llm_models.go       GGUF model indirme / içe aktarma
│   ├── wake.go             Sesli komut eşleştirme
│   └── paths.go            Veri klasörleri
├── frontend/               Svelte 5 arayüzü
├── build/                  Wails derleme betikleri (linux, windows, darwin, …)
├── third_party/
│   ├── whisper/            Derlenmiş whisper.cpp — CUDA (uygulama buna bağlanır)
│   ├── whisper-cpu/        Derlenmiş whisper.cpp — yalnızca CPU (yedek)
│   ├── llama/              Derlenmiş llama.cpp (bin/llama-server dahil)
│   ├── whisper-src/        whisper.cpp kaynak kodu (upstream 96ec4d9)
│   └── llama-src/          llama.cpp kaynak kodu (upstream 87f816e)
├── models/                 (git'te yok) Whisper ve GGUF modelleri
└── database.sqlite         Ayarlar, geçmiş, yapay zekâ ayarları
```

**Önemli:** `third_party/whisper`, `whisper-cpu` ve `llama` klasörlerindeki dosyalar **Linux x86-64** için, **CUDA 13.5** ile ve **CUDA mimarisi 86** (RTX 30xx serisi) hedeflenerek derlenmiştir. Farklı bir işletim sistemi, farklı bir ekran kartı veya farklı CUDA sürümü kullanıyorsanız [kaynaktan yeniden derleyin](#whispercpp-ve-llamacppyi-kaynaktan-derleme).

---

## Linux kurulumu (önerilen)

### 1. Sistem paketleri

Uygulama **GTK 4** ve **WebKitGTK 6.0** kullanır. Ayrıca ses için PulseAudio araçlarına (PipeWire kullanan sistemlerde `pipewire-pulse` üzerinden çalışır) ihtiyaç vardır.

**Arch Linux / CachyOS / Manjaro**
```bash
sudo pacman -S --needed go nodejs npm gcc cmake git \
  gtk4 webkitgtk-6.0 libpulse ffmpeg xdg-utils
# PipeWire kullanıyorsanız (çoğu güncel kurulum):
sudo pacman -S --needed pipewire-pulse
```

**Ubuntu 24.04+ / Debian 13+**
```bash
sudo apt install golang nodejs npm build-essential cmake git \
  libgtk-4-dev libwebkitgtk-6.0-dev pulseaudio-utils ffmpeg xdg-utils pkg-config
```
> Ubuntu deposundaki Go sürümü eskiyse [go.dev/dl](https://go.dev/dl/) adresinden güncel sürümü kurun (proje **Go 1.26+** ister).

**Fedora**
```bash
sudo dnf install golang nodejs npm gcc gcc-c++ cmake git \
  gtk4-devel webkitgtk6.0-devel pulseaudio-utils ffmpeg xdg-utils
```
> Fedora'da `ffmpeg` için RPM Fusion deposunu etkinleştirmeniz gerekebilir.

Kurulumdan sonra ses araçlarının çalıştığını kontrol edin:
```bash
pactl get-default-source     # varsayılan mikrofonun adını yazmalı
```

### 2. NVIDIA GPU (isteğe bağlı)

- Güncel NVIDIA sürücüsü kurulu olmalı; `nvidia-smi -L` komutu kartınızı listelemelidir. Uygulama GPU'yu bu komutla algılar.
- Hazır kütüphaneler **CUDA 13** çalışma zamanı kütüphanelerine (`libcudart`, `libcublas`) ihtiyaç duyar:
  - Arch/CachyOS: `sudo pacman -S cuda`
  - Ubuntu/Fedora: NVIDIA'nın [CUDA Toolkit](https://developer.nvidia.com/cuda-downloads) deposundan kurun.
- GPU'nuz yoksa veya CUDA kurmak istemiyorsanız [CPU sürümünü](#gpu-yok--cuda-hataları) kullanın.

### 3. Wails CLI

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
# ~/go/bin PATH'te değilse ekleyin:
export PATH="$PATH:$(go env GOPATH)/bin"

wails3 doctor   # eksik bağımlılıkları gösterir
```

### 4. Depoyu klonlama

```bash
git clone git@github.com:Eren-Seyfi/go-stt.git stt-app
cd stt-app
```

> Depo büyüktür (derlenmiş kütüphaneler + whisper.cpp ve llama.cpp kaynakları, yaklaşık 1 GB). Modeller depoda **yoktur**, uygulama içinden indirilir.

### 5. Arayüz bağımlılıkları

```bash
cd frontend && npm install && cd ..
```

### 6. Çalıştırma

**Geliştirme modu** (arayüz ve Go kodu değiştikçe otomatik yeniden yüklenir):
```bash
wails3 dev
```

**Üretim derlemesi:**
```bash
wails3 build
./bin/stt-app
```

### 7. İlk açılış

1. **Ayarlar → Modeller** bölümünden bir Whisper modeli indirin. Başlangıç için `small` veya `large-v3-turbo` önerilir.
2. Silero VAD modelini indirin (sessizlikleri atlamak için).
3. Mikrofonu seçip kayda başlayın.

---

## whisper.cpp ve llama.cpp'yi kaynaktan derleme

Aşağıdaki durumlarda kütüphaneleri yeniden derlemeniz gerekir:
- Farklı bir NVIDIA kartınız varsa (ör. RTX 40xx = mimari `89`, RTX 50xx = `120`, RTX 20xx = `75`),
- Farklı bir CUDA sürümü kullanıyorsanız,
- GPU'nuz yoksa ve CPU sürümünü güncellemek istiyorsanız,
- Windows veya macOS kullanıyorsanız,
- whisper.cpp / llama.cpp'yi güncellemek istiyorsanız.

Kaynak kodlar depoda hazır olarak gelir: `third_party/whisper-src` ve `third_party/llama-src`.

### whisper.cpp — CUDA

```bash
cd third_party/whisper-src
cmake -B build-cuda \
  -DCMAKE_BUILD_TYPE=Release \
  -DBUILD_SHARED_LIBS=ON \
  -DGGML_CUDA=ON \
  -DCMAKE_CUDA_ARCHITECTURES=86 \
  -DCMAKE_INSTALL_PREFIX="$PWD/../whisper"
cmake --build build-cuda -j"$(nproc)"
cmake --install build-cuda
cd ../..
```
> `CMAKE_CUDA_ARCHITECTURES` değerini kartınıza göre değiştirin. Kartınızın değerini `nvidia-smi --query-gpu=compute_cap --format=csv` ile öğrenebilirsiniz (ör. `8.6` → `86`).

### whisper.cpp — yalnızca CPU

```bash
cd third_party/whisper-src
cmake -B build-cpu -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=ON \
  -DGGML_CUDA=OFF -DCMAKE_INSTALL_PREFIX="$PWD/../whisper-cpu"
cmake --build build-cpu -j"$(nproc)"
cmake --install build-cpu
cd ../..
```
> GPU'su olmayan bir bilgisayarda doğrudan `../whisper` klasörüne kurabilirsiniz; böylece uygulama CPU sürümüyle derlenir.

### llama.cpp (`llama-server`)

```bash
cd third_party/llama-src
cmake -B build-cuda \
  -DCMAKE_BUILD_TYPE=Release \
  -DBUILD_SHARED_LIBS=ON \
  -DGGML_CUDA=ON \
  -DCMAKE_CUDA_ARCHITECTURES=86 \
  -DCMAKE_INSTALL_PREFIX="$PWD/../llama"
cmake --build build-cuda -j"$(nproc)" --target llama-server
cmake --install build-cuda
cd ../..
```
> GPU yoksa `-DGGML_CUDA=OFF` kullanın.

Derlemeden sonra uygulamayı yeniden derleyin (`wails3 build`). Derleme çıktıları (`build-cuda/` vb.) git tarafından yok sayılır.

---

## Windows kurulumu (deneysel)

> **Hazır sürüm:** Kurulum gerektirmeyen Windows sürümü depoda hazırdır: `bin/windows/stt-app-windows-x64.zip`. Zip'i açıp `stt-app.exe`'yi çalıştırmanız yeterlidir; aşağıdaki adımlar yalnızca **Windows'ta kendiniz derlemek** isterseniz gereklidir.
>
> Mikrofon kaydı Windows'un yerleşik `winmm` waveIn API'siyle yapılır; ek program veya sürücü gerekmez. Windows, mikrofonun kendi formatını (ör. 48 kHz stereo) whisper'ın istediği 16 kHz mono formatına kendisi dönüştürür. Mikrofon listesinde Windows'un varsayılan kayıt cihazı en üstte gelir. Cihaz adları Windows tarafından 31 karaktere kısaltılır; seçilen mikrofon sonradan çıkarılırsa kayıt varsayılan cihazdan yapılır.
>
> Windows'ta uygulamanın mikrofona erişebilmesi için **Ayarlar → Gizlilik ve güvenlik → Mikrofon** altında "Masaüstü uygulamalarının mikrofonunuza erişmesine izin verin" seçeneği açık olmalıdır.

### Linux'tan Windows sürümü üretme

Taşınabilir Windows klasörü Linux'ta MinGW-w64 ile çapraz derlenerek üretilir:

```bash
# Arch/CachyOS: sudo pacman -S mingw-w64-gcc ninja
build/windows/portable.sh              # whisper.cpp + llama.cpp + uygulama
build/windows/portable.sh --skip-libs  # yalnızca uygulamayı yeniden derle
```

Çıktı `bin/windows/stt-app-windows-x64/` klasörü ve aynı adlı `.zip` dosyasıdır. Betik whisper.cpp'yi DLL olarak (`third_party/whisper-windows`), `llama-server.exe`'yi statik olarak (`third_party/llama-windows`) derler, MinGW çalışma zamanı DLL'lerini ekler ve tüm DLL bağımlılıklarının klasörde olduğunu doğrular. Dağıtım için `-march=native` yerine AVX2 hedeflenir (2013 sonrası Intel/AMD işlemciler).

### 1. Gerekli araçlar

| Araç | Nereden | Not |
| --- | --- | --- |
| Go 1.26+ | [go.dev/dl](https://go.dev/dl/) | |
| Node.js 20+ | [nodejs.org](https://nodejs.org/) | |
| Git | [git-scm.com](https://git-scm.com/) | Ayarlar için aşağıya bakın |
| MSYS2 + MinGW-w64 GCC | [msys2.org](https://www.msys2.org/) | cgo için C derleyicisi **şart** |
| CMake + Ninja | MSYS2 içinden | whisper.cpp derlemek için |
| WebView2 Runtime | Windows 10/11'de genelde kurulu | Wails arayüzü için |
| ffmpeg (isteğe bağlı) | [ffmpeg.org](https://ffmpeg.org/) | PATH'e eklenmeli |

MSYS2 kurulduktan sonra **MSYS2 UCRT64** terminalinde:
```bash
pacman -S --needed mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-cmake mingw-w64-ucrt-x86_64-ninja
```
Ardından `C:\msys64\ucrt64\bin` klasörünü Windows **PATH** ortam değişkenine ekleyin ve yeni bir PowerShell açıp kontrol edin:
```powershell
gcc --version
cmake --version
```

### 2. Git ayarları (klonlamadan önce)

```powershell
git config --global core.longpaths true     # llama-src içinde 260 karakteri aşan yollar var
git config --global core.autocrlf input     # .sh betiklerinin bozulmaması için
```
> Depodaki `.so` kısayolları (symlink) Windows'ta küçük metin dosyalarına dönüşür. Linux kütüphaneleri Windows'ta zaten kullanılmadığı için bu sorun yaratmaz.

### 3. Klonlama ve Wails CLI

```powershell
git clone git@github.com:Eren-Seyfi/go-stt.git stt-app
cd stt-app
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
cd frontend; npm install; cd ..
```

### 4. whisper.cpp'yi Windows için derleme

`third_party/whisper` içindeki Linux kütüphanelerinin yerine Windows sürümü derlenmelidir. Go'nun cgo'su MinGW GCC kullandığı için whisper.cpp'yi de **MinGW ile** derlemek en sorunsuz yoldur (CPU sürümü):

```powershell
cd third_party\whisper-src
cmake -B build-win -G Ninja -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=ON `
  -DGGML_CUDA=OFF -DCMAKE_INSTALL_PREFIX="$PWD\..\whisper"
cmake --build build-win
cmake --install build-win
cd ..\..
```

Bu komut `third_party\whisper\lib` altına `.dll.a` bağlantı dosyalarını, `third_party\whisper\bin` altına da `whisper.dll`, `ggml.dll`, `ggml-base.dll`, `ggml-cpu.dll` dosyalarını kurar.

> **CUDA ile Windows'ta:** CUDA derleyicisi (`nvcc`) Windows'ta yalnızca MSVC (Visual Studio Build Tools) ile çalışır. MSVC ile derlenen DLL'ler MinGW'den de kullanılabilir, çünkü whisper.cpp'nin arayüzü saf C'dir. Ancak bu yol denenmemiştir, başlangıç için CPU sürümünü öneririz.

### 5. `llama-server.exe` (isteğe bağlı)

En kolay yol, llama.cpp'nin hazır Windows sürümünü kullanmaktır:
1. [llama.cpp sürümler sayfasından](https://github.com/ggml-org/llama.cpp/releases) `llama-…-bin-win-cpu-x64.zip` (veya NVIDIA için `…-win-cuda-…-x64.zip`) dosyasını indirin.
2. Zip içeriğini `third_party\llama\bin\` klasörüne **ya da** derlenmiş `stt-app.exe`'nin yanına çıkarın.
3. Alternatif olarak tam yolu ortam değişkeniyle verin:
   ```powershell
   $env:STT_LLAMA_SERVER = "C:\araclar\llama\llama-server.exe"
   ```

Uygulama Windows'ta `llama-server.exe` dosyasını şu sırayla arar: `STT_LLAMA_SERVER` → exe'nin yanı → `<exe>\llama\bin\` → `third_party\llama\bin\` → `PATH`.

### 6. Derleme

Windows derleme betiği cgo'yu varsayılan olarak **kapalı** tutar. Bu uygulama whisper.cpp'ye cgo ile bağlandığı için cgo'yu **açık** olarak belirtmelisiniz:

```powershell
$env:CGO_ENABLED = "1"
wails3 build CGO_ENABLED=1
```

### 7. DLL'leri exe'nin yanına kopyalama

Windows'ta Linux'taki `rpath` karşılığı yoktur; DLL'ler exe'nin yanında veya PATH'te olmalıdır:

```powershell
Copy-Item third_party\whisper\bin\*.dll bin\
# MinGW çalışma zamanı DLL'leri de gerekebilir:
Copy-Item C:\msys64\ucrt64\bin\libgcc_s_seh-1.dll, C:\msys64\ucrt64\bin\libstdc++-6.dll, C:\msys64\ucrt64\bin\libwinpthread-1.dll, C:\msys64\ucrt64\bin\libgomp-1.dll bin\
.\bin\stt-app.exe
```
> "… .dll bulunamadı" hatası alırsanız eksik DLL'in adını MSYS2'nin `ucrt64\bin` klasöründe arayıp `bin\` klasörüne kopyalayın.

### Windows'ta bilinen kısıtlamalar

- **Konuşma tanıma yalnızca CPU ile** çalışır (CUDA sürümü Linux'tan çapraz derlenemez; Windows'ta MSVC + CUDA ile derlenmelidir).
- **"Klasörü aç" düğmeleri çalışmaz** (`xdg-open` kullanılıyor; Windows karşılığı `explorer`).
- **LM Studio'dan model içe aktarma** `cp --reflink` kullandığı için çalışmayabilir.
- **Uygulama çökerse `llama-server.exe` açık kalır.** Görev Yöneticisi'nden kapatın. (Linux'taki `Pdeathsig` karşılığı Windows'ta "Job Object" ile eklenebilir.)
- Önceki oturumdan kalan `llama-server` süreçleri otomatik temizlenmez.

---

## macOS

macOS'ta kod derlenecek şekilde ayrılmıştır (`llama_proc_other.go`), ancak **denenmemiştir**. Ses kaydı macOS'ta `audio_pulse.go` (PulseAudio araçları) üzerinden denenir; bu araçlar macOS'ta genelde bulunmadığından mikrofon için macOS'a özel bir kayıt katmanı (CoreAudio) gerekir. Denemek isterseniz:

- Xcode Command Line Tools: `xcode-select --install`
- Homebrew ile: `brew install go node cmake ffmpeg`
- whisper.cpp ve llama.cpp'yi **Metal** desteğiyle derleyin (macOS'ta varsayılan olarak açıktır; `-DGGML_CUDA=ON` vermeyin).

---

## Modeller

Modeller git deposunda **yoktur** (`/models/` `.gitignore`'dadır) ve uygulama içinden indirilir.

### Whisper (konuşma tanıma) — `models/whisper/`

Hugging Face'teki `ggerganov/whisper.cpp` deposundan indirilir:

| Model | Boyut | Not |
| --- | --- | --- |
| `tiny`, `base` | ~30–60 MB | Hızlı, düşük doğruluk |
| `small` | ~180 MB | Dengeli |
| `medium` | ~500 MB | İyi doğruluk |
| `large-v3-turbo` | ~550 MB | **Önerilen**: büyük model doğruluğu, yüksek hız |
| `large-v3` | ~1 GB | En yüksek doğruluk, GPU önerilir |

**VAD:** `ggml-silero-v6.2.0.bin` (ve v5.1.2), `ggml-org/whisper-vad` deposundan indirilir.

Modelleri elle de koyabilirsiniz: `.bin` dosyasını `models/whisper/` klasörüne kopyalamanız yeterlidir.

### GGUF (yerel dil modelleri) — `models/llm/`

- **Hugging Face:** Uygulamada depo adını girin (ör. `unsloth/Qwen3-8B-GGUF`) ve dosya seçin. Erişim izni gereken (gated) depolar için `HF_TOKEN` ortam değişkenini ayarlayın.
- **LM Studio:** LM Studio kuruluysa (`lms` komutu PATH'teyse) indirdiğiniz modeller içe aktarılabilir.
- Görsel destekli modellerde `mmproj` dosyası otomatik bulunur.

---

## Yapay zekâ sağlayıcıları

Ayarlar ekranından sağlayıcı seçilir; API anahtarları veritabanında saklanır. Anahtarı ortam değişkeniyle de verebilirsiniz:

| Sağlayıcı | Ortam değişkeni | Not |
| --- | --- | --- |
| Anthropic (Claude) | `ANTHROPIC_API_KEY` | |
| OpenAI | `OPENAI_API_KEY` | |
| Google Gemini | `GEMINI_API_KEY` | |
| OpenRouter | `OPENROUTER_API_KEY` | |
| Groq | `GROQ_API_KEY` | |
| Mistral | `MISTRAL_API_KEY` | |
| DeepSeek | `DEEPSEEK_API_KEY` | |
| xAI (Grok) | `XAI_API_KEY` | |
| Ollama | — | `http://localhost:11434` üzerinde çalışıyor olmalı |
| llama.cpp (yerel) | — | Uygulama `llama-server`'ı kendisi başlatır |

---

## Ortam değişkenleri

| Değişken | Açıklama |
| --- | --- |
| `STT_APP_DIR` | Tüm verilerin (veritabanı, modeller, ekler) tutulacağı klasör. Verilmezse proje klasörü / exe'nin bulunduğu klasör kullanılır. |
| `STT_LLAMA_SERVER` | `llama-server` dosyasının tam yolu. |
| `HF_TOKEN` | Hugging Face erişim anahtarı (gated modeller için). |
| `*_API_KEY` | Yapay zekâ sağlayıcı anahtarları (yukarıdaki tabloya bakın). |
| `LD_LIBRARY_PATH` | (Linux) whisper kütüphanelerini farklı bir klasörden yüklemek için; [Sorun giderme](#sorun-giderme) bölümüne bakın. |

---

## Veriler nerede saklanır?

Uygulama tüm verisini **kendi klasöründe** tutar (`~/.local/share` gibi ortak yerlere yazmaz):

| Yol | İçerik |
| --- | --- |
| `database.sqlite` (+ `-shm`, `-wal`) | Ayarlar, konuşma geçmişi, yapay zekâ ayarları ve **API anahtarları** |
| `models/whisper/` | Whisper modelleri |
| `models/llm/` | GGUF modelleri |
| `attachments/` | Sohbete eklenen dosyalar |
| `.webkit/` | Arayüzün tarayıcı verileri (önbellek vb.) |

Klasör şu sırayla belirlenir: `STT_APP_DIR` → exe `<proje>/bin/` içindeyse proje klasörü → exe'nin bulunduğu klasör `go.mod` içeriyorsa orası → çalışma dizini → exe'nin klasörü.

> ⚠️ **Gizlilik uyarısı:** `database.sqlite` şu an git'e dahildir ve API anahtarlarınızı ve konuşma geçmişinizi içerebilir. Depo herkese açıksa veritabanını `.gitignore`'a geri eklemeyi ve `git rm --cached database.sqlite*` ile depodan çıkarmayı düşünün.

---

## Sorun giderme

### `libwhisper.so.1: cannot open shared object file`

Go, derleme sırasında kütüphane yolunu **mutlak yol** olarak programın içine yazar (`RUNPATH`, ör. `/home/<kullanıcı>/…/third_party/whisper/lib`). Bu yüzden derlenmiş `bin/stt-app` dosyasını başka bir klasöre veya bilgisayara taşırsanız kütüphaneleri bulamaz. Çözümler:
- Uygulamayı yeni konumunda yeniden derleyin (`wails3 build`), **veya**
- Kütüphane yolunu elle verin:
  ```bash
  LD_LIBRARY_PATH="$PWD/third_party/whisper/lib" ./bin/stt-app
  ```

### GPU yok / CUDA hataları

`libcudart.so.13`, `libcublas` bulunamadı veya `CUDA error` hataları alırsanız CPU kütüphaneleriyle çalıştırın. İki klasördeki kütüphaneler aynı adları taşıdığı için yeniden derlemeye gerek yoktur:
```bash
LD_LIBRARY_PATH="$PWD/third_party/whisper-cpu/lib" ./bin/stt-app
```
Kalıcı çözüm için whisper.cpp'yi [CPU sürümüyle](#whispercpp--yalnızca-cpu) `third_party/whisper` klasörüne derleyin.

### Kartım farklı, CUDA çalışmıyor (`no kernel image is available`)

Hazır kütüphaneler yalnızca CUDA mimarisi 86 için derlenmiştir. [Kaynaktan derlerken](#whispercpp-ve-llamacppyi-kaynaktan-derleme) `-DCMAKE_CUDA_ARCHITECTURES` değerini kartınıza göre verin.

### Mikrofon listesi boş

- `pactl list sources short` komutu mikrofonları listelemeli.
- PipeWire kullanıyorsanız `pipewire-pulse` servisinin çalıştığından emin olun: `systemctl --user status pipewire-pulse`.
- Flatpak/sandbox içinde çalıştırıyorsanız ses sunucusuna erişim izni verin.

### `llama-server bulunamadı`

`third_party/llama/bin/llama-server` dosyasının var olduğundan ve çalıştırılabilir olduğundan emin olun (`chmod +x`), ya da `STT_LLAMA_SERVER` ile tam yolu verin.

### Önceki oturumdan kalan `llama-server`

Linux'ta uygulama açılırken eski `llama-server` süreçlerini otomatik kapatır. Elle kapatmak için:
```bash
pkill -f 'llama-server.*--alias yerel'
```

### Dosya eklerken "ffmpeg bulunamadı"

Ses/video ekleri için `ffmpeg` kurulu ve PATH'te olmalıdır.

### `wails3: command not found`

`$(go env GOPATH)/bin` klasörünü PATH'e ekleyin.

---

## Geliştirici notları

### Kaynak kodlar neden doğrudan depoda?

`third_party/whisper-src` ve `third_party/llama-src`, submodule yerine **doğrudan `main` dalına** eklenmiştir; böylece depoyu tek komutla klonlamak yeterlidir. Upstream git bilgileri yerelde `.git-orig` klasöründe saklanır (git tarafından yok sayılır).

**Upstream'den güncelleme:**
```bash
cd third_party/whisper-src
mv .git-orig .git
git pull
mv .git .git-orig
cd ../..
git add third_party/whisper-src
git commit -m "whisper.cpp güncellendi"
```
> `.git-orig` yalnızca bu kaynakları ilk ekleyen makinede vardır. Yeni bir klonda güncellemek için kaynak klasörü silip upstream'den yeniden kopyalayın.

### Platforma özel kod

- `backend/whisper.go` → `#cgo linux` / `#cgo darwin` satırları `rpath` ekler; Windows'ta `rpath` yoktur.
- `backend/llama_proc_*.go` → süreç yönetimi Go'nun dosya adı kuralıyla (`_linux.go`, `_windows.go`) veya `//go:build` satırıyla seçilir.
- Windows için çapraz tip kontrolü (MinGW kuruluysa):
  ```bash
  GOOS=windows CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go vet ./backend
  ```

### Yapılacaklar (Windows/macOS desteği için)

- [x] Windows mikrofon kayıt katmanı (`audio_windows.go`, winmm waveIn).
- [ ] macOS mikrofon kayıt katmanı (CoreAudio).
- [ ] Windows'ta CUDA destekli whisper.cpp derlemesi.
- [ ] `xdg-open` yerine platforma göre `explorer` / `open`.
- [ ] Windows'ta Job Object ile `llama-server`'ın uygulamayla birlikte kapanması.
- [ ] `cp --reflink` yerine taşınabilir dosya kopyalama.

package backend

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Everything the app stores lives inside its own folder (the project
// directory), never in ~/.local/share or other shared places:
//
//	database.sqlite      settings, transcript, AI config and keys
//	models/whisper/      speech recognition models
//	models/llm/          local language models (GGUF)
//	attachments/         files attached to chat messages
//	.webkit/             the window's own browser data (cache, storage)

var appDir = sync.OnceValue(func() string {
	if d := os.Getenv("STT_APP_DIR"); d != "" {
		if abs, err := filepath.Abs(d); err == nil {
			return abs
		}
		return d
	}
	isProject := func(d string) bool {
		_, err := os.Stat(filepath.Join(d, "go.mod"))
		return err == nil
	}
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			dir := filepath.Dir(exe)
			if parent := filepath.Dir(dir); filepath.Base(dir) == "bin" && isProject(parent) {
				return parent // <project>/bin/stt-app
			}
			if isProject(dir) {
				return dir
			}
		}
	}
	if _, file, _, ok := runtime.Caller(0); ok { // built from these sources
		if d := filepath.Dir(filepath.Dir(file)); isProject(d) {
			return d
		}
	}
	if wd, err := os.Getwd(); err == nil && isProject(wd) {
		return wd
	}
	if exe, err := os.Executable(); err == nil { // installed elsewhere: next to the binary
		return filepath.Dir(exe)
	}
	return "."
})

// AppDir is the folder that holds all of the app's data.
func AppDir() string { return appDir() }

// DataDir is kept for older call sites: the app folder itself.
func DataDir() string { return AppDir() }

// DBPath is the SQLite database.
func DBPath() string { return filepath.Join(AppDir(), "database.sqlite") }

// ModelsDir holds the speech recognition (Whisper) models.
func ModelsDir() string { return filepath.Join(AppDir(), "models", "whisper") }

// LLMDir holds local language models (GGUF), one folder per model.
func LLMDir() string { return filepath.Join(AppDir(), "models", "llm") }

// AttachmentsDir holds files attached to chat messages.
func AttachmentsDir() string { return filepath.Join(AppDir(), "attachments") }

func webkitDir() string { return filepath.Join(AppDir(), ".webkit") }

// ---- environment ----------------------------------------------------------

var (
	hostEnvOnce sync.Once
	hostEnv     []string // the environment as the app was started with
)

// SetupDirs must run first thing in main, before the UI starts: it moves
// data from the old shared location into the app folder and points the
// webview's own storage (which GLib places under XDG_DATA_HOME /
// XDG_CACHE_HOME) into the app folder too.
func SetupDirs() {
	hostEnvOnce.Do(func() { hostEnv = os.Environ() })
	legacy := legacyDataDir()
	for _, d := range []string{ModelsDir(), LLMDir(), AttachmentsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Printf("klasör oluşturulamadı %s: %v", d, err)
		}
	}
	_ = os.Chmod(AttachmentsDir(), 0o700)
	migrateLegacy(legacy)
	migrateLLMLayout()
	if c := legacyCacheDir(); !strings.HasPrefix(c, AppDir()) {
		moveDirContents(c, filepath.Join(webkitDir(), "cache", "stt-app"))
	}

	data, cache := filepath.Join(webkitDir(), "data"), filepath.Join(webkitDir(), "cache")
	_ = os.MkdirAll(data, 0o700)
	_ = os.MkdirAll(cache, 0o700)
	os.Setenv("XDG_DATA_HOME", data)
	os.Setenv("XDG_CACHE_HOME", cache)
}

// HostEnv is the environment the app was started with, for programs we run
// (xdg-open must see the user's real XDG folders, not the webview's).
func HostEnv() []string {
	hostEnvOnce.Do(func() { hostEnv = os.Environ() })
	return hostEnv
}

// hostCommand is exec.Command with the original environment.
func hostCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env = HostEnv()
	return cmd
}

// ---- moving data from the old location -------------------------------------

func legacyDataDir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "stt-app")
}

// moveFile renames src to dst unless dst already exists.
func moveFile(src, dst string) {
	if _, err := os.Lstat(src); err != nil {
		return
	}
	if _, err := os.Lstat(dst); err == nil {
		return // already there: keep the newer copy in the app folder
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		log.Printf("taşınamadı %s: %v", src, err)
		return
	}
	if err := os.Rename(src, dst); err != nil {
		log.Printf("taşınamadı %s → %s: %v", src, dst, err)
	}
}

// moveDirContents moves every entry of src into dst.
func moveDirContents(src, dst string) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return
	}
	for _, e := range entries {
		moveFile(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()))
	}
	_ = os.Remove(src) // only succeeds once empty
}

func migrateLegacy(old string) {
	if old == AppDir() || strings.HasPrefix(AppDir(), old+string(os.PathSeparator)) {
		return
	}
	if _, err := os.Stat(old); err != nil {
		return
	}
	log.Printf("veriler %s konumundan %s klasörüne taşınıyor", old, AppDir())
	// The database with its WAL files: SQLite pairs them by name, so they
	// must be renamed together.
	if _, err := os.Stat(DBPath()); err != nil {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			moveFile(filepath.Join(old, "stt-app.db"+suffix), DBPath()+suffix)
		}
	}
	moveDirContents(filepath.Join(old, "models"), ModelsDir())
	moveDirContents(filepath.Join(old, "attachments"), AttachmentsDir())
	// The webview's storage, now kept under .webkit/data/<app name>.
	for _, d := range []string{"storage", "mediakeys"} {
		moveDirContents(filepath.Join(old, d), filepath.Join(webkitDir(), "data", "stt-app", d))
	}
	_ = os.Remove(old)
}

func legacyCacheDir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "stt-app")
}

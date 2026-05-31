package weftdriverplugin

// launch_test.go covers the host-side launch helpers (locate, isExecutable,
// and Launch's error paths). Spinning up a real plugin subprocess would test
// Launch's happy path, but that requires building a tiny driver binary in
// TestMain — the value isn't worth the complexity since go-plugin itself has
// extensive tests and weft-driver-vz/qemu integration tests cover that end.
// See the "Untestable without subprocess" note in the package README.

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	plugin "github.com/hashicorp/go-plugin"
)

func TestIsExecutable(t *testing.T) {
	dir := t.TempDir()

	// non-existent
	if isExecutable(filepath.Join(dir, "nope")) {
		t.Errorf("non-existent path reported executable")
	}

	// directory
	if isExecutable(dir) {
		t.Errorf("directory reported executable")
	}

	// regular file, non-executable
	nonExec := filepath.Join(dir, "plain")
	if err := os.WriteFile(nonExec, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isExecutable(nonExec) {
		t.Errorf("0644 file reported executable")
	}

	// regular file, executable bit set
	exe := filepath.Join(dir, "exe")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// chmod is a no-op on windows; perm bits don't carry the meaning we test
		t.Skip("perm-bit semantics differ on windows")
	}
	if !isExecutable(exe) {
		t.Errorf("0755 file reported non-executable")
	}
}

func TestLocate_EmptyName(t *testing.T) {
	_, err := locate("", nil)
	if err == nil || !strings.Contains(err.Error(), "empty executable name") {
		t.Errorf("expected empty-name error, got %v", err)
	}
}

func TestLocate_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "abs-exe")
	mustExe(t, exe)

	if runtime.GOOS == "windows" {
		t.Skip("perm-bit semantics differ on windows")
	}

	got, err := locate(exe, nil)
	if err != nil {
		t.Fatalf("locate(abs): %v", err)
	}
	if got != exe {
		t.Errorf("locate(abs) = %q, want %q", got, exe)
	}
}

func TestLocate_AbsolutePath_NotExecutable(t *testing.T) {
	dir := t.TempDir()
	notExe := filepath.Join(dir, "abs-notexe")
	if err := os.WriteFile(notExe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("perm-bit semantics differ on windows")
	}
	_, err := locate(notExe, nil)
	if err == nil || !strings.Contains(err.Error(), "not an executable") {
		t.Errorf("expected not-executable error, got %v", err)
	}
}

func TestLocate_PathWithSeparatorVerbatim(t *testing.T) {
	// A name containing a separator is treated as a path, used as-is.
	dir := t.TempDir()
	exe := filepath.Join(dir, "rel-exe")
	mustExe(t, exe)
	if runtime.GOOS == "windows" {
		t.Skip("perm-bit semantics differ on windows")
	}

	// supply a path with a separator
	got, err := locate(exe, nil)
	if err != nil {
		t.Fatalf("locate(path): %v", err)
	}
	if got != exe {
		t.Errorf("locate(path) = %q, want %q", got, exe)
	}
}

func TestLocate_SearchDirsHitFirst(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("perm-bit semantics differ on windows")
	}
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	wanted := filepath.Join(dir1, "weft-driver-fake")
	other := filepath.Join(dir2, "weft-driver-fake")
	mustExe(t, wanted)
	mustExe(t, other)

	got, err := locate("weft-driver-fake", []string{dir1, dir2})
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if got != wanted {
		t.Errorf("locate found %q, want %q (search-dir order broken)", got, wanted)
	}
}

func TestLocate_EnvPluginDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("perm-bit semantics differ on windows")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "weft-driver-envdir")
	mustExe(t, exe)
	t.Setenv(EnvPluginDir, dir)

	got, err := locate("weft-driver-envdir", nil)
	if err != nil {
		t.Fatalf("locate via env: %v", err)
	}
	if got != exe {
		t.Errorf("locate via env = %q, want %q", got, exe)
	}
}

func TestLocate_NotFound(t *testing.T) {
	t.Setenv(EnvPluginDir, t.TempDir())
	_, err := locate("definitely-no-such-binary-xyzzy-987", []string{t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestLaunch_EmptyExecutableName(t *testing.T) {
	_, _, err := Launch(LaunchOptions{Executable: ""})
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestLaunch_MissingExecutable(t *testing.T) {
	_, _, err := Launch(LaunchOptions{
		Executable: "weft-driver-nonexistent-xyzzy",
		SearchDirs: []string{t.TempDir()},
	})
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestLaunch_NotAPluginBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script trick is unix-only")
	}
	// Build a faux 'executable' that exits without doing the go-plugin
	// handshake. Launch should fail with a handshake error and not leak the
	// child process — Kill() is on the failure path.
	dir := t.TempDir()
	exe := filepath.Join(dir, "fake-plugin")
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	_, client, err := Launch(LaunchOptions{
		Executable: exe,
		HostUUID:   "h",
		Hostname:   "host",
		AZ:         "az",
		StateDir:   t.TempDir(),
	})
	if err == nil {
		if client != nil {
			client.Kill()
		}
		t.Fatalf("expected handshake failure, got nil")
	}
	if !strings.Contains(err.Error(), "handshake") {
		t.Logf("Launch error (non-fatal, may vary): %v", err)
	}
}

// TestCleanup is a smoke test — Cleanup is a thin wrapper over go-plugin's
// CleanupClients. We just confirm it doesn't panic when called with no
// managed clients registered.
func TestCleanup(t *testing.T) {
	Cleanup()
}

// mustExe writes a 0755 file at p so isExecutable returns true.
func mustExe(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Compile-time guard: keep the plugin import live so the package builds even
// if some launch path stops referencing it (defensive against future edits).
var _ = plugin.HandshakeConfig{}

// Sentinel: confirm Handshake constants are stable. A change to ProtocolVersion
// or MagicCookieValue is a wire-breaking event and should be deliberate.
func TestHandshakeConstants(t *testing.T) {
	if Handshake.ProtocolVersion != 1 {
		t.Errorf("ProtocolVersion drift: %d", Handshake.ProtocolVersion)
	}
	if Handshake.MagicCookieKey != "WEFT_DRIVER_PLUGIN" {
		t.Errorf("MagicCookieKey drift: %q", Handshake.MagicCookieKey)
	}
	if Handshake.MagicCookieValue == "" {
		t.Errorf("MagicCookieValue empty")
	}
	if PluginName != "weft_driver" {
		t.Errorf("PluginName drift: %q", PluginName)
	}
}

// TestEnvVarNames pins the env-var contract between host and plugin. Renaming
// these silently breaks every existing plugin binary in the wild.
func TestEnvVarNames(t *testing.T) {
	cases := map[string]string{
		EnvHostUUID:  "WEFT_DRIVER_HOST_UUID",
		EnvHostname:  "WEFT_DRIVER_HOSTNAME",
		EnvAZ:        "WEFT_DRIVER_AZ",
		EnvStateDir:  "WEFT_DRIVER_STATE_DIR",
		EnvPluginDir: "WEFT_PLUGIN_DIR",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("env var name drift: got %q want %q", got, want)
		}
	}
	// And shut the compiler up about unused errors helper.
	_ = errors.New
}

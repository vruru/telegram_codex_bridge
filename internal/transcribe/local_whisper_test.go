package transcribe

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWhisperInstallRoot(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "data", "whisper-venv")
	if got := whisperInstallRoot(root); got != want {
		t.Fatalf("whisperInstallRoot() = %q, want %q", got, want)
	}
}

func TestStatusPrefersPrivateVenv(t *testing.T) {
	root := t.TempDir()
	privateBin := filepath.Join(root, "data", "whisper-venv", "bin")
	pathBin := t.TempDir()

	mustWriteExecutable(t, filepath.Join(pathBin, "ffmpeg"), "#!/bin/sh\nexit 0\n")
	mustWriteExecutable(t, filepath.Join(privateBin, "python"), "#!/bin/sh\nif [ \"$1\" = \"-c\" ]; then\n  echo \"20250625\"\n  exit 0\nfi\nexit 1\n")
	mustWriteExecutable(t, filepath.Join(privateBin, "whisper"), "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"20250625\"\n  exit 0\nfi\nexit 0\n")

	withTempPATH(t, pathBin, func() {
		status := Status(context.Background(), root)
		if !status.Installed {
			t.Fatalf("expected installed status, got %#v", status)
		}
		if !status.Available {
			t.Fatalf("expected available status, got %#v", status)
		}
		if status.Source != whisperSourceAppManaged {
			t.Fatalf("expected app-managed source, got %q", status.Source)
		}
		if status.WhisperPath != filepath.Join(privateBin, "whisper") {
			t.Fatalf("expected private whisper path, got %q", status.WhisperPath)
		}
	})
}

func TestStatusDetectsExternalWhisper(t *testing.T) {
	root := t.TempDir()
	pathBin := t.TempDir()

	mustWriteExecutable(t, filepath.Join(pathBin, "ffmpeg"), "#!/bin/sh\nexit 0\n")
	mustWriteExecutable(t, filepath.Join(pathBin, "whisper"), "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"external-1\"\n  exit 0\nfi\nif [ \"$1\" = \"--help\" ]; then\n  echo \"help\"\n  exit 0\nfi\nexit 0\n")

	withTempPATH(t, pathBin, func() {
		status := Status(context.Background(), root)
		if !status.Installed {
			t.Fatalf("expected installed status, got %#v", status)
		}
		if status.Source != whisperSourceExternal {
			t.Fatalf("expected external source, got %q", status.Source)
		}
		if status.WhisperPath != filepath.Join(pathBin, "whisper") {
			t.Fatalf("expected external whisper path, got %q", status.WhisperPath)
		}
	})
}

func mustWriteExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func withTempPATH(t *testing.T, prefix string, run func()) {
	t.Helper()
	old := os.Getenv("PATH")
	if err := os.Setenv("PATH", prefix+":"+old); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	defer func() {
		_ = os.Setenv("PATH", old)
	}()
	run()
}

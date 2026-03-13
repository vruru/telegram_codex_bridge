package macos

import (
	"strings"
	"testing"
)

func TestRenderLaunchAgent(t *testing.T) {
	content := renderLaunchAgent(launchAgentSpec{
		Label:       "com.example.bridge",
		Program:     "/tmp/telegram-codex-bridge/bin/telegram-codex-bridge",
		WorkingDir:  "/tmp/telegram-codex-bridge",
		StdoutLog:   "/tmp/telegram-codex-bridge/data/logs/stdout.log",
		StderrLog:   "/tmp/telegram-codex-bridge/data/logs/stderr.log",
		RunAtLoad:   true,
		KeepAlive:   true,
		ProcessType: "Interactive",
	})

	for _, needle := range []string{
		"<key>Label</key>",
		"<string>com.example.bridge</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>KeepAlive</key>",
		"<key>ProgramArguments</key>",
	} {
		if !strings.Contains(content, needle) {
			t.Fatalf("expected plist to contain %q", needle)
		}
	}
}

func TestXMLEscape(t *testing.T) {
	got := xmlEscape(`one & two < three > "four" 'five'`)
	want := "one &amp; two &lt; three &gt; &quot;four&quot; &apos;five&apos;"
	if got != want {
		t.Fatalf("xmlEscape() = %q, want %q", got, want)
	}
}

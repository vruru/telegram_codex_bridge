package i18n

import "testing"

func TestNewDetectsChineseTelegramLocale(t *testing.T) {
	t.Parallel()

	loc := New("zh-CN")
	if got := loc.T("中文", "English"); got != "中文" {
		t.Fatalf("T() = %q, want %q", got, "中文")
	}
}

func TestNewDefaultsToEnglishForNonChineseLocale(t *testing.T) {
	t.Parallel()

	loc := New("en-US")
	if got := loc.T("中文", "English"); got != "English" {
		t.Fatalf("T() = %q, want %q", got, "English")
	}
}

func TestForMessagePrefersEnglishText(t *testing.T) {
	t.Parallel()

	loc := ForMessage("zh-CN", "Please summarize this repository")
	if got := loc.T("中文", "English"); got != "English" {
		t.Fatalf("T() = %q, want %q", got, "English")
	}
}

func TestForMessagePrefersChineseText(t *testing.T) {
	t.Parallel()

	loc := ForMessage("en-US", "请总结这个仓库")
	if got := loc.T("中文", "English"); got != "中文" {
		t.Fatalf("T() = %q, want %q", got, "中文")
	}
}

func TestResolveHonorsExplicitPreference(t *testing.T) {
	t.Parallel()

	loc := Resolve("en-US", "Please summarize this repository", "zh", "auto", true)
	if got := loc.T("中文", "English"); got != "中文" {
		t.Fatalf("T() = %q, want %q", got, "中文")
	}
}

func TestResolveFallsBackToConfiguredDefault(t *testing.T) {
	t.Parallel()

	loc := Resolve("en-US", "/help", "auto", "zh", false)
	if got := loc.T("中文", "English"); got != "中文" {
		t.Fatalf("T() = %q, want %q", got, "中文")
	}
}

package i18n

import (
	"os"
	"strings"
	"unicode"
)

const (
	PreferenceAuto = "auto"
	PreferenceZH   = "zh"
	PreferenceEN   = "en"
)

type Catalog struct {
	chinese bool
}

func New(languageCode string) Catalog {
	switch NormalizePreference(languageCode) {
	case PreferenceZH:
		return Catalog{chinese: true}
	case PreferenceEN:
		return Catalog{chinese: false}
	}

	languageCode = strings.TrimSpace(strings.ToLower(languageCode))
	if languageCode == "" {
		languageCode = strings.TrimSpace(strings.ToLower(os.Getenv("LANG")))
	}

	return Catalog{
		chinese: strings.HasPrefix(languageCode, "zh"),
	}
}

func ForMessage(languageCode, text string) Catalog {
	if hasCJK(text) {
		return Catalog{chinese: true}
	}
	if looksEnglish(text) {
		return Catalog{chinese: false}
	}
	return New(languageCode)
}

func Resolve(languageCode, text, explicitPreference, defaultPreference string, preferMessage bool) Catalog {
	switch NormalizePreference(explicitPreference) {
	case PreferenceZH:
		return New(PreferenceZH)
	case PreferenceEN:
		return New(PreferenceEN)
	}

	if preferMessage {
		if hasCJK(text) {
			return Catalog{chinese: true}
		}
		if looksEnglish(text) {
			return Catalog{chinese: false}
		}
	}

	switch NormalizePreference(defaultPreference) {
	case PreferenceZH:
		return New(PreferenceZH)
	case PreferenceEN:
		return New(PreferenceEN)
	}

	return New(languageCode)
}

func NormalizePreference(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	switch raw {
	case PreferenceZH, "zh-cn", "zh-hans", "zh-hant", "cn", "中文", "chinese":
		return PreferenceZH
	case PreferenceEN, "en-us", "en-gb", "english":
		return PreferenceEN
	case "", PreferenceAuto, "default", "system":
		return PreferenceAuto
	default:
		return PreferenceAuto
	}
}

func (c Catalog) Code() string {
	if c.chinese {
		return PreferenceZH
	}
	return PreferenceEN
}

func (c Catalog) T(zh, en string) string {
	if c.chinese {
		return zh
	}
	return en
}

func (c Catalog) PendingFrame(step int) string {
	frames := []string{
		c.T("还在处理中.", "Still working."),
		c.T("还在处理中..", "Still working.."),
		c.T("还在处理中...", "Still working..."),
	}
	if step < 0 {
		step = 0
	}
	return frames[step%len(frames)]
}

func hasCJK(text string) bool {
	for _, r := range text {
		if unicode.In(r, unicode.Han) {
			return true
		}
	}
	return false
}

func looksEnglish(text string) bool {
	letters := 0
	asciiLetters := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
			if r <= unicode.MaxASCII {
				asciiLetters++
			}
		}
	}
	return letters >= 3 && asciiLetters*100/letters >= 80
}

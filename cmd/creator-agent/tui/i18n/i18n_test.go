package i18n

import (
	"strings"
	"testing"
)

func resetLang() {
	SetLang(EN)
}

func TestDefaultLanguageIsEnglish(t *testing.T) {
	defer resetLang()
	SetLang(EN)
	if CurrentLang() != EN {
		t.Fatalf("default/current language = %q, want %q", CurrentLang(), EN)
	}
	got := T("status.thinking")
	if got != "Thinking" {
		t.Fatalf("English status.thinking = %q, want %q", got, "Thinking")
	}
}

func TestSetLangSwitchesToChinese(t *testing.T) {
	defer resetLang()
	SetLang(ZH)
	if CurrentLang() != ZH {
		t.Fatalf("language = %q, want %q", CurrentLang(), ZH)
	}
	got := T("status.thinking")
	if got == "" || got == "status.thinking" {
		t.Fatalf("Chinese status.thinking unresolved: %q", got)
	}
	if !containsCJK(got) {
		t.Fatalf("Chinese status.thinking should contain CJK chars: %q", got)
	}
}

func TestParseLang(t *testing.T) {
	defer resetLang()
	cases := []struct {
		in   string
		want Lang
	}{
		{"", EN},
		{"en", EN},
		{"EN", EN},
		{"zh", ZH},
		{"zh-CN", ZH},
		{"zh_TW", ZH},
		{"fr", EN},
	}
	for _, c := range cases {
		if got := ParseLang(c.in); got != c.want {
			t.Errorf("ParseLang(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTFormatsArguments(t *testing.T) {
	defer resetLang()
	SetLang(EN)
	got := T("status_line.executing_named", "bash", " | 120ms")
	want := "🔧 Executing bash | 120ms"
	if got != want {
		t.Fatalf("formatted English = %q, want %q", got, want)
	}
	SetLang(ZH)
	got = T("status_line.executing_named", "bash", " | 120ms")
	if !strings.Contains(got, "bash") {
		t.Fatalf("Chinese formatting lost argument: %q", got)
	}
}

func TestMissingKeyReturnsKey(t *testing.T) {
	defer resetLang()
	SetLang(EN)
	got := T("this.key.does.not.exist")
	if got != "this.key.does.not.exist" {
		t.Fatalf("missing key fallback = %q, want the key itself", got)
	}
}

func TestEnglishAndChineseDictionariesHaveSameKeys(t *testing.T) {
	for k := range enDict {
		if _, ok := zhDict[k]; !ok {
			t.Errorf("zhDict missing key %q", k)
		}
	}
	for k := range zhDict {
		if _, ok := enDict[k]; !ok {
			t.Errorf("enDict missing key %q", k)
		}
	}
}

func containsCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

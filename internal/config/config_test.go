package config

import (
	"flag"
	"strings"
	"testing"
	"time"
)

// TestParseInterval проверяет оба поддерживаемых формата интервала: число
// секунд и длительность.
func TestParseInterval(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "число секунд", in: "300", want: 300 * time.Second},
		{name: "длительность", in: "2m", want: 2 * time.Minute},
		{name: "миллисекунды", in: "500ms", want: 500 * time.Millisecond},
		{name: "пробелы по краям", in: " 5s ", want: 5 * time.Second},
		{name: "пусто", in: "", wantErr: true},
		{name: "мусор", in: "abc", wantErr: true},
		{name: "число с единицей без значения", in: "s", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseInterval(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseInterval(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseInterval(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseInterval(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestParseSeconds проверяет округление длительности до целых секунд.
func TestParseSeconds(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{in: "300", want: 300},
		{in: "1s", want: 1},
		{in: "2m", want: 120},
		{in: "500ms", want: 1},
		{in: "0", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseSeconds(tt.in)
			if err != nil {
				t.Fatalf("ParseSeconds(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseSeconds(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestParseSeconds_ErrorMentionsBothFormats проверяет, что ошибка объясняет
// оба ожидаемых формата и сохраняет диагностику time.ParseDuration.
func TestParseSeconds_ErrorMentionsBothFormats(t *testing.T) {
	_, err := ParseSeconds("abc")
	if err == nil {
		t.Fatal("ParseSeconds(\"abc\") = nil error, want error")
	}

	for _, want := range []string{`"300"`, `"1s"`, "abc", "invalid duration"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// TestParseStoreInterval проверяет, что субсекундные значения отклоняются:
// ноль для сервера — особое значение (синхронное сохранение).
func TestParseStoreInterval(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{name: "синхронно", in: "0", want: 0},
		{name: "синхронно явно", in: "0s", want: 0},
		{name: "секунды", in: "5s", want: 5},
		{name: "число", in: "300", want: 300},
		{name: "субсекундное", in: "300ms", wantErr: true},
		{name: "чуть меньше секунды", in: "999ms", wantErr: true},
		{name: "мусор", in: "abc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseStoreInterval(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseStoreInterval(%q) = %d, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseStoreInterval(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseStoreInterval(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestApplyIf проверяет приоритет: значение из файла применяется, только если
// поле задано и флаг не был передан явно.
func TestApplyIf(t *testing.T) {
	const flagName = "a"

	addr := "default"
	ApplyIf(&addr, nil, func(string) bool { return false }, flagName)
	if addr != "default" {
		t.Errorf("addr = %q, want default: отсутствующее поле не должно менять конфиг", addr)
	}

	fileAddr := "file"
	ApplyIf(&addr, &fileAddr, func(string) bool { return false }, flagName)
	if addr != "file" {
		t.Errorf("addr = %q, want file", addr)
	}

	addr = "flag"
	ApplyIf(&addr, &fileAddr, func(string) bool { return true }, flagName)
	if addr != "flag" {
		t.Errorf("addr = %q, want flag: явный флаг должен побеждать файл", addr)
	}
}

// TestApplyValue проверяет преобразование значения и проброс ошибки разбора.
func TestApplyValue(t *testing.T) {
	n := 0
	raw := "5s"
	if err := ApplyValue(&n, &raw, func(string) bool { return false }, "i", ParseSeconds); err != nil {
		t.Fatalf("ApplyValue() error = %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}

	// Явный флаг важнее файла: значение не меняется и ошибки нет.
	n = 7
	if err := ApplyValue(&n, &raw, func(string) bool { return true }, "i", ParseSeconds); err != nil {
		t.Fatalf("ApplyValue() error = %v", err)
	}
	if n != 7 {
		t.Errorf("n = %d, want 7: значение флага не должно перекрываться файлом", n)
	}

	// Ошибка разбора значения из файла возвращается вызывающему.
	bad := "abc"
	if err := ApplyValue(&n, &bad, func(string) bool { return false }, "i", ParseSeconds); err == nil {
		t.Error("ApplyValue() = nil error, want parse error")
	}
}

// TestExplicitFlags проверяет, что различаются явно заданные и умолчательные
// флаги.
func TestExplicitFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	addr := fs.String("a", "default", "")
	fs.String("r", "default", "")
	if err := fs.Parse([]string{"-a", "explicit"}); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	set := ExplicitFlags(fs)
	if !set("a") {
		t.Error("set(\"a\") = false, want true: флаг передан явно")
	}
	if set("r") {
		t.Error("set(\"r\") = true, want false: флаг не передавался")
	}
	if *addr != "explicit" {
		t.Errorf("-a = %q, want explicit", *addr)
	}
}

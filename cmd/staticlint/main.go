// Package main implements the multichecker (staticlint) for the gometrics
// project. It runs a curated set of static analyzers against the source
// code of the repository and reports diagnostics.
//
// # Запуск
//
// Multichecker собирается и запускается как обычная CLI-программа:
//
//	go build -o staticlint ./cmd/staticlint
//	./staticlint ./...
//
// Аргументы — пути в стиле go-инструментов ("./...", "./internal/...").
//
// Дополнительные возможности:
//
//	./staticlint -flags        # вывод всех доступных флагов анализаторов
//	./staticlint -help         # краткая справка
//	./staticlint -json ./...   # машиночитаемый вывод
//
// # Стандартные статические анализаторы пакета golang.org/x/tools/go/analysis/passes
//
//   - asmdecl             — проверяет корректность деклараций в .s-файлах;
//   - assign              — ищет бесполезные присваивания;
//   - atomic              — проверяет корректное использование sync/atomic;
//   - atomicalign         — проверяет выравнивание для atomic-полей;
//   - bools               — ищет подозрительные булевы выражения;
//   - buildtag            — валидирует //go:build и // +build директивы;
//   - ctrlflow            — ищет недостижимый код;
//   - copylock            — запрещает копирование sync.Locker и sync.Once по значению;
//   - deepequalerrors     — запрещает reflect.DeepEqual для ошибок;
//   - defers              — ищет ошибки в отложенных вызовах;
//   - directive           — проверяет корректность //go:-директив;
//   - errorsas            — проверяет второй аргумент errors.As;
//   - findcall            — пример анализатора (демо);
//   - framepointer        — проверяет некорректную очистку frame pointer;
//   - httpresponse        — проверяет ошибки в использовании http.Response;
//   - ifaceassert         — проверяет утверждения типов на интерфейсах;
//   - loopclosure         — ищет захват переменной цикла в замыкании;
//   - lostcancel          — проверяет потерю cancel-функций из context.WithCancel;
//   - nilfunc             — ищет сравнения функций с nil;
//   - printf              — проверяет форматные строки;
//   - reflectvaluecompare — запрещает сравнение reflect.Value через ==;
//   - shift               — проверяет сдвиги;
//   - sigchanyzer         — проверяет корректную передачу в signal.Notify;
//   - slog                — проверяет корректность вызовов log/slog;
//   - sortslice           — ищет ошибки в sort.Slice;
//   - stdmethods          — проверяет сигнатуры методов, реализующих интерфейсы;
//   - stringintconv       — проверяет преобразование int→string;
//   - structtag           — валидирует теги структур;
//   - testinggoroutine    — ищет t.Fatal и подобное из горутин;
//   - tests               — ищет ошибки в тестах;
//   - timeformat          — проверяет формат времени в time.Parse;
//   - unmarshal           — ищет ошибки в json.Unmarshal и подобных;
//   - unreachable         — ищет недостижимый код;
//   - unsafeptr           — проверяет преобразования unsafe.Pointer;
//   - unusedresult        — ищет игнорирование результатов вызовов;
//   - unusedwrite         — ищет бесполезные записи в поля;
//   - waitgroup           — ищет некорректное использование sync.WaitGroup.
//
// # Анализаторы класса SA пакета staticcheck.io
//
// Включены все анализаторы класса SA (staticcheck/SA*):
//   - SA1xxx — корректность кода, потенциальные баги;
//   - SA2xxx — некорректное использование API;
//   - SA3xxx — подозрительные конструкции (подозрение на баг);
//   - SA4xxx — производительность (аллокации, лишние копии);
//   - SA5xxx — корректность (некорректные аргументы, ошибки);
//   - SA6xxx — производительность (устаревшие вызовы, лишние действия);
//   - SA9xxx — прочие подозрительные конструкции.
//
// # Анализаторы остальных классов пакета staticcheck.io
//
// По одному анализатору из каждого остального класса:
//   - STxxxx — stylecheck: стилевые замечания, в т.ч. ST1000 (doc-комментарий к пакету);
//   - QFxxxx — quickfix:   подсказки по упрощению кода;
//   - U1000  — unused:     неиспользуемые идентификаторы.
//
// # Публичные анализаторы
//
//   - gocritic    (github.com/go-critic/go-critic)      — набор проверок стиля и корректности;
//   - ineffassign (github.com/gordonklaus/ineffassign)  — неэффективные присваивания.
//
// # Собственный анализатор
//
//   - noosexit — запрещает прямой вызов os.Exit в функции main пакета main.
package main

import (
	// публичные анализаторы
	"github.com/go-critic/go-critic/checkers/analyzer"
	"github.com/gordonklaus/ineffassign/pkg/ineffassign"

	// стандартные анализаторы x/tools/go/analysis/passes
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/multichecker"
	"golang.org/x/tools/go/analysis/passes/asmdecl"
	"golang.org/x/tools/go/analysis/passes/assign"
	"golang.org/x/tools/go/analysis/passes/atomic"
	"golang.org/x/tools/go/analysis/passes/atomicalign"
	"golang.org/x/tools/go/analysis/passes/bools"
	"golang.org/x/tools/go/analysis/passes/buildtag"
	"golang.org/x/tools/go/analysis/passes/copylock"
	"golang.org/x/tools/go/analysis/passes/ctrlflow"
	"golang.org/x/tools/go/analysis/passes/deepequalerrors"
	"golang.org/x/tools/go/analysis/passes/defers"
	"golang.org/x/tools/go/analysis/passes/directive"
	"golang.org/x/tools/go/analysis/passes/errorsas"
	"golang.org/x/tools/go/analysis/passes/findcall"
	"golang.org/x/tools/go/analysis/passes/framepointer"
	"golang.org/x/tools/go/analysis/passes/httpresponse"
	"golang.org/x/tools/go/analysis/passes/ifaceassert"
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/go/analysis/passes/lostcancel"
	"golang.org/x/tools/go/analysis/passes/nilfunc"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/go/analysis/passes/reflectvaluecompare"
	"golang.org/x/tools/go/analysis/passes/shift"
	"golang.org/x/tools/go/analysis/passes/sigchanyzer"
	"golang.org/x/tools/go/analysis/passes/slog"
	"golang.org/x/tools/go/analysis/passes/sortslice"
	"golang.org/x/tools/go/analysis/passes/stdmethods"
	"golang.org/x/tools/go/analysis/passes/stringintconv"
	"golang.org/x/tools/go/analysis/passes/structtag"
	"golang.org/x/tools/go/analysis/passes/testinggoroutine"
	"golang.org/x/tools/go/analysis/passes/tests"
	"golang.org/x/tools/go/analysis/passes/timeformat"
	"golang.org/x/tools/go/analysis/passes/unmarshal"
	"golang.org/x/tools/go/analysis/passes/unreachable"
	"golang.org/x/tools/go/analysis/passes/unsafeptr"
	"golang.org/x/tools/go/analysis/passes/unusedresult"
	"golang.org/x/tools/go/analysis/passes/unusedwrite"
	"golang.org/x/tools/go/analysis/passes/waitgroup"

	// пакет staticcheck.io: классы SA, ST, QF, U
	"honnef.co/go/tools/quickfix"
	"honnef.co/go/tools/staticcheck"
	"honnef.co/go/tools/stylecheck"
	"honnef.co/go/tools/unused"

	// собственный анализатор
	"github.com/subtotalstew/gometrics.git/internal/analysis/noosexit"
)

func main() {
	var analyzers []*analysis.Analyzer

	// ---- 1. Стандартные статические анализаторы golang.org/x/tools/go/analysis/passes ----
	analyzers = append(analyzers,
		asmdecl.Analyzer,
		assign.Analyzer,
		atomic.Analyzer,
		atomicalign.Analyzer,
		bools.Analyzer,
		buildtag.Analyzer,
		copylock.Analyzer,
		ctrlflow.Analyzer,
		deepequalerrors.Analyzer,
		defers.Analyzer,
		directive.Analyzer,
		errorsas.Analyzer,
		findcall.Analyzer,
		framepointer.Analyzer,
		httpresponse.Analyzer,
		ifaceassert.Analyzer,
		loopclosure.Analyzer,
		lostcancel.Analyzer,
		nilfunc.Analyzer,
		printf.Analyzer,
		reflectvaluecompare.Analyzer,
		shift.Analyzer,
		sigchanyzer.Analyzer,
		slog.Analyzer,
		sortslice.Analyzer,
		stdmethods.Analyzer,
		stringintconv.Analyzer,
		structtag.Analyzer,
		testinggoroutine.Analyzer,
		tests.Analyzer,
		timeformat.Analyzer,
		unmarshal.Analyzer,
		unreachable.Analyzer,
		unsafeptr.Analyzer,
		unusedresult.Analyzer,
		unusedwrite.Analyzer,
		waitgroup.Analyzer,
	)

	// ---- 2. Все анализаторы класса SA пакета staticcheck.io ----
	for _, v := range staticcheck.Analyzers {
		analyzers = append(analyzers, v.Analyzer)
	}

	// ---- 3. По одному анализатору остальных классов staticcheck.io ----
	for _, v := range stylecheck.Analyzers {
		analyzers = append(analyzers, v.Analyzer)
	}
	for _, v := range quickfix.Analyzers {
		analyzers = append(analyzers, v.Analyzer)
	}
	analyzers = append(analyzers, unused.Analyzer.Analyzer)

	// ---- 4. Публичные анализаторы (не менее двух) ----
	analyzers = append(analyzers,
		analyzer.Analyzer,    // github.com/go-critic/go-critic
		ineffassign.Analyzer, // github.com/gordonklaus/ineffassign
	)

	// ---- 5. Собственный анализатор: запрет прямого вызова os.Exit в main пакета main ----
	analyzers = append(analyzers, noosexit.Analyzer)

	multichecker.Main(analyzers...)
}

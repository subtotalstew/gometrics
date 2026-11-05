package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDo_Success(t *testing.T) {
	err := Do("test", func() error {
		return nil
	}, func(err error) bool {
		return true
	})

	if err != nil {
		t.Errorf("Do() error = %v, want nil", err)
	}
}

func TestDo_NonRetriableError(t *testing.T) {
	expected := errors.New("non-retriable")

	err := Do("test", func() error {
		return expected
	}, func(err error) bool {
		return false
	})

	if !errors.Is(err, expected) {
		t.Errorf("Do() error = %v, want %v", err, expected)
	}
}

// withoutPauses подменяет Intervals на время теста. Возвращает функцию
// восстановления.
func withoutPauses(t *testing.T) {
	t.Helper()

	saved := Intervals
	Intervals = []time.Duration{time.Hour}
	t.Cleanup(func() { Intervals = saved })
}

// TestDoContext_NotStartedWhenCancelled проверяет, что при уже отменённом
// контексте операция не выполняется ни разу.
func TestDoContext_NotStartedWhenCancelled(t *testing.T) {
	withoutPauses(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := DoContext(ctx, "test", func() error {
		calls++
		return errors.New("boom")
	}, func(error) bool { return true })

	if err == nil {
		t.Fatal("DoContext() = nil error, want cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("DoContext() error = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Errorf("calls = %d, want 0: отменённый контекст должен отменять попытки", calls)
	}
}

// TestDoContext_CancelledDuringPause проверяет, что отмена прерывает паузу
// между попытками, не дожидаясь её окончания.
func TestDoContext_CancelledDuringPause(t *testing.T) {
	withoutPauses(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := DoContext(ctx, "test", func() error {
		return errors.New("boom")
	}, func(error) bool { return true })
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("DoContext() = nil error, want cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("DoContext() error = %v, want context.Canceled", err)
	}
	if elapsed > time.Second {
		t.Errorf("DoContext() вернулся через %v: пауза не прервалась по отмене", elapsed)
	}
}

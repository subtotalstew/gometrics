package retry

import (
	"errors"
	"testing"
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

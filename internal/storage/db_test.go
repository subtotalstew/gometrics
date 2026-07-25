package storage

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func newMockDBStorage(t *testing.T) (*DBStorage, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("не удалось создать sqlmock: %v", err)
	}

	s := &DBStorage{db: db}

	cleanup := func() {
		db.Close()
	}

	return s, mock, cleanup
}

func TestDBStorage_SetGauge(t *testing.T) {
	s, mock, cleanup := newMockDBStorage(t)
	defer cleanup()

	mock.ExpectExec(`INSERT INTO gauges`).
		WithArgs("Alloc", 123.45).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.SetGauge("Alloc", 123.45)
	if err != nil {
		t.Errorf("SetGauge() error = %v, want nil", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("не все ожидаемые запросы были выполнены: %v", err)
	}
}

func TestDBStorage_SetGauge_Error(t *testing.T) {
	s, mock, cleanup := newMockDBStorage(t)
	defer cleanup()

	mock.ExpectExec(`INSERT INTO gauges`).
		WithArgs("Alloc", 123.45).
		WillReturnError(errors.New("connection lost"))

	err := s.SetGauge("Alloc", 123.45)
	if err == nil {
		t.Error("SetGauge() error = nil, want error")
	}
}

func TestDBStorage_UpdateCounter(t *testing.T) {
	s, mock, cleanup := newMockDBStorage(t)
	defer cleanup()

	mock.ExpectExec(`INSERT INTO counters`).
		WithArgs("PollCount", int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.UpdateCounter("PollCount", 5)
	if err != nil {
		t.Errorf("UpdateCounter() error = %v, want nil", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("не все ожидаемые запросы были выполнены: %v", err)
	}
}

func TestDBStorage_UpdateCounter_Error(t *testing.T) {
	s, mock, cleanup := newMockDBStorage(t)
	defer cleanup()

	mock.ExpectExec(`INSERT INTO counters`).
		WithArgs("PollCount", int64(5)).
		WillReturnError(errors.New("connection lost"))

	err := s.UpdateCounter("PollCount", 5)
	if err == nil {
		t.Error("UpdateCounter() error = nil, want error")
	}
}

func TestDBStorage_GetGauge_Found(t *testing.T) {
	s, mock, cleanup := newMockDBStorage(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"value"}).AddRow(99.9)
	mock.ExpectQuery(`SELECT value FROM gauges WHERE id = \$1`).
		WithArgs("Alloc").
		WillReturnRows(rows)

	value, ok := s.GetGauge("Alloc")
	if !ok {
		t.Error("GetGauge() ok = false, want true")
	}
	if value != 99.9 {
		t.Errorf("GetGauge() = %v, want 99.9", value)
	}
}

func TestDBStorage_GetGauge_NotFound(t *testing.T) {
	s, mock, cleanup := newMockDBStorage(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT value FROM gauges WHERE id = \$1`).
		WithArgs("Missing").
		WillReturnError(sql.ErrNoRows)

	_, ok := s.GetGauge("Missing")
	if ok {
		t.Error("GetGauge() ok = true, want false for missing metric")
	}
}

func TestDBStorage_GetCounter_Found(t *testing.T) {
	s, mock, cleanup := newMockDBStorage(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"delta"}).AddRow(int64(42))
	mock.ExpectQuery(`SELECT delta FROM counters WHERE id = \$1`).
		WithArgs("PollCount").
		WillReturnRows(rows)

	value, ok := s.GetCounter("PollCount")
	if !ok {
		t.Error("GetCounter() ok = false, want true")
	}
	if value != 42 {
		t.Errorf("GetCounter() = %v, want 42", value)
	}
}

func TestDBStorage_GetCounter_NotFound(t *testing.T) {
	s, mock, cleanup := newMockDBStorage(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT delta FROM counters WHERE id = \$1`).
		WithArgs("Missing").
		WillReturnError(sql.ErrNoRows)

	_, ok := s.GetCounter("Missing")
	if ok {
		t.Error("GetCounter() ok = true, want false for missing metric")
	}
}

func TestDBStorage_GetAllMetrics(t *testing.T) {
	s, mock, cleanup := newMockDBStorage(t)
	defer cleanup()

	gaugeRows := sqlmock.NewRows([]string{"id", "value"}).
		AddRow("Alloc", 1.1).
		AddRow("Sys", 2.2)
	mock.ExpectQuery(`SELECT id, value FROM gauges`).WillReturnRows(gaugeRows)

	counterRows := sqlmock.NewRows([]string{"id", "delta"}).
		AddRow("PollCount", int64(10))
	mock.ExpectQuery(`SELECT id, delta FROM counters`).WillReturnRows(counterRows)

	gauges, counters := s.GetAllMetrics()

	if len(gauges) != 2 {
		t.Errorf("GetAllMetrics() gauges len = %d, want 2", len(gauges))
	}
	if gauges["Alloc"] != 1.1 || gauges["Sys"] != 2.2 {
		t.Errorf("GetAllMetrics() gauges = %v, unexpected values", gauges)
	}

	if len(counters) != 1 {
		t.Errorf("GetAllMetrics() counters len = %d, want 1", len(counters))
	}
	if counters["PollCount"] != 10 {
		t.Errorf("GetAllMetrics() counters = %v, unexpected values", counters)
	}
}

func TestDBStorage_GetAllMetrics_EmptyOnQueryError(t *testing.T) {
	s, mock, cleanup := newMockDBStorage(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT id, value FROM gauges`).
		WillReturnError(errors.New("query failed"))
	mock.ExpectQuery(`SELECT id, delta FROM counters`).
		WillReturnError(errors.New("query failed"))

	gauges, counters := s.GetAllMetrics()

	if gauges == nil || len(gauges) != 0 {
		t.Errorf("GetAllMetrics() gauges = %v, want empty map", gauges)
	}
	if counters == nil || len(counters) != 0 {
		t.Errorf("GetAllMetrics() counters = %v, want empty map", counters)
	}
}

func TestDBStorage_DB(t *testing.T) {
	s, _, cleanup := newMockDBStorage(t)
	defer cleanup()

	if s.DB() == nil {
		t.Error("DB() returned nil, want non-nil *sql.DB")
	}
}

func TestDBStorage_Close(t *testing.T) {
	s, mock, _ := newMockDBStorage(t)

	mock.ExpectClose()

	if err := s.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

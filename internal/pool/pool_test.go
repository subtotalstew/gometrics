package pool

import (
	"sync"
	"testing"
)

// item — минимальный тип с Reset для тестов.
type item struct {
	value int
	data  []int
}

func (i *item) Reset() {
	i.value = 0
	i.data = i.data[:0]
}

func TestPool_GetPut(t *testing.T) {
	t.Parallel()

	p := New(func() *item { return &item{} })

	a := p.Get()
	if a == nil {
		t.Fatal("Get() returned nil")
	}
	a.value = 42
	a.data = append(a.data, 1, 2, 3)
	p.Put(a)

	b := p.Get()
	if b.value != 0 {
		t.Errorf("after reuse: value = %d, want 0", b.value)
	}
	if len(b.data) != 0 {
		t.Errorf("after reuse: data len = %d, want 0", len(b.data))
	}
}

func TestPool_NewCalledWhenEmpty(t *testing.T) {
	t.Parallel()

	calls := 0
	p := New(func() *item {
		calls++
		return &item{}
	})
	_ = p.Get()
	_ = p.Get()
	_ = p.Get()
	if calls < 3 {
		t.Errorf("newFn calls = %d, want >= 3", calls)
	}
}

func TestPool_PutNil(t *testing.T) {
	t.Parallel()

	p := New(func() *item { return &item{} })
	var nilItem *item
	p.Put(nilItem)

	got := p.Get()
	if got == nil {
		t.Fatal("Get() after Put(nil) returned nil")
	}
}

func TestPool_Concurrent(t *testing.T) {
	t.Parallel()

	p := New(func() *item { return &item{} })

	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				v := p.Get()
				v.value = i
				v.data = append(v.data, i)
				p.Put(v)
			}
		}()
	}
	wg.Wait()
}

func TestPool_NewNil_GetReturnsNil(t *testing.T) {
	t.Parallel()

	// newFn == nil: поведение как у sync.Pool без New.
	p := New[*item](nil)
	if p == nil {
		t.Fatal("New(nil) returned nil Pool")
	}

	// Get на пустом пуле не должен паниковать и должен вернуть nil.
	got := p.Get()
	if got != nil {
		t.Errorf("Get() with nil newFn = %v, want nil", got)
	}

	// Повторный Get тоже nil.
	got2 := p.Get()
	if got2 != nil {
		t.Errorf("second Get() with nil newFn = %v, want nil", got2)
	}
}

func TestPool_NewNil_PutThenGet(t *testing.T) {
	t.Parallel()

	// newFn == nil, но Put работает: объект попадает в пул и возвращается.
	p := New[*item](nil)

	v := &item{value: 42, data: []int{1, 2, 3}}
	p.Put(v)

	got := p.Get()
	if got == nil {
		t.Fatal("Get() after Put with nil newFn returned nil")
	}
	if got.value != 0 {
		t.Errorf("after reuse: value = %d, want 0 (Reset should run)", got.value)
	}
	if len(got.data) != 0 {
		t.Errorf("after reuse: data len = %d, want 0 (Reset should run)", len(got.data))
	}

	// После того как пул опустел, снова Get → nil.
	if extra := p.Get(); extra != nil {
		t.Errorf("Get() on empty pool = %v, want nil", extra)
	}
}

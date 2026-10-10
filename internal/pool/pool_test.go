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

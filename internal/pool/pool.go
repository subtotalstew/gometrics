// Package pool предоставляет generic-контейнер Pool[T] для повторного
// использования «тяжёлых» объектов, поддерживающих метод Reset().
//
// Перед возвратом объекта в пул его состояние сбрасывается, чтобы
// следующий получатель (Get) не увидел чужие данные.
package pool

import (
	"reflect"
	"sync"
)

// resetter — контракт, которому должен удовлетворять generic-параметр T.
type resetter interface {
	Reset()
}

// Pool — типобезопасная обёртка над sync.Pool.
type Pool[T resetter] struct {
	pool *sync.Pool
}

// New создаёт и возвращает указатель на Pool.
//
// newFn вызывается лениво, когда пул пуст. Если newFn == nil,
// поведение совпадает с sync.Pool без инициализатора: Get() на пустом
// пуле возвращает zero-value типа T (для указательного T — nil).
func New[T resetter](newFn func() T) *Pool[T] {
	p := &Pool[T]{pool: &sync.Pool{}}
	if newFn != nil {
		p.pool.New = func() any { return newFn() }
	}
	return p
}

// Get возвращает объект из пула.
//
// Если пул пуст и newFn в New был nil, возвращается zero-value T
// (nil для указательных типов). Для значимых T это означает, что
// вернётся zero-value структуры, и Put на ней вызовет Reset() —
// это нормально, если Reset корректно работает на zero-value.
func (p *Pool[T]) Get() T {
	v := p.pool.Get()
	if v == nil {
		var zero T
		return zero
	}
	return v.(T)
}

// Put возвращает объект в пул, предварительно вызывая Reset().
// nil-указатели игнорируются, чтобы не загрязнять пул и не паниковать
// на nil-получателе при вызове Reset().
func (p *Pool[T]) Put(v T) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return
	}
	v.Reset()
	p.pool.Put(v)
}

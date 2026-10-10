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
func New[T resetter](newFn func() T) *Pool[T] {
	return &Pool[T]{
		pool: &sync.Pool{
			New: func() any { return newFn() },
		},
	}
}

// Get возвращает объект из пула.
func (p *Pool[T]) Get() T {
	return p.pool.Get().(T)
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

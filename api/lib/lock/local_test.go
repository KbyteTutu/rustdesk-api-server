package lock

import (
	"sync"
	"testing"
)

func TestLocalGetLockSharesSameMutex(t *testing.T) {
	l := NewLocal()
	const workers = 32
	locks := make([]*sync.Mutex, workers)
	var wg sync.WaitGroup
	count := 0
	for i := range locks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			locks[i] = l.GetLock("key")
			locks[i].Lock()
			count++
			locks[i].Unlock()
		}(i)
	}
	wg.Wait()
	if count != workers {
		t.Fatalf("count=%d", count)
	}
	for _, mutex := range locks {
		if mutex != locks[0] {
			t.Fatal("same key has different mutexes")
		}
	}
	if l.GetLock("other") == locks[0] {
		t.Fatal("different keys share mutex")
	}
}

func TestLocalLockSerializesWrites(t *testing.T) {
	l := NewLocal()
	var wg sync.WaitGroup
	count := 0
	for j := 0; j < 32; j++ {
		wg.Add(1)
		go func() { defer wg.Done(); l.Lock("key"); count++; l.UnLock("key") }()
	}
	wg.Wait()
	if count != 32 {
		t.Fatalf("count=%d", count)
	}
}

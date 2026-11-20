package sse

import (
	"sync"
	"testing"
)

func TestBroadcastAndSlowConsumer(t *testing.T) {
	broker := New()
	a, closeA, err := broker.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer closeA()
	b, closeB, err := broker.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer closeB()
	if err := broker.Publish("new_email", map[string]int{"id": 1}); err != nil {
		t.Fatal(err)
	}
	first := <-a
	second := <-b
	if first.ID != second.ID || first.Type != "new_email" || string(first.Data) != `{"id":1}` {
		t.Fatal("broadcast mismatch")
	}
	for i := 0; i < 33; i++ {
		if err := broker.Publish("sync_status", i); err != nil {
			t.Fatal(err)
		}
	}
	for range a {
	}
	for range b {
	} // bounded queues close slow subscribers for reconnect/resync.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, unsubscribe, err := broker.Subscribe()
			if err != nil {
				t.Error(err)
				return
			}
			if err := broker.Publish("flag_updated", 1); err != nil {
				t.Error(err)
			}
			unsubscribe()
			for range ch {
			}
		}()
	}
	wg.Wait()
	if err := broker.Publish("bad\nevent", nil); err == nil {
		t.Fatal("accepted unknown event")
	}
}

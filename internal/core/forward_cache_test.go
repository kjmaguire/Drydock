package core

import (
	"testing"
	"time"
)

func TestTheForwardListIsReusedForAFewSecondsAndDroppedByOpenOrClose(t *testing.T) {
	var fc forwardCache
	t0 := time.Unix(1_000_000, 0)
	if _, ok := fc.get("t1", t0); ok {
		t.Fatal("nothing is cached yet")
	}
	fc.put("t1", t0, SandboxForwards{Running: true, Max: 5})
	if v, ok := fc.get("t1", t0.Add(forwardCacheTTL-time.Millisecond)); !ok || !v.Running {
		t.Fatalf("%+v %v", v, ok)
	}
	if _, ok := fc.get("t2", t0); ok {
		t.Fatal("another task's answer is not this one's")
	}
	if _, ok := fc.get("t1", t0.Add(forwardCacheTTL)); ok {
		t.Fatal("the answer expires")
	}
	fc.put("t1", t0, SandboxForwards{Running: true})
	fc.drop("t1")
	if _, ok := fc.get("t1", t0); ok {
		t.Fatal("an open or a close drops the answer")
	}
	// what is old goes when something is added
	fc.put("old", t0, SandboxForwards{})
	fc.put("new", t0.Add(time.Minute), SandboxForwards{})
	fc.mu.Lock()
	n := len(fc.m)
	fc.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d kept", n)
	}
}

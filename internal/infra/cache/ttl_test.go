package cache

import (
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func TestTTL(t *testing.T) {
	clk := &clock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	c := NewTTL[string, int](time.Minute, clk.now)

	if _, ok := c.Get("a"); ok {
		t.Fatal("empty cache returned a value")
	}
	c.Set("a", 1, 10*time.Second)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("Get = %v, %v; want 1, true", v, ok)
	}
	clk.t = clk.t.Add(10 * time.Second)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expired entry was returned")
	}
	if c.Len() != 0 {
		t.Fatalf("Len = %d, want expired entry removed on Get", c.Len())
	}
}

func TestTTLNonPositiveTTLIsNotStored(t *testing.T) {
	c := NewTTL[string, int](time.Minute, nil)
	c.Set("a", 1, 0)
	c.Set("b", 1, -time.Second)
	if c.Len() != 0 {
		t.Fatalf("Len = %d, want 0", c.Len())
	}
}

func TestTTLSweepsExpiredOnSet(t *testing.T) {
	clk := &clock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	c := NewTTL[string, int](time.Minute, clk.now)
	c.Set("old", 1, time.Second)
	clk.t = clk.t.Add(2 * time.Minute)
	c.Set("new", 2, time.Minute)
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (expired entry swept)", c.Len())
	}
}

func TestTTLDeleteFunc(t *testing.T) {
	c := NewTTL[string, int](time.Minute, nil)
	c.Set("a", 1, time.Minute)
	c.Set("b", 2, time.Minute)
	c.DeleteFunc(func(_ string, v int) bool { return v == 1 })
	if _, ok := c.Get("a"); ok {
		t.Fatal("a must be deleted")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("b must stay")
	}
}

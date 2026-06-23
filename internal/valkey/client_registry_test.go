/*
Copyright 2025 Valkey Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package valkey

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	vclient "github.com/valkey-io/valkey-go"
)

// fakeClient is a minimal vclient.Client stand-in that records Close calls.
// Only the methods we exercise need real behaviour; the rest panic so misuse
// is obvious.
// closed is an atomic.Bool so concurrent sweeper/Start goroutines and test
// assertions do not race.
type fakeClient struct {
	vclient.Client
	id     string
	closed atomic.Bool
}

func (f *fakeClient) Close() { f.closed.Store(true) }

// newFakeRegistry returns a registry whose newClient hands out a fresh
// fakeClient per call, plus a slice capturing every client it built.
func newFakeRegistry(t *testing.T) (*ClientRegistry, *[]*fakeClient) {
	t.Helper()
	built := &[]*fakeClient{}
	r := NewClientRegistry(10 * time.Minute)
	r.newClient = func(opt vclient.ClientOption) (vclient.Client, error) {
		c := &fakeClient{id: opt.InitAddress[0]}
		*built = append(*built, c)
		return c, nil
	}
	return r, built
}

func optFor(addr, user, pass string) vclient.ClientOption {
	return vclient.ClientOption{InitAddress: []string{addr}, Username: user, Password: pass}
}

func TestGetClientMissThenHit(t *testing.T) {
	r, built := newFakeRegistry(t)
	ctx := context.Background()

	c1, err := r.GetClient(ctx, "ns/a", optFor("10.0.0.1:6379", "op", "pw"), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c2, err := r.GetClient(ctx, "ns/a", optFor("10.0.0.1:6379", "op", "pw"), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c1 != c2 {
		t.Fatal("expected cached client to be reused on hit")
	}
	if len(*built) != 1 {
		t.Fatalf("expected exactly 1 client built, got %d", len(*built))
	}
}

func TestGetClientRebuildsOnSignatureChange(t *testing.T) {
	r, built := newFakeRegistry(t)
	ctx := context.Background()

	c1, _ := r.GetClient(ctx, "ns/a", optFor("10.0.0.1:6379", "op", "pw"), "")
	r.Release("ns/a") // first caller done before pod restarts
	// Pod restarted -> new IP -> signature differs.
	c2, _ := r.GetClient(ctx, "ns/a", optFor("10.0.0.2:6379", "op", "pw"), "")
	defer r.Release("ns/a")

	if c1 == c2 {
		t.Fatal("expected a new client after signature change")
	}
	if !c1.(*fakeClient).closed.Load() {
		t.Fatal("expected the stale client to be closed")
	}
	if len(*built) != 2 {
		t.Fatalf("expected 2 clients built, got %d", len(*built))
	}
}

func TestGetClientRebuildsOnCredentialChange(t *testing.T) {
	r, built := newFakeRegistry(t)
	ctx := context.Background()

	// Initial connection with pw1.
	c1, _ := r.GetClient(ctx, "ns/a", optFor("10.0.0.1:6379", "op", "pw1"), "")
	r.Release("ns/a") // first caller done before password rotates
	// Password rotated -> signature differs; registry must rebuild.
	c2, _ := r.GetClient(ctx, "ns/a", optFor("10.0.0.1:6379", "op", "pw2"), "")
	defer r.Release("ns/a")

	if c1 == c2 {
		t.Fatal("expected a new client after credential change")
	}
	if !c1.(*fakeClient).closed.Load() {
		t.Fatal("expected the stale client to be closed after credential change")
	}
	if len(*built) != 2 {
		t.Fatalf("expected 2 clients built, got %d", len(*built))
	}
}

func TestGetClientRebuildsOnTLSVersionChange(t *testing.T) {
	r, _ := newFakeRegistry(t)
	ctx := context.Background()

	c1, _ := r.GetClient(ctx, "ns/a", optFor("10.0.0.1:6379", "op", "pw"), "rv1")
	r.Release("ns/a")
	c2, _ := r.GetClient(ctx, "ns/a", optFor("10.0.0.1:6379", "op", "pw"), "rv2")
	defer r.Release("ns/a")

	if c1 == c2 {
		t.Fatal("expected a new client after tlsVersion change")
	}
	if !c1.(*fakeClient).closed.Load() {
		t.Fatal("expected the stale client to be closed")
	}
}

func TestGetClientErrorIsReturned(t *testing.T) {
	ctx := context.Background()
	r := NewClientRegistry(10 * time.Minute)
	r.newClient = func(opt vclient.ClientOption) (vclient.Client, error) {
		return nil, errors.New("connection refused")
	}

	_, err := r.GetClient(ctx, "ns/a", optFor("10.0.0.1:6379", "op", "pw"), "")
	if err == nil {
		t.Fatal("expected dial error to propagate")
	}
	if _, ok := r.entries["ns/a"]; ok {
		t.Fatal("expected no entry stored after a failed dial")
	}
}

func TestEvictClosesAndRemoves(t *testing.T) {
	r, _ := newFakeRegistry(t)
	ctx := context.Background()

	c, _ := r.GetClient(ctx, "ns/a", optFor("10.0.0.1:6379", "op", "pw"), "")
	r.Release("ns/a")
	r.Evict("ns/a")

	if !c.(*fakeClient).closed.Load() {
		t.Fatal("expected evicted client to be closed")
	}
	if _, ok := r.entries["ns/a"]; ok {
		t.Fatal("expected entry to be removed")
	}
}

func TestEvictSkipsEntryWithActiveBorrow(t *testing.T) {
	r, _ := newFakeRegistry(t)
	ctx := context.Background()

	c, _ := r.GetClient(ctx, "ns/a", optFor("10.0.0.1:6379", "op", "pw"), "")
	// Evict while the borrow is still active — must not close the in-use client.
	r.Evict("ns/a")

	if c.(*fakeClient).closed.Load() {
		t.Fatal("expected in-use client to survive Evict")
	}
	if _, ok := r.entries["ns/a"]; !ok {
		t.Fatal("expected entry to remain while borrow is outstanding")
	}

	// Once released the entry is eligible for sweep.
	r.Release("ns/a")
}

func TestEvictMissingKeyIsNoOp(t *testing.T) {
	r, _ := newFakeRegistry(t)
	r.Evict("ns/does-not-exist") // must not panic
}

func TestGetExistingClientReturnsFalseOnMiss(t *testing.T) {
	r, _ := newFakeRegistry(t)
	c, ok := r.GetExistingClient("ns/does-not-exist")
	if ok || c != nil {
		t.Fatal("expected false/nil for missing key")
	}
}

func TestGetExistingClientBorrowsOnHit(t *testing.T) {
	ctx := context.Background()
	r, built := newFakeRegistry(t)
	_, err := r.GetClient(ctx, "ns/node", optFor("addr:6379", "", ""), "v1")
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	r.Release("ns/node")

	c, ok := r.GetExistingClient("ns/node")
	if !ok {
		t.Fatal("expected hit on existing entry")
	}
	if c != (*built)[0] {
		t.Fatal("GetExistingClient returned wrong client")
	}
	// Borrow should prevent sweep eviction.
	r.sweepIdle()
	if _, ok2 := r.GetExistingClient("ns/node"); !ok2 {
		t.Fatal("entry swept while borrowed")
	}
	r.Release("ns/node")
	r.Release("ns/node")
}

func TestSweepIdleClosesStaleLeavesFresh(t *testing.T) {
	ctx := context.Background()
	r := NewClientRegistry(10 * time.Minute)
	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	current := base
	r.now = func() time.Time { return current }
	r.newClient = func(opt vclient.ClientOption) (vclient.Client, error) {
		return &fakeClient{id: opt.InitAddress[0]}, nil
	}

	stale, _ := r.GetClient(ctx, "ns/stale", optFor("10.0.0.1:6379", "op", "pw"), "")
	r.Release("ns/stale") // caller done; entry now eligible for sweep when TTL expires
	// Advance 20m, then create a fresh entry.
	current = base.Add(20 * time.Minute)
	fresh, _ := r.GetClient(ctx, "ns/fresh", optFor("10.0.0.2:6379", "op", "pw"), "")
	defer r.Release("ns/fresh")

	r.sweepIdle()

	if !stale.(*fakeClient).closed.Load() {
		t.Fatal("expected stale client (idle > TTL) to be closed")
	}
	if _, ok := r.entries["ns/stale"]; ok {
		t.Fatal("expected stale entry removed")
	}
	if fresh.(*fakeClient).closed.Load() {
		t.Fatal("expected fresh client to survive the sweep")
	}
	if _, ok := r.entries["ns/fresh"]; !ok {
		t.Fatal("expected fresh entry to remain")
	}
}

func TestSweepIdleSkipsEntryWithActiveBorrow(t *testing.T) {
	ctx := context.Background()
	r := NewClientRegistry(10 * time.Minute)
	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	current := base
	r.now = func() time.Time { return current }
	r.newClient = func(opt vclient.ClientOption) (vclient.Client, error) {
		return &fakeClient{id: opt.InitAddress[0]}, nil
	}

	c, _ := r.GetClient(ctx, "ns/a", optFor("10.0.0.1:6379", "op", "pw"), "")
	// Do NOT release — simulate a slow in-flight reconcile.
	current = base.Add(20 * time.Minute) // advance past idleTTL

	r.sweepIdle()

	if c.(*fakeClient).closed.Load() {
		t.Fatal("expected in-use client to survive the sweep")
	}
	if _, ok := r.entries["ns/a"]; !ok {
		t.Fatal("expected entry to remain while borrow is outstanding")
	}

	r.Release("ns/a")
}

func TestStartClosesAllOnContextCancel(t *testing.T) {
	r, _ := newFakeRegistry(t)
	c, _ := r.GetClient(context.Background(), "ns/a", optFor("10.0.0.1:6379", "op", "pw"), "")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Start(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancel")
	}
	if !c.(*fakeClient).closed.Load() {
		t.Fatal("expected all clients closed on shutdown")
	}
}

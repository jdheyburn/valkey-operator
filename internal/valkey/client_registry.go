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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	vclient "github.com/valkey-io/valkey-go"
)

// entry is one cached, long-lived client and the signature of the inputs that
// produced it.
type entry struct {
	client   vclient.Client
	sig      string
	lastUsed time.Time
	borrows  int // number of callers currently using this client
}

// ClientRegistry hands out long-lived, reusable valkey-go clients keyed by
// node identity ("namespace/name"). It is the single front door for Valkey
// connections. Callers MUST NOT close a borrowed client; lifecycle belongs to
// the registry. The zero value is not usable; construct with NewClientRegistry.
type ClientRegistry struct {
	mu      sync.Mutex
	entries map[string]*entry

	// newClient builds a client from an option. Overridable in tests.
	newClient func(vclient.ClientOption) (vclient.Client, error)
	// now returns the current time. Overridable in tests.
	now func() time.Time

	idleTTL time.Duration
}

// NewClientRegistry returns an empty registry. Clients unused for longer than
// idleTTL are eligible for eviction by the sweeper started in Start.
func NewClientRegistry(idleTTL time.Duration) *ClientRegistry {
	return &ClientRegistry{
		entries:   make(map[string]*entry),
		newClient: vclient.NewClient,
		now:       time.Now,
		idleTTL:   idleTTL,
	}
}

// signature derives a stable key from the connection inputs. When it changes
// (pod IP moved, credentials or TLS rotated) the cached client is rebuilt.
//
// The inputs are hashed (SHA-256) rather than stored verbatim so the password
// is never retained in plaintext as a long-lived map key — only a
// non-reversible fingerprint is kept. We need equality, not the value. Each
// field is length-prefixed before hashing so no field-boundary ambiguity can
// produce a collision (e.g. a password containing the separator).
//
// tlsVersion MUST be a value that changes whenever opt.TLSConfig changes.
// Callers pass the TLS secret's resourceVersion, or "" when no TLS is in use.
// The registry intentionally does not inspect opt.TLSConfig itself, so a stale
// tlsVersion would silently return a client built against old TLS material.
func signature(opt vclient.ClientOption, tlsVersion string) string {
	h := sha256.New()
	writeField := func(s string) {
		var lenBuf [8]byte
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(s)))
		h.Write(lenBuf[:])
		h.Write([]byte(s))
	}
	writeField(strings.Join(opt.InitAddress, ","))
	writeField(opt.Username)
	writeField(opt.Password)
	writeField(tlsVersion)
	return hex.EncodeToString(h.Sum(nil))
}

// GetClient borrows the client for key, building it on a miss and rebuilding it
// when the inputs have drifted. The caller MUST call Release(key) when done.
// The returned client must not be closed by the caller.
func (r *ClientRegistry) GetClient(ctx context.Context, key string, opt vclient.ClientOption, tlsVersion string) (vclient.Client, error) {
	sig := signature(opt, tlsVersion)

	r.mu.Lock()
	if e, ok := r.entries[key]; ok && e.sig == sig {
		e.lastUsed = r.now()
		e.borrows++
		c := e.client
		r.mu.Unlock()
		return c, nil
	}
	r.mu.Unlock()

	// Dial off-lock so a slow connect to one node doesn't block borrows for others.
	client, err := r.newClient(opt)
	if err != nil {
		return nil, err
	}

	// Re-check: another goroutine may have built or refreshed this key while we dialed.
	// All Close calls happen after the lock is released to avoid holding it during I/O.
	r.mu.Lock()
	var toClose vclient.Client
	if e, ok := r.entries[key]; ok {
		if e.sig == sig {
			// Lost the race; keep the existing client and close the duplicate off-lock.
			toClose = client
			e.lastUsed = r.now()
			e.borrows++
			c := e.client
			r.mu.Unlock()
			toClose.Close()
			return c, nil
		}
		if e.borrows == 0 {
			// Stale signature, no active borrows — replace and close old client off-lock.
			toClose = e.client
		}
		// If borrows > 0 the old client is still in use; leave it to be closed
		// once its callers Release it and the sweeper next runs.
	}
	r.entries[key] = &entry{client: client, sig: sig, lastUsed: r.now(), borrows: 1}
	r.mu.Unlock()
	if toClose != nil {
		toClose.Close()
	}
	return client, nil
}

// GetExistingClient borrows a client for key without building a new one.
// Returns (nil, false) if the key is not present in the registry.
// The caller MUST call Release(key) when done.
func (r *ClientRegistry) GetExistingClient(key string) (vclient.Client, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[key]; ok {
		e.borrows++
		e.lastUsed = r.now()
		return e.client, true
	}
	return nil, false
}

// Release decrements the borrow count for key. Every successful GetClient call
// must be paired with exactly one Release call.
func (r *ClientRegistry) Release(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[key]; ok && e.borrows > 0 {
		e.borrows--
	}
}

// Evict closes and removes the client for key, if present. If the entry has
// active borrows it is left in place — the idle sweeper will close it once all
// callers have released it. A no-op for unknown keys.
func (r *ClientRegistry) Evict(key string) {
	r.mu.Lock()
	var toClose vclient.Client
	if e, ok := r.entries[key]; ok && e.borrows == 0 {
		toClose = e.client
		delete(r.entries, key)
	}
	r.mu.Unlock()
	if toClose != nil {
		toClose.Close()
	}
}

// sweepInterval bounds how often the idle sweep runs. Derived from idleTTL but
// floored so a tiny TTL does not spin the sweeper hot.
func (r *ClientRegistry) sweepInterval() time.Duration {
	if r.idleTTL <= 0 {
		return time.Minute
	}
	if iv := r.idleTTL / 2; iv > time.Minute {
		return iv
	}
	return time.Minute
}

// sweepIdle closes and removes every entry unused for longer than idleTTL.
// Entries with active borrows are skipped and retried on the next tick.
// Clients are closed after the lock is released to avoid blocking GetClient.
func (r *ClientRegistry) sweepIdle() {
	r.mu.Lock()
	cutoff := r.now().Add(-r.idleTTL)
	var toClose []vclient.Client
	for key, e := range r.entries {
		if e.lastUsed.Before(cutoff) && e.borrows == 0 {
			toClose = append(toClose, e.client)
			delete(r.entries, key)
		}
	}
	r.mu.Unlock()
	for _, c := range toClose {
		c.Close()
	}
}

// closeAll closes every client and empties the registry.
// Clients are closed after the lock is released to avoid blocking GetClient.
func (r *ClientRegistry) closeAll() {
	r.mu.Lock()
	toClose := make([]vclient.Client, 0, len(r.entries))
	for key, e := range r.entries {
		toClose = append(toClose, e.client)
		delete(r.entries, key)
	}
	r.mu.Unlock()
	for _, c := range toClose {
		c.Close()
	}
}

// NeedLeaderElection returns false so the registry runs on every instance, not
// only on the leader. Satisfies controller-runtime's LeaderElectionRunnable.
func (r *ClientRegistry) NeedLeaderElection() bool { return false }

// Start runs the idle sweeper until ctx is cancelled, then closes all clients.
// Satisfies controller-runtime's manager.Runnable.
func (r *ClientRegistry) Start(ctx context.Context) error {
	ticker := time.NewTicker(r.sweepInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			r.closeAll()
			return nil
		case <-ticker.C:
			r.sweepIdle()
		}
	}
}

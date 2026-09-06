// MIT License
//
// Copyright (c) 2024-2026 Arsene Tochemey Gandote
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package slatedbkit

import (
	"context"
	"errors"
	"sync"
	"time"

	slatedb "slatedb.io/slatedb-go/uniffi"
)

// ErrNotConnected is returned when an operation is attempted before Connect.
var ErrNotConnected = errors.New("store is not connected")

// Store provides the shared lifecycle and durability plumbing used by every
// SlateDB-backed eGo store. It owns a single SlateDB Db and its ObjectStore.
type Store struct {
	cfg *Config

	mu        sync.Mutex
	db        *slatedb.Db
	os        *slatedb.ObjectStore
	connected bool

	stopFlusher chan struct{}
	flusherWG   sync.WaitGroup
}

// NewStore creates a Store from the given config.
func NewStore(cfg *Config) *Store {
	return &Store{
		cfg:         cfg,
		stopFlusher: make(chan struct{}),
	}
}

// Connected reports whether the store is currently connected.
func (s *Store) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

// Connect opens the object store and the SlateDB database.
func (s *Store) Connect(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.connected {
		return nil
	}

	s.cfg.Sanitize()
	if err := s.cfg.Validate(); err != nil {
		return err
	}

	os, err := resolveObjectStore(s.cfg)
	if err != nil {
		return err
	}

	builder := slatedb.NewDbBuilder(s.cfg.DbPath, os)
	defer builder.Destroy()

	if len(s.cfg.Settings) > 0 {
		settings := slatedb.SettingsDefault()
		defer settings.Destroy()
		for key, value := range s.cfg.Settings {
			if err := settings.Set(key, value); err != nil {
				return err
			}
		}
		if err := builder.WithSettings(settings); err != nil {
			return err
		}
	}

	if s.cfg.Seed != nil {
		if err := builder.WithSeed(*s.cfg.Seed); err != nil {
			return err
		}
	}

	if s.cfg.WalObjectStoreURL != "" {
		wal, err := slatedb.ObjectStoreResolve(s.cfg.WalObjectStoreURL)
		if err != nil {
			return err
		}
		if err := builder.WithWalObjectStore(wal); err != nil {
			wal.Destroy()
			return err
		}
	}

	db, err := builder.Build()
	if err != nil {
		return err
	}

	s.db = db
	s.os = os
	s.connected = true

	if s.cfg.Durability == DurabilityFlush {
		s.startFlusher()
	}

	return nil
}

// Disconnect stops the background flusher, flushes pending writes, and shuts
// down the database.
func (s *Store) Disconnect(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return nil
	}

	if s.cfg.Durability == DurabilityFlush {
		s.stopFlusherLocked()
		// final synchronous flush so nothing in the WAL is lost
		_ = s.db.FlushWithOptions(slatedb.FlushOptions{FlushType: slatedb.FlushTypeWal})
	}

	flushType := slatedb.FlushTypeWal
	if err := s.db.ShutdownWithOptions(slatedb.CloseOptions{FlushType: &flushType}); err != nil {
		return err
	}
	s.db.Destroy()
	s.os.Destroy()

	s.db = nil
	s.os = nil
	s.connected = false
	return nil
}

// Ping verifies the store is connected, connecting if necessary.
func (s *Store) Ping(ctx context.Context) error {
	if !s.Connected() {
		return s.Connect(ctx)
	}
	return nil
}

// IsConnected returns an error when the store is not connected.
func (s *Store) IsConnected() error {
	if !s.Connected() {
		return ErrNotConnected
	}
	return nil
}

// Db returns the underlying SlateDB database handle.
func (s *Store) Db() *slatedb.Db {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db
}

// Durability returns the configured durability mode.
func (s *Store) Durability() Durability {
	return s.cfg.Durability
}

// AwaitDurable blocks until the given write handle has been durably persisted,
// honoring the configured durability mode. For DurabilityNone it returns
// immediately; for DurabilityDurable it awaits the handle; for DurabilityFlush
// it returns immediately (the background flusher drains the WAL).
func (s *Store) AwaitDurable(handle *slatedb.WriteHandle) error {
	switch s.cfg.Durability {
	case DurabilityNone, DurabilityFlush:
		return nil
	default:
		if handle == nil {
			return nil
		}
		return handle.AwaitDurable()
	}
}

// startFlusher launches the background WAL flusher goroutine.
func (s *Store) startFlusher() {
	s.stopFlusher = make(chan struct{})
	s.flusherWG.Add(1)
	go func() {
		defer s.flusherWG.Done()
		ticker := time.NewTicker(s.cfg.FlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopFlusher:
				return
			case <-ticker.C:
				if s.Connected() {
					_ = s.db.FlushWithOptions(slatedb.FlushOptions{FlushType: slatedb.FlushTypeWal})
				}
			}
		}
	}()
}

// stopFlusherLocked halts the background flusher. Caller must hold s.mu.
func (s *Store) stopFlusherLocked() {
	close(s.stopFlusher)
	s.flusherWG.Wait()
}

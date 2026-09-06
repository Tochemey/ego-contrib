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
	"errors"
	"time"
)

// Durability specifies how durably writes are acknowledged before a store
// method returns. SlateDB writes are durable only once flushed to object
// storage, so the chosen mode trades write latency against the recovery point
// objective (RPO).
type Durability int

const (
	// DurabilityNone returns after the write is applied to the in-memory
	// WAL + memtable. Fastest, but in-flight data is lost on a crash before a
	// flush. Only meaningful for the embedded in-memory object store.
	DurabilityNone Durability = iota
	// DurabilityDurable awaits WriteHandle.AwaitDurable() so the write is
	// flushed to object storage before the store method returns. Smallest RPO.
	DurabilityDurable
	// DurabilityFlush returns after the local commit and relies on a
	// background flusher (FlushInterval) plus a final flush on Disconnect.
	DurabilityFlush
)

// String returns a human readable representation of the durability level.
func (x Durability) String() string {
	switch x {
	case DurabilityNone:
		return "none"
	case DurabilityDurable:
		return "durable"
	case DurabilityFlush:
		return "flush"
	default:
		return "unknown"
	}
}

// Config holds the shared SlateDB configuration used by every store. It is
// exported so each store module can embed it in its own public Config.
type Config struct {
	// ObjectStoreURL resolves the SlateDB object store, e.g.
	// "memory:///" (embedded, no network) or "s3://bucket/path",
	// "gs://bucket/path", "az://container/path". Required.
	ObjectStoreURL string
	// EnvFile, when set, builds the object store from environment
	// configuration loaded from this file (ObjectStoreFromEnv).
	EnvFile *string

	// DbPath is the logical database path within the object store. Different
	// stores must use distinct paths to avoid colliding. Required.
	DbPath string

	// Settings maps SlateDB dotted setting paths to JSON literal values,
	// applied via Settings.Set, e.g. {"flush_interval": "\"250ms\""}.
	Settings map[string]string

	// WalObjectStoreURL optionally points at a separate, lower-latency object
	// store used only for the WAL.
	WalObjectStoreURL string

	// Seed is an optional seed value passed to the DbBuilder.
	Seed *uint64

	// Durability selects how writes are acknowledged (default Durable).
	Durability Durability
	// FlushInterval is the interval of the background flusher when
	// Durability == DurabilityFlush (default 1s).
	FlushInterval time.Duration
}

// Sanitize fills defaults for unset fields.
func (x *Config) Sanitize() {
	if x.FlushInterval <= 0 {
		x.FlushInterval = time.Second
	}
	if x.Durability == 0 && x.Durability != DurabilityNone {
		x.Durability = DurabilityDurable
	}
}

// Validate returns an error when required configuration is missing.
func (x *Config) Validate() error {
	if x.ObjectStoreURL == "" {
		return errors.New("object_store_url is required")
	}
	if x.DbPath == "" {
		return errors.New("db_path is required")
	}
	return nil
}

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

package slatedb

import (
	"time"

	"github.com/tochemey/ego-contrib/durablestore/slatedb/internal/slatedbkit"
)

// Durability specifies how durably writes are acknowledged before WriteState
// returns. SlateDB writes are durable only once flushed to object storage.
type Durability = slatedbkit.Durability

const (
	// DurabilityNone returns after the write is applied in memory.
	DurabilityNone Durability = iota
	// DurabilityDurable awaits the write being flushed to object storage
	// before returning (default).
	DurabilityDurable
	// DurabilityFlush returns after the local commit and relies on a
	// background flusher plus a final flush on Disconnect.
	DurabilityFlush
)

// Config configures the SlateDB durable state store.
type Config struct {
	// ObjectStoreURL resolves the SlateDB object store, e.g. "memory:///"
	// (embedded, no network) or "s3://bucket/path", "gs://...", "az://...".
	// Required.
	ObjectStoreURL string
	// EnvFile, when set, builds the object store from environment
	// configuration loaded from this file.
	EnvFile *string

	// DbPath is the logical database path within the object store. Use a
	// distinct path from other SlateDB stores sharing the same object store.
	// Required.
	DbPath string

	// Settings maps SlateDB dotted setting paths to JSON literal values,
	// applied via Settings.Set.
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

// toKitConfig converts the public config into the internal shared config.
func (x *Config) toKitConfig() *slatedbkit.Config {
	return &slatedbkit.Config{
		ObjectStoreURL:    x.ObjectStoreURL,
		EnvFile:           x.EnvFile,
		DbPath:            x.DbPath,
		Settings:          x.Settings,
		WalObjectStoreURL: x.WalObjectStoreURL,
		Seed:              x.Seed,
		Durability:        x.Durability,
		FlushInterval:     x.FlushInterval,
	}
}

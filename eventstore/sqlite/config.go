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

package sqlite

import "time"

// JournalMode is the SQLite journal mode a store runs with.
// See https://www.sqlite.org/pragma.html#pragma_journal_mode
type JournalMode string

const (
	// JournalModeWAL is write-ahead logging. Readers run while a write is in flight,
	// which no other mode allows. This is the default and the right choice unless the
	// database lives on a filesystem that does not support it, such as some network mounts.
	JournalModeWAL JournalMode = "WAL"
	// JournalModeDelete is the SQLite default, a rollback journal deleted on commit.
	JournalModeDelete JournalMode = "DELETE"
	// JournalModeTruncate truncates the rollback journal instead of deleting it.
	JournalModeTruncate JournalMode = "TRUNCATE"
	// JournalModePersist keeps the rollback journal and zeroes its header on commit.
	JournalModePersist JournalMode = "PERSIST"
	// JournalModeMemory keeps the rollback journal in memory. A crash mid-transaction
	// corrupts the database.
	JournalModeMemory JournalMode = "MEMORY"
	// JournalModeOff disables the rollback journal, which disables transactions.
	// A crash mid-transaction corrupts the database.
	JournalModeOff JournalMode = "OFF"
)

// Synchronous is how aggressively SQLite flushes to disk on commit.
// See https://www.sqlite.org/pragma.html#pragma_synchronous
type Synchronous string

const (
	// SynchronousOff hands writes to the operating system and does not wait.
	// A machine crash can corrupt the database.
	SynchronousOff Synchronous = "OFF"
	// SynchronousNormal is durable across process crashes and only risks the most
	// recent transactions when the machine itself loses power. This is the default.
	SynchronousNormal Synchronous = "NORMAL"
	// SynchronousFull flushes on every commit. No committed transaction is lost,
	// at the cost of an fsync per commit.
	SynchronousFull Synchronous = "FULL"
	// SynchronousExtra is SynchronousFull plus a flush of the directory entry.
	SynchronousExtra Synchronous = "EXTRA"
)

// configuration defaults, applied by sanitize to every field left empty
const (
	defaultJournalMode        = JournalModeWAL
	defaultSynchronous        = SynchronousNormal
	defaultBusyTimeout        = 5 * time.Second
	defaultMaxOpenConnections = 4
	defaultConnMaxLifetime    = time.Hour
	defaultConnMaxIdleTime    = 30 * time.Minute
)

// Config defines the settings the events store uses to open and tune its own
// database handle. It is consumed by NewEventsStore. When the handle is supplied
// by the caller through NewEventsStoreWithDB, this configuration is not needed.
type Config struct {
	// DBPath is the path of the SQLite database file, for instance "/var/lib/ego/events.db".
	// The file and its parent directory must be writable by the process.
	// Leave it empty to run against an in-memory database, which is discarded when the
	// store disconnects. That suits tests, never production.
	DBPath string

	// JournalMode is the journal mode of the database. Defaults to JournalModeWAL.
	JournalMode JournalMode

	// Synchronous is how aggressively commits are flushed to disk. Defaults to SynchronousNormal.
	Synchronous Synchronous

	// BusyTimeout is how long a connection waits for a lock held by another
	// connection before giving up. Defaults to 5 seconds.
	// SQLite allows a single writer at a time, so this timeout is what turns write
	// contention into a short wait instead of an immediate "database is locked" error.
	BusyTimeout time.Duration

	// Pragmas sets any additional SQLite pragma on every connection, for instance
	// {"cache_size": "-64000", "temp_store": "MEMORY", "mmap_size": "268435456"}.
	// They are applied after the settings above, so an entry here overrides the
	// journal mode, the synchronous setting or the busy timeout when it names one of them.
	// See https://www.sqlite.org/pragma.html for the full list.
	Pragmas map[string]string

	// MaxOpenConnections is the largest number of open connections. Defaults to 4.
	// Set it to 1 to serialize every statement and remove write contention entirely,
	// at the cost of read concurrency.
	MaxOpenConnections int
	// MaxIdleConnections is the number of connections kept open when idle.
	// Defaults to MaxOpenConnections, which avoids reopening connections under a steady load.
	MaxIdleConnections int
	// ConnMaxLifetime is the age at which a connection is closed. Defaults to 1 hour.
	ConnMaxLifetime time.Duration
	// ConnMaxIdleTime is the idle time after which a connection is closed. Defaults to 30 minutes.
	ConnMaxIdleTime time.Duration
}

// sanitize returns a copy of the configuration with the defaults applied.
// The receiver is left untouched.
func (c *Config) sanitize() *Config {
	cfg := *c

	if cfg.JournalMode == "" {
		cfg.JournalMode = defaultJournalMode
	}

	if cfg.Synchronous == "" {
		cfg.Synchronous = defaultSynchronous
	}

	if cfg.BusyTimeout <= 0 {
		cfg.BusyTimeout = defaultBusyTimeout
	}

	if cfg.MaxOpenConnections <= 0 {
		cfg.MaxOpenConnections = defaultMaxOpenConnections
	}

	if cfg.MaxIdleConnections <= 0 {
		cfg.MaxIdleConnections = cfg.MaxOpenConnections
	}

	if cfg.ConnMaxLifetime == 0 {
		cfg.ConnMaxLifetime = defaultConnMaxLifetime
	}

	if cfg.ConnMaxIdleTime == 0 {
		cfg.ConnMaxIdleTime = defaultConnMaxIdleTime
	}

	// copy the pragmas so a later change by the caller cannot alter the store
	if len(cfg.Pragmas) > 0 {
		pragmas := make(map[string]string, len(cfg.Pragmas))
		for name, value := range cfg.Pragmas {
			pragmas[name] = value
		}
		cfg.Pragmas = pragmas
	}

	return &cfg
}

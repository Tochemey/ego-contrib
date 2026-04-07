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

package postgres

import "time"

// dbConfig defines the internal postgres configuration
type dbConfig struct {
	DBHost                string        // DBHost represents the database host
	DBPort                int           // DBPort is the database port
	DBName                string        // DBName is the database name
	DBUser                string        // DBUser is the database user used to connect
	DBPassword            string        // DBPassword is the database password
	DBSchema              string        // DBSchema represents the database schema
	DBSSLMode             string        // DBSSLMode represents the database SSL mode
	MaxConnections        int           // MaxConnections represents the number of max connections in the pool
	MinConnections        int           // MinConnections represents the number of minimum connections in the pool
	MaxConnectionLifetime time.Duration // MaxConnectionLifetime represents the duration since creation after which a connection will be automatically closed.
	MaxConnIdleTime       time.Duration // MaxConnIdleTime is the duration after which an idle connection will be automatically closed by the health check.
	HealthCheckPeriod     time.Duration // HealthCheckPeriod is the duration between checks of the health of idle connections.
}

// newConfig creates an instance of dbConfig from the public Config
func newConfig(config *Config) *dbConfig {
	cfg := &dbConfig{
		DBHost:                config.DBHost,
		DBPort:                config.DBPort,
		DBName:                config.DBName,
		DBUser:                config.DBUser,
		DBPassword:            config.DBPassword,
		DBSchema:              config.DBSchema,
		DBSSLMode:             config.DBSSLMode,
		MaxConnections:        config.MaxConnections,
		MinConnections:        config.MinConnections,
		MaxConnectionLifetime: config.MaxConnectionLifetime,
		MaxConnIdleTime:       config.MaxConnIdleTime,
		HealthCheckPeriod:     config.HealthCheckPeriod,
	}

	cfg.sanitize()
	return cfg
}

func (c *dbConfig) sanitize() {
	if c == nil {
		return
	}

	// apply defaults
	if c.DBSSLMode == "" {
		c.DBSSLMode = "disable"
	}

	if c.MaxConnections == 0 {
		c.MaxConnections = 4
	}

	if c.MaxConnectionLifetime == 0 {
		c.MaxConnectionLifetime = time.Hour
	}

	if c.MaxConnIdleTime == 0 {
		c.MaxConnIdleTime = 30 * time.Minute
	}

	if c.HealthCheckPeriod == 0 {
		c.HealthCheckPeriod = time.Minute
	}
}

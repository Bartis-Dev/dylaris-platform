package database

import (
	"database/sql"
	"dylaris-core/config"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq" // Required: Postgres Driver
)

func InitDB(cfg config.Config) (*sql.DB, error) {
	// Postgres Connection String (DSN)
	sslMode := cfg.DBSSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, sslMode)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("DB Open Error: %w", err)
	}

	// Wait for Postgres to accept connections instead of crashing out —
	// covers the Core booting before the DB in a compose / stack deploy.
	if err := pingWithRetry(db, 60, 2*time.Second); err != nil {
		return nil, fmt.Errorf("DB Ping Error: %w", err)
	}
	log.Println("Postgres DB connection established.")

	useTimescale := config.UsesTimescale(cfg.DBType)
	log.Printf("DB_TYPE=%s (server_stats: %s)", cfg.DBType, map[bool]string{true: "TimescaleDB hypertable + native retention", false: "plain table + hourly retention sweep"}[useTimescale])
	if err := ensureSchema(db, useTimescale); err != nil {
		return nil, err
	}

	return db, nil
}

// pingWithRetry blocks until the database answers or the attempt budget
// is exhausted.
func pingWithRetry(db *sql.DB, attempts int, delay time.Duration) error {
	var err error
	for i := 1; i <= attempts; i++ {
		if err = db.Ping(); err == nil {
			return nil
		}
		log.Printf("DB not ready (%d/%d): %v", i, attempts, err)
		time.Sleep(delay)
	}
	return err
}

// EnsureSchema provisions the full schema on an arbitrary database. It is the
// exported entry point used by the in-panel DB migration to create every table
// on a fresh TARGET database before the generic row copy. useTimescale controls
// whether server_stats is created as a TimescaleDB hypertable (target backend)
// or a plain table. It is idempotent, like ensureSchema.
func EnsureSchema(db *sql.DB, useTimescale bool) error {
	return ensureSchema(db, useTimescale)
}

// ensureSchema creates every table, applies column migrations and seeds
// the baseline rows. Every statement is idempotent (CREATE/ALTER ... IF
// NOT EXISTS, conditional inserts), so it is safe to run repeatedly.
func ensureSchema(db *sql.DB, useTimescale bool) error {
	if err := createUsersTable(db); err != nil {
		return err
	}
	if err := createModulesTable(db); err != nil {
		return err
	}
	if err := createNodesTable(db); err != nil {
		return err
	}
	if err := createServersTable(db); err != nil {
		return err
	}
	if err := createSettingsTable(db); err != nil {
		return err
	}
	if err := createServerInvitesTable(db); err != nil {
		return err
	}
	if err := createServerStatsTable(db, useTimescale); err != nil {
		return err
	}
	if err := createGatewayTables(db); err != nil {
		return err
	}
	if err := createLibraryDisabledTable(db); err != nil {
		return err
	}
	if err := createBackupTables(db); err != nil {
		return err
	}
	if err := createTicketTables(db); err != nil {
		return err
	}
	if err := migrateSchema(db); err != nil {
		return err
	}
	if err := applyPhase0a1Schema(db); err != nil {
		return err
	}
	if err := applyPhase8Schema(db); err != nil {
		return err
	}
	if err := applyPhase9Schema(db); err != nil {
		return err
	}
	if err := applyPhase10Schema(db); err != nil {
		return err
	}
	if err := applyPhase13Schema(db); err != nil {
		return err
	}
	if err := applyPhase14Schema(db); err != nil {
		return err
	}
	if err := applyPhase11Schema(db); err != nil {
		return err
	}
	if err := applyPhase15Schema(db); err != nil {
		return err
	}
	if err := applyPhase16Schema(db); err != nil {
		return err
	}
	if err := applyPhase17Schema(db); err != nil {
		return err
	}
	if err := applyPhase18Schema(db); err != nil {
		return err
	}
	if err := applyWarpSchema(db); err != nil {
		return err
	}
	if err := applyBYONSchema(db); err != nil {
		return err
	}
	if err := applyTrafficSchema(db); err != nil {
		return err
	}
	if err := applyBillingSchema(db); err != nil {
		return err
	}
	if err := applyPlansSchema(db); err != nil {
		return err
	}
	if err := applyUnifiedModpackSchema(db); err != nil {
		return err
	}
	applyAdminResetEnvIfRequested(db)

	seedSystemModules(db)
	return nil
}

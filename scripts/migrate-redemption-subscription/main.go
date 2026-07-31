package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const redemptionSubscriptionMigrationKey = "migration.redemption_subscription.v1"

type migrationSummary struct {
	Dialect              string
	TotalCodes           int64
	InvalidQuotaCodes    int64
	SubscriptionCodes    int64
	HasPlanIDColumn      bool
	AlreadyApplied       bool
	DisabledInvalidCodes int64
}

// main checks or applies the one-time redemption subscription data migration.
func main() {
	apply := flag.Bool("apply", false, "apply the migration; omit to run a read-only check")
	flag.Parse()

	db, dialect, err := openMigrationDatabase()
	if err != nil {
		fmt.Fprintf(os.Stderr, "open migration database: %v\n", err)
		os.Exit(1)
	}
	sqlDB, err := db.DB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get database connection: %v\n", err)
		os.Exit(1)
	}
	defer sqlDB.Close()

	summary, err := inspectMigration(db, dialect)
	if err != nil {
		fmt.Fprintf(os.Stderr, "inspect migration: %v\n", err)
		os.Exit(1)
	}
	printMigrationSummary("check", summary)
	if !*apply {
		return
	}

	summary, err = applyMigration(db, dialect)
	if err != nil {
		fmt.Fprintf(os.Stderr, "apply migration: %v\n", err)
		os.Exit(1)
	}
	printMigrationSummary("apply", summary)
}

// openMigrationDatabase opens the configured primary database without running application startup migrations.
func openMigrationDatabase() (*gorm.DB, string, error) {
	dsn := os.Getenv("SQL_DSN")
	config := &gorm.Config{}

	switch {
	case dsn == "" || strings.HasPrefix(dsn, "local"):
		path := os.Getenv("SQLITE_PATH")
		if path == "" {
			path = "one-api.db?_busy_timeout=30000"
		}
		db, err := gorm.Open(sqlite.Open(path), config)
		return db, "sqlite", err
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		db, err := gorm.Open(postgres.New(postgres.Config{
			DSN:                  dsn,
			PreferSimpleProtocol: true,
		}), config)
		return db, "postgres", err
	case strings.HasPrefix(dsn, "clickhouse://"), strings.HasPrefix(dsn, "tcp://"),
		strings.HasPrefix(dsn, "http://"), strings.HasPrefix(dsn, "https://"):
		return nil, "", errors.New("primary database must be SQLite, MySQL, or PostgreSQL")
	default:
		if !strings.Contains(dsn, "parseTime") {
			if strings.Contains(dsn, "?") {
				dsn += "&parseTime=true"
			} else {
				dsn += "?parseTime=true"
			}
		}
		db, err := gorm.Open(mysql.Open(dsn), config)
		return db, "mysql", err
	}
}

// inspectMigration reads the current migration state without changing schema or data.
func inspectMigration(db *gorm.DB, dialect string) (migrationSummary, error) {
	summary := migrationSummary{Dialect: dialect}
	if !db.Migrator().HasTable(&model.Redemption{}) {
		return summary, errors.New("redemptions table does not exist")
	}
	if !db.Migrator().HasTable(&model.Option{}) {
		return summary, errors.New("options table does not exist")
	}

	if err := db.Model(&model.Redemption{}).Count(&summary.TotalCodes).Error; err != nil {
		return summary, err
	}

	summary.HasPlanIDColumn = db.Migrator().HasColumn(&model.Redemption{}, "PlanId")
	invalidQuery := db.Model(&model.Redemption{}).
		Where("status = ? AND quota <= 0", common.RedemptionCodeStatusEnabled)
	if summary.HasPlanIDColumn {
		invalidQuery = invalidQuery.Where("plan_id = 0")
		if err := db.Model(&model.Redemption{}).
			Where("plan_id > 0").
			Count(&summary.SubscriptionCodes).Error; err != nil {
			return summary, err
		}
	}
	if err := invalidQuery.Count(&summary.InvalidQuotaCodes).Error; err != nil {
		return summary, err
	}

	var marker model.Option
	markerQuery := db.Where("key = ?", redemptionSubscriptionMigrationKey).
		Limit(1).
		Find(&marker)
	if markerQuery.Error != nil {
		return summary, markerQuery.Error
	}
	summary.AlreadyApplied = markerQuery.RowsAffected > 0
	return summary, nil
}

// applyMigration adds the final schema, normalizes historical rows, and records one-time completion.
func applyMigration(db *gorm.DB, dialect string) (migrationSummary, error) {
	summary, err := inspectMigration(db, dialect)
	if err != nil {
		return summary, err
	}
	if summary.AlreadyApplied {
		return summary, nil
	}
	if summary.SubscriptionCodes > 0 {
		return summary, errors.New("subscription redemption codes exist before the migration marker; verify database state manually")
	}

	if !summary.HasPlanIDColumn {
		// Add the final schema field before the transactional data normalization.
		if err := db.Migrator().AddColumn(&model.Redemption{}, "PlanId"); err != nil {
			return summary, err
		}
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Redemption{}).
			Where("plan_id IS NULL").
			Update("plan_id", 0).Error; err != nil {
			return err
		}

		update := tx.Model(&model.Redemption{}).
			Where("plan_id = 0 AND status = ? AND quota <= 0", common.RedemptionCodeStatusEnabled).
			Update("status", common.RedemptionCodeStatusDisabled)
		if update.Error != nil {
			return update.Error
		}
		summary.DisabledInvalidCodes = update.RowsAffected

		return tx.Create(&model.Option{
			Key:   redemptionSubscriptionMigrationKey,
			Value: time.Now().UTC().Format(time.RFC3339),
		}).Error
	})
	if err != nil {
		return summary, err
	}

	verified, err := inspectMigration(db, dialect)
	if err != nil {
		return summary, err
	}
	verified.DisabledInvalidCodes = summary.DisabledInvalidCodes
	if !verified.HasPlanIDColumn || !verified.AlreadyApplied || verified.InvalidQuotaCodes != 0 {
		return verified, errors.New("migration verification failed")
	}
	return verified, nil
}

// printMigrationSummary prints non-sensitive counts for deployment verification.
func printMigrationSummary(stage string, summary migrationSummary) {
	fmt.Printf(
		"stage=%s dialect=%s plan_id_column=%t applied=%t total_codes=%d subscription_codes=%d invalid_quota_codes=%d disabled_invalid_codes=%d\n",
		stage,
		summary.Dialect,
		summary.HasPlanIDColumn,
		summary.AlreadyApplied,
		summary.TotalCodes,
		summary.SubscriptionCodes,
		summary.InvalidQuotaCodes,
		summary.DisabledInvalidCodes,
	)
}

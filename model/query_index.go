package model

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

type queryIndexSpec struct {
	Table   string
	Name    string
	Columns []string
}

var mainQueryIndexes = []queryIndexSpec{
	{
		Table:   "subscription_pre_consume_records",
		Name:    "idx_subscription_pre_consume_updated_id",
		Columns: []string{"updated_at", "id"},
	},
	{
		Table:   "system_tasks",
		Name:    "idx_system_tasks_type_status_id",
		Columns: []string{"type", "status", "id"},
	},
	{
		Table:   "system_tasks",
		Name:    "idx_system_tasks_type_id",
		Columns: []string{"type", "id"},
	},
}

var logQueryIndexes = []queryIndexSpec{
	{
		Table:   "logs",
		Name:    "idx_logs_created_at_id",
		Columns: []string{"created_at", "id"},
	},
	{
		Table:   "logs",
		Name:    "idx_logs_type_created_at",
		Columns: []string{"type", "created_at"},
	},
	{
		Table:   "logs",
		Name:    "idx_logs_request_final_created_id",
		Columns: []string{"request_id", "is_intermediate", "created_at", "id"},
	},
}

// ensureMainQueryIndexes creates indexes used by recurring primary-database queries.
func ensureMainQueryIndexes(db *gorm.DB) error {
	return ensureQueryIndexes(db, common.MainDatabaseType(), mainQueryIndexes)
}

// ensureLogQueryIndexes creates indexes used by log listing and aggregation queries.
func ensureLogQueryIndexes(db *gorm.DB) error {
	return ensureQueryIndexes(db, common.LogDatabaseType(), logQueryIndexes)
}

// ensureQueryIndexes creates missing indexes with non-blocking DDL where the database supports it.
func ensureQueryIndexes(db *gorm.DB, databaseType common.DatabaseType, specs []queryIndexSpec) error {
	if databaseType == common.DatabaseTypeClickHouse {
		return nil
	}
	for _, spec := range specs {
		if db.Migrator().HasIndex(spec.Table, spec.Name) {
			continue
		}
		statement, err := buildCreateQueryIndexSQL(databaseType, spec)
		if err != nil {
			return err
		}
		// 每条索引独立执行，PostgreSQL 的 CONCURRENTLY 不能位于事务中。
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("create index %s on %s: %w", spec.Name, spec.Table, err)
		}
	}
	return nil
}

// buildCreateQueryIndexSQL returns dialect-specific online index DDL.
func buildCreateQueryIndexSQL(databaseType common.DatabaseType, spec queryIndexSpec) (string, error) {
	if spec.Table == "" || spec.Name == "" || len(spec.Columns) == 0 {
		return "", fmt.Errorf("invalid query index specification: table=%q name=%q columns=%v", spec.Table, spec.Name, spec.Columns)
	}

	quote := `"`
	if databaseType == common.DatabaseTypeMySQL {
		quote = "`"
	}
	quoteIdentifier := func(identifier string) string {
		return quote + identifier + quote
	}
	columns := make([]string, 0, len(spec.Columns))
	for _, column := range spec.Columns {
		if column == "" {
			return "", fmt.Errorf("invalid query index specification: table=%q name=%q columns=%v", spec.Table, spec.Name, spec.Columns)
		}
		columns = append(columns, quoteIdentifier(column))
	}

	table := quoteIdentifier(spec.Table)
	name := quoteIdentifier(spec.Name)
	columnList := strings.Join(columns, ", ")
	switch databaseType {
	case common.DatabaseTypePostgreSQL:
		return fmt.Sprintf("CREATE INDEX CONCURRENTLY IF NOT EXISTS %s ON %s (%s)", name, table, columnList), nil
	case common.DatabaseTypeSQLite:
		return fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", name, table, columnList), nil
	case common.DatabaseTypeMySQL:
		return fmt.Sprintf("ALTER TABLE %s ADD INDEX %s (%s), ALGORITHM=INPLACE, LOCK=NONE", table, name, columnList), nil
	default:
		return "", fmt.Errorf("unsupported database type for query index: %s", databaseType)
	}
}

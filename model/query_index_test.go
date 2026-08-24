package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildCreateQueryIndexSQL verifies online DDL remains valid for every relational database.
func TestBuildCreateQueryIndexSQL(t *testing.T) {
	spec := queryIndexSpec{
		Table:   "logs",
		Name:    "idx_logs_created_at_id",
		Columns: []string{"created_at", "id"},
	}
	tests := []struct {
		name         string
		databaseType common.DatabaseType
		want         string
	}{
		{
			name:         "PostgreSQL concurrent",
			databaseType: common.DatabaseTypePostgreSQL,
			want:         `CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_logs_created_at_id" ON "logs" ("created_at", "id")`,
		},
		{
			name:         "SQLite idempotent",
			databaseType: common.DatabaseTypeSQLite,
			want:         `CREATE INDEX IF NOT EXISTS "idx_logs_created_at_id" ON "logs" ("created_at", "id")`,
		},
		{
			name:         "MySQL online",
			databaseType: common.DatabaseTypeMySQL,
			want:         "ALTER TABLE `logs` ADD INDEX `idx_logs_created_at_id` (`created_at`, `id`), ALGORITHM=INPLACE, LOCK=NONE",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statement, err := buildCreateQueryIndexSQL(test.databaseType, spec)
			require.NoError(t, err)
			assert.Equal(t, test.want, statement)
		})
	}
}

// TestEnsureQueryIndexesSQLite verifies production index definitions and repeatable migration behavior.
func TestEnsureQueryIndexesSQLite(t *testing.T) {
	specs := append(append([]queryIndexSpec{}, mainQueryIndexes...), logQueryIndexes...)
	require.NoError(t, ensureQueryIndexes(DB, common.DatabaseTypeSQLite, specs))
	require.NoError(t, ensureQueryIndexes(DB, common.DatabaseTypeSQLite, specs))

	for _, spec := range specs {
		t.Run(spec.Name, func(t *testing.T) {
			assert.True(t, DB.Migrator().HasIndex(spec.Table, spec.Name))

			var columns []struct {
				Sequence int    `gorm:"column:seqno"`
				Name     string `gorm:"column:name"`
			}
			require.NoError(t, DB.Raw(`PRAGMA index_info("`+spec.Name+`")`).Scan(&columns).Error)
			actual := make([]string, 0, len(columns))
			for _, column := range columns {
				actual = append(actual, column.Name)
			}
			assert.Equal(t, spec.Columns, actual)
		})
	}
}

package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestStartupMigratesAuthenticationStores exercises startup rather than test-only AutoMigrate fixtures.
func TestStartupMigratesAuthenticationStores(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dsn := "local"
			if dialect != "sqlite" {
				dsn = os.Getenv("TEST_" + strings.ToUpper(dialect) + "_DSN")
				if dsn == "" {
					t.Skip("isolated test database DSN is not configured")
				}
			}
			originalDB, originalLogDB := DB, LOG_DB
			originalPath, originalMaster, originalRedis := common.SQLitePath, common.IsMasterNode, common.RedisEnabled
			originalMainType, originalLogType := common.MainDatabaseType(), common.LogDatabaseType()
			t.Cleanup(func() {
				DB, LOG_DB = originalDB, originalLogDB
				common.SQLitePath, common.IsMasterNode, common.RedisEnabled = originalPath, originalMaster, originalRedis
				common.SetMainDatabaseType(originalMainType)
				common.SetLogDatabaseType(originalLogType)
				initCol()
			})
			common.SQLitePath = filepath.Join(t.TempDir(), "startup.db")
			common.IsMasterNode, common.RedisEnabled = true, false
			t.Setenv("SQL_DSN", dsn)
			t.Setenv("LOG_SQL_DSN", "")
			require.NoError(t, InitDB())
			sqlDB, err := DB.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			for _, store := range []any{&UserSession{}, &AuthFlow{}, &ExternalIdentityClaim{}, &LoginEncryptionKey{}, &TaskPlugin{}, &CasbinRule{}, &AuthzRole{}} {
				require.True(t, DB.Migrator().HasTable(store), "%T must be available after startup", store)
			}

			user := User{Username: "startup-user", Password: "stored-password-hash", TelegramId: "startup-telegram", AffCode: "startup-aff"}
			require.NoError(t, DB.Create(&user).Error)
			require.NoError(t, DB.Model(&user).UpdateColumn("auth_version", 0).Error)
			// Preserve existing records while initializing legacy authentication state twice.
			for range 2 {
				require.NoError(t, migrateDB())
			}
			require.NoError(t, DB.First(&user, user.Id).Error)
			assert.EqualValues(t, 1, user.AuthVersion)
			assert.Equal(t, "stored-password-hash", user.Password)
			var claims []ExternalIdentityClaim
			require.NoError(t, DB.Where("user_id = ?", user.Id).Find(&claims).Error)
			require.Len(t, claims, 1)
			assert.Equal(t, user.TelegramId, claims[0].Subject)
			session := newTestUserSession("startup-session", user.Id, time.Now().Unix())
			require.NoError(t, CreateUserSession(session))
			count, err := CountActiveUserSessions(user.Id, time.Now().Unix())
			require.NoError(t, err)
			assert.EqualValues(t, 1, count)
			token, _, err := CreateAuthFlow(AuthFlowCreate{Purpose: AuthFlowPurposeOAuth, ExpiresAt: time.Now().Add(time.Minute)})
			require.NoError(t, err)
			_, err = ConsumeAuthFlow(token, AuthFlowMatch{Purpose: AuthFlowPurposeOAuth})
			require.NoError(t, err)
			_, err = ConsumeAuthFlow(token, AuthFlowMatch{Purpose: AuthFlowPurposeOAuth})
			assert.ErrorIs(t, err, ErrAuthFlowConsumed)
			_, err = GetTaskPluginSyncSnapshot()
			require.NoError(t, err)
			require.NoError(t, InitPasswordEncryption())
		})
	}
}

type previousRefreshHashMigrationLegacy struct {
	SID                 string `gorm:"column:sid;type:varchar(64);primaryKey"`
	PreviousRefreshHash string `gorm:"column:previous_refresh_hash;type:char(64)"`
}

type previousRefreshHashMigrationTarget struct {
	SID                 string `gorm:"column:sid;type:varchar(64);primaryKey"`
	PreviousRefreshHash string `gorm:"column:previous_refresh_hash;type:varchar(64)"`
}

type migrationSQLRecorder struct {
	mu         sync.Mutex
	statements []string
}

func (recorder *migrationSQLRecorder) LogMode(logger.LogLevel) logger.Interface { return recorder }
func (recorder *migrationSQLRecorder) Info(context.Context, string, ...any)     {}
func (recorder *migrationSQLRecorder) Warn(context.Context, string, ...any)     {}
func (recorder *migrationSQLRecorder) Error(context.Context, string, ...any)    {}

func (recorder *migrationSQLRecorder) Trace(_ context.Context, _ time.Time, sql func() (string, int64), _ error) {
	statement, _ := sql()
	recorder.mu.Lock()
	recorder.statements = append(recorder.statements, statement)
	recorder.mu.Unlock()
}

func (recorder *migrationSQLRecorder) reset() {
	recorder.mu.Lock()
	recorder.statements = nil
	recorder.mu.Unlock()
}

func (recorder *migrationSQLRecorder) schemaMutations() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	mutations := make([]string, 0)
	for _, statement := range recorder.statements {
		normalized := strings.ToUpper(strings.TrimSpace(statement))
		if strings.HasPrefix(normalized, "ALTER TABLE") ||
			strings.HasPrefix(normalized, "CREATE TABLE") ||
			strings.HasPrefix(normalized, "DROP TABLE") ||
			strings.HasPrefix(normalized, "RENAME TABLE") ||
			strings.HasPrefix(normalized, "CREATE INDEX") ||
			strings.HasPrefix(normalized, "CREATE UNIQUE INDEX") ||
			strings.HasPrefix(normalized, "DROP INDEX") {
			mutations = append(mutations, statement)
		}
	}
	return mutations
}

func TestUserSessionPreviousRefreshHashSchemaUsesNullableVarchar(t *testing.T) {
	statement := &gorm.Statement{DB: DB}
	require.NoError(t, statement.Parse(&UserSession{}))
	field := statement.Schema.LookUpField("PreviousRefreshHash")
	require.NotNil(t, field)
	assert.Equal(t, "varchar(64)", field.TagSettings["TYPE"])
	assert.False(t, field.NotNull)
}

func testPreviousRefreshHashMigration(t *testing.T, db *gorm.DB, recorder *migrationSQLRecorder, dialect string) {
	t.Helper()
	tableName := fmt.Sprintf("user_session_previous_hash_migration_%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = db.Migrator().DropTable(tableName) })

	require.NoError(t, db.Table(tableName).AutoMigrate(&previousRefreshHashMigrationLegacy{}))
	digest := strings.Repeat("a", 60)
	require.NoError(t, db.Table(tableName).Create(&previousRefreshHashMigrationLegacy{
		SID:                 "legacy-session",
		PreviousRefreshHash: digest,
	}).Error)

	require.NoError(t, db.Table(tableName).AutoMigrate(&previousRefreshHashMigrationTarget{}))
	var session UserSession
	require.NoError(t, db.Table(tableName).
		Select("sid", "previous_refresh_hash").
		Where("sid = ?", "legacy-session").
		First(&session).Error)
	assert.Equal(t, digest, session.PreviousRefreshHash, "legacy CHAR padding must be normalized on database reads")

	columnTypes, err := db.Table(tableName).Migrator().ColumnTypes(&previousRefreshHashMigrationTarget{})
	require.NoError(t, err)
	var previousHashColumnFound bool
	for _, columnType := range columnTypes {
		if !strings.EqualFold(columnType.Name(), "previous_refresh_hash") {
			continue
		}
		previousHashColumnFound = true
		nullable, ok := columnType.Nullable()
		require.True(t, ok)
		if dialect != "sqlite" {
			assert.True(t, nullable)
		}
		assert.Contains(t, strings.ToUpper(columnType.DatabaseTypeName()), "VARCHAR")
	}
	assert.True(t, previousHashColumnFound)

	recorder.reset()
	require.NoError(t, db.Table(tableName).AutoMigrate(&previousRefreshHashMigrationTarget{}))
	assert.Empty(t, recorder.schemaMutations(), "a second migration must not repeat type-changing DDL")
}

func TestUserSessionPreviousRefreshHashMigrationSQLite(t *testing.T) {
	recorder := &migrationSQLRecorder{}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: recorder})
	require.NoError(t, err)
	testPreviousRefreshHashMigration(t, db, recorder, "sqlite")
}

func TestUserSessionPreviousRefreshHashMigrationConfiguredDatabases(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		dialector func(string) gorm.Dialector
	}{
		{name: "mysql", env: "TEST_MYSQL_DSN", dialector: func(dsn string) gorm.Dialector { return mysql.Open(dsn) }},
		{name: "postgres", env: "TEST_POSTGRES_DSN", dialector: func(dsn string) gorm.Dialector {
			return postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(test.env))
			if dsn == "" {
				t.Skip(test.env + " is not configured")
			}
			recorder := &migrationSQLRecorder{}
			db, err := gorm.Open(test.dialector(dsn), &gorm.Config{Logger: recorder})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			testPreviousRefreshHashMigration(t, db, recorder, test.name)
		})
	}
}

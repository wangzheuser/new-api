package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionGroupMigration(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			if dialect == "sqlite" {
				path := filepath.Join(t.TempDir(), "subscriptions.db")
				require.NoError(t, os.WriteFile(path, nil, 0600))
				t.Setenv("SQL_DSN", "")
				t.Setenv("SQLITE_PATH", path)
			} else {
				dsn := os.Getenv("SUBSCRIPTION_MATRIX_" + dialect)
				if dsn == "" {
					t.Skip("configure a disposable database")
				}
				t.Setenv("SQL_DSN", dsn)
			}
			db, err := openDatabase()
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
			require.False(t, db.Migrator().HasTable("users"), "the test requires an empty database")
			for range 2 {
				require.NoError(t, db.AutoMigrate(&model.User{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionOrder{}))
			}
			for _, stmt := range []string{
				"ALTER TABLE subscription_plans DROP COLUMN quota_scope",
				"ALTER TABLE user_subscriptions DROP COLUMN quota_scope",
				"ALTER TABLE user_subscriptions DROP COLUMN quota_reset_period",
				"ALTER TABLE user_subscriptions DROP COLUMN quota_reset_custom_seconds",
				"ALTER TABLE users DROP COLUMN subscription_group",
				"ALTER TABLE subscription_orders DROP COLUMN terms",
				"ALTER TABLE subscription_orders DROP COLUMN reserved_until",
				"ALTER TABLE subscription_orders DROP COLUMN paid_at",
				"ALTER TABLE subscription_orders DROP COLUMN fulfilled_subscription_id",
			} {
				require.NoError(t, db.Exec(stmt).Error)
			}
			require.NoError(t, db.Exec("ALTER TABLE subscription_plans ADD COLUMN entitlement_group varchar(64)").Error)
			require.NoError(t, db.Exec("ALTER TABLE user_subscriptions ADD COLUMN entitlement_group varchar(64)").Error)
			require.NoError(t, db.Exec("CREATE INDEX idx_user_subscriptions_entitlement_group ON user_subscriptions(entitlement_group)").Error)
			user := model.User{Username: "migration_test", Group: "vip"}
			require.NoError(t, db.Omit("SubscriptionGroup").Create(&user).Error)
			plan := model.SubscriptionPlan{Title: "groups", GrantGroups: model.GroupNames{"group-b"}, QuotaResetPeriod: "weekly"}
			require.NoError(t, db.Omit("QuotaScope").Create(&plan).Error)
			require.NoError(t, db.Model(&plan).UpdateColumn("entitlement_group", "group-a").Error)
			now := time.Now().Unix()
			instance := model.UserSubscription{
				UserId: user.Id, PlanId: plan.Id, GrantGroups: model.GroupNames{"group-b"},
				Status: "active", StartTime: now - 60, EndTime: now + 30000,
				AmountTotal: 100, AmountUsed: 70, AllocationCount: 5, UpgradeGroup: "vip",
				PrevUserGroup: "default", NextResetTime: now + 1800,
			}
			require.NoError(t, db.Omit("QuotaScope", "QuotaResetPeriod", "QuotaResetCustomSeconds").Create(&instance).Error)
			require.NoError(t, migrate(db, false, false))
			require.True(t, db.Migrator().HasColumn("subscription_plans", "entitlement_group"))
			require.NoError(t, db.Exec("INSERT INTO subscription_orders (trade_no, user_id, plan_id, status) VALUES (?, ?, ?, ?)", "pending-old", user.Id, plan.Id, "pending").Error)
			require.Error(t, migrate(db, true, false))
			require.NoError(t, migrate(db, true, true))
			require.NoError(t, migrate(db, true, false))
			for range 2 {
				require.NoError(t, db.AutoMigrate(&model.User{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionOrder{}))
			}
			var order model.SubscriptionOrder
			require.NoError(t, db.Where("trade_no = ?", "pending-old").First(&order).Error)
			assert.Equal(t, model.SubscriptionOrderUnverified, order.Status)
			assert.Zero(t, order.PaidAt)
			require.NoError(t, db.First(&plan, plan.Id).Error)
			assert.Equal(t, model.SubscriptionQuotaGroups, plan.QuotaScope)
			assert.Equal(t, model.GroupNames{"group-a", "group-b"}, plan.GrantGroups)
			require.NoError(t, db.First(&instance, instance.Id).Error)
			assert.Equal(t, model.SubscriptionQuotaGroupsAndGeneral, instance.QuotaScope)
			assert.Equal(t, model.GroupNames{"group-b"}, instance.GrantGroups)
			assert.EqualValues(t, 70, instance.AmountUsed)
			assert.EqualValues(t, 100, instance.AmountTotal)
			assert.EqualValues(t, 5, instance.AllocationCount)
			assert.Equal(t, "weekly", instance.QuotaResetPeriod)
			assert.Equal(t, now+1800, instance.NextResetTime)
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Equal(t, "default", user.Group)
			assert.Equal(t, "vip", user.EffectiveBaseGroup())
			assert.False(t, db.Migrator().HasColumn("subscription_plans", "entitlement_group"))
			assert.False(t, db.Migrator().HasColumn("user_subscriptions", "entitlement_group"))
		})
	}
}

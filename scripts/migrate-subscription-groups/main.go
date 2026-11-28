package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type legacyScopeRow struct {
	ID               int
	EntitlementGroup sql.NullString
	GrantGroups      model.GroupNames
	QuotaScope       string
}

func main() {
	apply := flag.Bool("apply", false, "migrate an offline database after taking a verified backup")
	pendingReviewed := flag.Bool("pending-reviewed", false, "confirm existing pending orders were reconciled or assigned for manual review")
	flag.Parse()
	db, err := openDatabase()
	if err == nil {
		err = migrate(db, *apply, *pendingReviewed)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func openDatabase() (*gorm.DB, error) {
	dsn := os.Getenv("SQL_DSN")
	var driver gorm.Dialector
	switch {
	case dsn == "":
		path := os.Getenv("SQLITE_PATH")
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("SQLITE_PATH must name an existing database file")
		}
		driver = sqlite.Open(path)
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
	default:
		driver = mysql.Open(dsn)
	}
	db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, errors.New("cannot connect to the selected database")
	}
	return db, nil
}

func migrate(db *gorm.DB, apply bool, pendingReviewed bool) error {
	for _, table := range []string{"subscription_plans", "user_subscriptions", "subscription_orders"} {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("missing required table %s", table)
		}
	}
	var pending int64
	if err := db.Table("subscription_orders").Where("status = ?", "pending").Count(&pending).Error; err != nil {
		return errors.New("cannot inspect pending subscription orders")
	}
	fmt.Printf("pending orders requiring payment reconciliation: %d\n", pending)
	if apply && pending > 0 && !pendingReviewed {
		return errors.New("reconcile pending orders and record their disposition before applying with -pending-reviewed")
	}
	for _, item := range []struct {
		table string
		model any
	}{
		{"subscription_plans", &model.SubscriptionPlan{}},
		{"user_subscriptions", &model.UserSubscription{}},
	} {
		m := db.Migrator()
		legacy := m.HasColumn(item.table, "entitlement_group")
		var count int64
		if err := db.Table(item.table).Count(&count).Error; err != nil {
			return fmt.Errorf("%s: cannot count records", item.table)
		}
		fmt.Printf("%s: %d records, legacy group column=%t\n", item.table, count, legacy)
		if !legacy {
			if !m.HasColumn(item.table, "quota_scope") {
				return fmt.Errorf("%s: unrecognized schema", item.table)
			}
			continue
		}
		if !m.HasColumn(item.table, "grant_groups") {
			return fmt.Errorf("%s: expected grant_groups in source", item.table)
		}
		if item.table == "user_subscriptions" {
			var invalid int64
			if err := db.Table(item.table).
				Where("amount_total < 0 OR amount_used < 0 OR status IS NULL OR status NOT IN ?", []string{"scheduled", "active", "expired", "cancelled", "archived"}).
				Count(&invalid).Error; err != nil || invalid > 0 {
				return fmt.Errorf("user subscriptions with invalid amount or status: %d", invalid)
			}
			// Reset rules cannot be recovered from an orphaned active subscription.
			var missing int64
			err := db.Table("user_subscriptions s").Joins("LEFT JOIN subscription_plans p ON p.id = s.plan_id").
				Where("s.status IN ? AND p.id IS NULL", []string{"active", "scheduled"}).Count(&missing).Error
			if err != nil || missing > 0 {
				return fmt.Errorf("active/scheduled subscriptions without a plan: %d", missing)
			}
		}
		if err := convertScopes(db, item.table, false); err != nil {
			return err
		}
	}
	groups, err := inspectUpgrades(db)
	if err != nil {
		return err
	}
	fmt.Printf("active upgrades to convert: %d\n", len(groups))
	if !apply {
		return nil
	}
	// Additive DDL is checked individually because MySQL commits it implicitly.
	for _, item := range []struct {
		table  string
		model  any
		fields []string
	}{
		{"subscription_plans", &model.SubscriptionPlan{}, []string{"QuotaScope"}},
		{"user_subscriptions", &model.UserSubscription{}, []string{"QuotaScope", "QuotaResetPeriod", "QuotaResetCustomSeconds"}},
		{"subscription_orders", &model.SubscriptionOrder{}, []string{"Terms", "ReservedUntil", "PaidAt", "FulfilledSubscriptionID"}},
		{"users", &model.User{}, []string{"SubscriptionGroup"}},
	} {
		for _, field := range item.fields {
			if !db.Migrator().HasColumn(item.model, field) {
				if err := db.Migrator().AddColumn(item.model, field); err != nil {
					return fmt.Errorf("%s: cannot add %s", item.table, field)
				}
			}
		}
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("subscription_orders").
			Where("status = ? AND (terms IS NULL OR terms = '')", "pending").
			UpdateColumn("status", model.SubscriptionOrderUnverified).Error; err != nil {
			return errors.New("cannot classify historical payment orders")
		}
		for _, group := range groups {
			if err := tx.Model(&model.User{}).Where("id = ?", group.UserID).
				UpdateColumns(map[string]any{"group": group.Previous, "subscription_group": group.Upgrade}).Error; err != nil {
				return fmt.Errorf("cannot migrate base group for user %d", group.UserID)
			}
		}
		for _, table := range []string{"subscription_plans", "user_subscriptions"} {
			if !tx.Migrator().HasColumn(table, "entitlement_group") {
				continue
			}
			if err := convertScopes(tx, table, true); err != nil {
				return err
			}
			if table == "subscription_plans" {
				if err := tx.Exec("UPDATE subscription_plans SET allow_balance_pay = COALESCE(allow_balance_pay, ?), allow_wallet_overflow = COALESCE(allow_wallet_overflow, ?)", true, true).Error; err != nil {
					return errors.New("cannot normalize subscription plan flags")
				}
			} else {
				if err := tx.Exec(`UPDATE user_subscriptions SET allocation_count = CASE WHEN allocation_count IS NULL OR allocation_count < 1 THEN 1 ELSE allocation_count END,
					quota_reset_period = COALESCE((SELECT quota_reset_period FROM subscription_plans WHERE id = user_subscriptions.plan_id), 'never'),
					quota_reset_custom_seconds = COALESCE((SELECT quota_reset_custom_seconds FROM subscription_plans WHERE id = user_subscriptions.plan_id), 0)`).Error; err != nil {
					return errors.New("cannot snapshot existing reset rules")
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, table := range []string{"subscription_plans", "user_subscriptions"} {
		m := db.Migrator()
		if !m.HasColumn(table, "entitlement_group") {
			continue
		}
		// SQLite requires indexes using a column to be removed before dropping it.
		if m.HasIndex(table, "idx_user_subscriptions_entitlement_group") {
			if err := m.DropIndex(table, "idx_user_subscriptions_entitlement_group"); err != nil {
				return fmt.Errorf("%s: cannot remove old index", table)
			}
		}
		if err := db.Exec("ALTER TABLE " + table + " DROP COLUMN entitlement_group").Error; err != nil {
			return fmt.Errorf("%s: cannot remove old entitlement column", table)
		}
	}
	fmt.Println("subscription group conversion complete")
	return nil
}

type upgradeRow struct {
	UserID   int
	Upgrade  string
	Previous string
	Current  string
}

func inspectUpgrades(db *gorm.DB) ([]upgradeRow, error) {
	if !db.Migrator().HasColumn("user_subscriptions", "entitlement_group") {
		return nil, nil
	}
	group := "`group`"
	if db.Dialector.Name() == "postgres" {
		group = `"group"`
	}
	now := common.GetTimestamp()
	var overdue int64
	if err := db.Table("user_subscriptions").Where("status = ? AND end_time <= ? AND (upgrade_group <> '' OR downgrade_group <> '')", "active", now).
		Count(&overdue).Error; err != nil || overdue > 0 {
		return nil, fmt.Errorf("subscription group transitions still due: %d; run the old lifecycle before freezing", overdue)
	}
	var overlap int64
	if err := db.Table("user_subscriptions s").
		Joins("JOIN user_subscriptions other ON other.user_id = s.user_id AND other.id > s.id").
		Where("s.status IN ? AND other.status IN ?", []string{"active", "scheduled"}, []string{"active", "scheduled"}).
		Where("s.end_time > ? AND other.end_time > ? AND s.start_time < other.end_time AND other.start_time < s.end_time", now, now).
		Where("s.upgrade_group <> '' AND other.upgrade_group <> '' AND (s.upgrade_group <> other.upgrade_group OR s.downgrade_group <> other.downgrade_group)").
		Count(&overlap).Error; err != nil || overlap > 0 {
		return nil, fmt.Errorf("overlapping incompatible subscription upgrade windows: %d", overlap)
	}
	var rows []upgradeRow
	if err := db.Table("user_subscriptions s").
		Select("s.user_id, s.upgrade_group AS upgrade, s.prev_user_group AS previous, u."+group+" AS current").
		Joins("JOIN users u ON u.id = s.user_id").
		Where("s.status = ? AND s.start_time <= ? AND s.end_time > ? AND s.upgrade_group <> ''", "active", now, now).
		Order("s.user_id, s.id").Find(&rows).Error; err != nil {
		return nil, errors.New("cannot inspect active subscription group upgrades")
	}
	seen := make(map[int]upgradeRow, len(rows))
	for _, row := range rows {
		if row.Previous == "" || (row.Current != row.Upgrade && row.Current != row.Previous) {
			return nil, fmt.Errorf("ambiguous base group for user %d; reconcile the original subscription before applying", row.UserID)
		}
		if existing, ok := seen[row.UserID]; ok && existing != row {
			return nil, fmt.Errorf("conflicting active group upgrades for user %d", row.UserID)
		}
		seen[row.UserID] = row
	}
	out := make([]upgradeRow, 0, len(seen))
	for _, row := range seen {
		out = append(out, row)
	}
	return out, nil
}

func convertScopes(db *gorm.DB, table string, apply bool) error {
	lastID := 0
	for {
		var rows []legacyScopeRow
		if err := db.Table(table).Select("id, entitlement_group, grant_groups").
			Where("id > ?", lastID).Order("id").Limit(500).Find(&rows).Error; err != nil {
			return fmt.Errorf("%s: cannot read legacy records after ID %d", table, lastID)
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			lastID = row.ID
			oldGroup := strings.TrimSpace(row.EntitlementGroup.String)
			groups := model.MergeGroupNames(row.GrantGroups, model.GroupNames{oldGroup})
			scope := model.SubscriptionQuotaGeneral
			if oldGroup != "" {
				scope = model.SubscriptionQuotaGroups
			} else if len(groups) > 0 {
				scope = model.SubscriptionQuotaGroupsAndGeneral
			}
			if apply {
				result := db.Table(table).Where("id = ?", row.ID).Updates(map[string]any{"grant_groups": groups, "quota_scope": scope})
				if result.Error != nil {
					return fmt.Errorf("%s: cannot update ID %d", table, row.ID)
				}
			}
		}
	}
}

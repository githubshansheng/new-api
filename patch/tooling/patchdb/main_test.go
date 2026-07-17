package main

import (
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const legacySQLiteSchema = `
CREATE TABLE "liandong_products" (
  "id" integer PRIMARY KEY AUTOINCREMENT,
  "business_type" varchar(32) NOT NULL,
  "name" varchar(128) NOT NULL,
  "goods_key" varchar(128) NOT NULL,
  "quota_amount" bigint NOT NULL,
  "plan_id" integer,
  "expected_amount_minor" bigint NOT NULL,
  "currency" varchar(8) NOT NULL,
  "enabled" numeric,
  "sort_order" integer,
  "created_by" integer,
  "updated_by" integer,
  "created_at" bigint,
  "updated_at" bigint
);
CREATE UNIQUE INDEX "idx_liandong_products_goods_key" ON "liandong_products" ("goods_key");
CREATE INDEX "idx_liandong_products_business_type" ON "liandong_products" ("business_type");
CREATE INDEX "idx_liandong_products_plan_id" ON "liandong_products" ("plan_id");

CREATE TABLE "liandong_orders" (
  "id" integer PRIMARY KEY AUTOINCREMENT,
  "local_trade_no" varchar(128) NOT NULL,
  "provider_trade_no" varchar(128),
  "user_id" integer NOT NULL,
  "product_id" integer NOT NULL,
  "product_name_snapshot" varchar(128) NOT NULL,
  "business_type" varchar(32) NOT NULL,
  "target_id" integer,
  "goods_key_snapshot" varchar(128) NOT NULL,
  "contact_snapshot" varchar(12) NOT NULL,
  "j_uuid_snapshot" varchar(128) NOT NULL,
  "expected_amount_minor" bigint NOT NULL,
  "currency_snapshot" varchar(8) NOT NULL,
  "fulfillment_snapshot" text NOT NULL,
  "payment_status" varchar(32) NOT NULL,
  "fulfillment_status" varchar(32) NOT NULL,
  "last_check_at" bigint,
  "next_check_at" bigint,
  "check_deadline_at" bigint,
  "check_count" integer,
  "consecutive_error_count" integer,
  "check_lock_until" bigint,
  "provider_summary" text,
  "last_error" text,
  "paid_at" bigint,
  "fulfilled_at" bigint,
  "created_at" bigint,
  "updated_at" bigint
);
CREATE UNIQUE INDEX "idx_liandong_orders_local_trade_no" ON "liandong_orders" ("local_trade_no");
CREATE UNIQUE INDEX "idx_liandong_orders_provider_trade_no" ON "liandong_orders" ("provider_trade_no");
CREATE UNIQUE INDEX "idx_liandong_orders_contact_snapshot" ON "liandong_orders" ("contact_snapshot");
CREATE INDEX "idx_liandong_orders_user_id" ON "liandong_orders" ("user_id");
CREATE INDEX "idx_liandong_orders_product_id" ON "liandong_orders" ("product_id");
CREATE INDEX "idx_liandong_orders_business_type" ON "liandong_orders" ("business_type");
CREATE INDEX "idx_liandong_orders_target_id" ON "liandong_orders" ("target_id");
CREATE INDEX "idx_liandong_orders_payment_status" ON "liandong_orders" ("payment_status");
CREATE INDEX "idx_liandong_orders_fulfillment_status" ON "liandong_orders" ("fulfillment_status");
CREATE INDEX "idx_liandong_orders_next_check_at" ON "liandong_orders" ("next_check_at");
CREATE INDEX "idx_liandong_orders_check_deadline_at" ON "liandong_orders" ("check_deadline_at");
CREATE INDEX "idx_liandong_orders_check_lock_until" ON "liandong_orders" ("check_lock_until");
CREATE INDEX "idx_liandong_orders_created_at" ON "liandong_orders" ("created_at");
`

func TestPasswordFromSecureInputReadsStdin(t *testing.T) {
	originalStdin := os.Stdin
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		os.Stdin = originalStdin
		_ = reader.Close()
		_ = writer.Close()
	})
	_, err = writer.WriteString("secret-value\r\n")
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	os.Stdin = reader
	t.Setenv("PATCH_DB_PASSWORD_STDIN", "1")
	t.Setenv("PATCH_DB_PASSWORD_FD", "")

	password, provided, err := passwordFromSecureInput()

	require.NoError(t, err)
	assert.True(t, provided)
	assert.Equal(t, "secret-value", password)
}

func TestPasswordFromSecureInputRejectsMultipleSources(t *testing.T) {
	t.Setenv("PATCH_DB_PASSWORD_STDIN", "1")
	t.Setenv("PATCH_DB_PASSWORD_FD", "9")

	_, _, err := passwordFromSecureInput()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be used together")
}

const legacySQLiteData = `
INSERT INTO "liandong_products" (
  "id", "business_type", "name", "goods_key", "quota_amount", "plan_id",
  "expected_amount_minor", "currency", "enabled", "sort_order", "created_by",
  "updated_by", "created_at", "updated_at"
) VALUES (7, 'quota', 'Legacy Product', 'legacy-key', 1000, NULL, 1999, 'CNY', 1, 5, 1, 2, 100, 200);

INSERT INTO "liandong_orders" (
  "id", "local_trade_no", "provider_trade_no", "user_id", "product_id",
  "product_name_snapshot", "business_type", "target_id", "goods_key_snapshot",
  "contact_snapshot", "j_uuid_snapshot", "expected_amount_minor",
  "currency_snapshot", "fulfillment_snapshot", "payment_status",
  "fulfillment_status", "last_check_at", "next_check_at", "check_deadline_at",
  "check_count", "consecutive_error_count", "check_lock_until",
  "provider_summary", "last_error", "paid_at", "fulfilled_at", "created_at",
  "updated_at"
) VALUES (
  9, 'LD-LEGACY', 'PROVIDER-1', 11, 7, 'Legacy Product', 'quota', 0,
  'legacy-key', '138001380001', 'juuid', 1999, 'CNY', '{"quota_amount":1000}',
  'pending', 'waiting', 1, 2, 3, 4, 5, 6, 'summary', 'none', 7, 8, 9, 10
);
`

func TestSQLiteFreshMigrationAndRerun(t *testing.T) {
	db, closeDB, config := openTestSQLite(t)
	defer closeDB()
	identityBefore, err := identifyDatabase(config)
	require.NoError(t, err)

	state, reason, err := inspectSchema(db, databaseSQLite)
	require.NoError(t, err)
	assert.Equal(t, stateFresh, state)
	assert.Empty(t, reason)

	require.NoError(t, migrateDatabase(db, databaseSQLite, testDDLDir(t)))
	require.NoError(t, verifyTargetSchema(db, databaseSQLite))
	identityAfter, err := identifyDatabase(config)
	require.NoError(t, err)
	assert.Equal(t, identityBefore, identityAfter)
	require.NoError(
		t,
		requireSuccessfulMigrationHistory(db, databaseSQLite, testDDLDir(t)),
	)
	history, err := readMigrationHistory(db, databaseSQLite)
	require.NoError(t, err)
	require.NotNil(t, history)
	assert.Equal(t, migrationStateSuccess, history.state)
	assert.Len(t, history.checksum, sha256.Size*2)

	state, reason, err = inspectSchema(db, databaseSQLite)
	require.NoError(t, err)
	assert.Equal(t, stateTarget, state)
	assert.Empty(t, reason)

	require.NoError(t, migrateDatabase(db, databaseSQLite, testDDLDir(t)))
	require.NoError(t, verifyTargetSchema(db, databaseSQLite))
	assert.FileExists(t, config.sqlitePath)
}

func TestSQLiteLegacyMigrationPreservesData(t *testing.T) {
	db, closeDB, _ := openTestSQLite(t)
	defer closeDB()
	execSQLite(t, db, legacySQLiteSchema+legacySQLiteData)

	state, _, err := inspectSchema(db, databaseSQLite)
	require.NoError(t, err)
	require.Equal(t, stateLegacy, state)
	require.NoError(t, migrateDatabase(db, databaseSQLite, testDDLDir(t)))

	var (
		productName      string
		goodsType        string
		inventoryMode    string
		inventoryCap     int64
		thumbnailVersion int64
	)
	require.NoError(t, db.QueryRow(`
		SELECT name, goods_type, inventory_mode, inventory_capacity, thumbnail_version
		FROM liandong_products
		WHERE id = 7
	`).Scan(
		&productName,
		&goodsType,
		&inventoryMode,
		&inventoryCap,
		&thumbnailVersion,
	))
	assert.Equal(t, "Legacy Product", productName)
	assert.Equal(t, "card", goodsType)
	assert.Equal(t, "unlimited", inventoryMode)
	assert.Zero(t, inventoryCap)
	assert.Zero(t, thumbnailVersion)

	var (
		tradeNo         string
		inventoryCodeID int64
		expiresAt       int64
		closedReason    string
		latePayment     bool
	)
	require.NoError(t, db.QueryRow(`
		SELECT local_trade_no, inventory_code_id, expires_at, closed_reason, late_payment
		FROM liandong_orders
		WHERE id = 9
	`).Scan(&tradeNo, &inventoryCodeID, &expiresAt, &closedReason, &latePayment))
	assert.Equal(t, "LD-LEGACY", tradeNo)
	assert.Zero(t, inventoryCodeID)
	assert.Zero(t, expiresAt)
	assert.Empty(t, closedReason)
	assert.False(t, latePayment)
	require.NoError(t, verifyTargetSchema(db, databaseSQLite))
}

func TestSQLiteLegacyMigrationPreservesAutoincrementHighWaterMarks(t *testing.T) {
	db, closeDB, _ := openTestSQLite(t)
	defer closeDB()
	execSQLite(t, db, legacySQLiteSchema)
	execSQLite(t, db, `
		INSERT INTO liandong_products (
		  id, business_type, name, goods_key, quota_amount,
		  expected_amount_minor, currency
		) VALUES (100, 'quota', 'Deleted high product', 'deleted-high', 1, 1, 'CNY');
		DELETE FROM liandong_products WHERE id = 100;
		INSERT INTO liandong_products (
		  id, business_type, name, goods_key, quota_amount,
		  expected_amount_minor, currency
		) VALUES (7, 'quota', 'Remaining product', 'remaining', 1, 1, 'CNY');

		INSERT INTO liandong_orders (
		  id, local_trade_no, user_id, product_id, product_name_snapshot,
		  business_type, goods_key_snapshot, contact_snapshot, j_uuid_snapshot,
		  expected_amount_minor, currency_snapshot, fulfillment_snapshot,
		  payment_status, fulfillment_status
		) VALUES (
		  200, 'LD-DELETED-HIGH', 1, 7, 'Remaining product',
		  'quota', 'remaining', '138001380002', 'juuid',
		  1, 'CNY', '{}', 'pending', 'waiting'
		);
		DELETE FROM liandong_orders WHERE id = 200;
		INSERT INTO liandong_orders (
		  id, local_trade_no, user_id, product_id, product_name_snapshot,
		  business_type, goods_key_snapshot, contact_snapshot, j_uuid_snapshot,
		  expected_amount_minor, currency_snapshot, fulfillment_snapshot,
		  payment_status, fulfillment_status
		) VALUES (
		  9, 'LD-REMAINING', 1, 7, 'Remaining product',
		  'quota', 'remaining', '138001380003', 'juuid',
		  1, 'CNY', '{}', 'pending', 'waiting'
		);
	`)

	require.NoError(t, migrateDatabase(db, databaseSQLite, testDDLDir(t)))

	var productSequence, orderSequence int64
	require.NoError(t, db.QueryRow(
		`SELECT seq FROM sqlite_sequence WHERE name = 'liandong_products'`,
	).Scan(&productSequence))
	require.NoError(t, db.QueryRow(
		`SELECT seq FROM sqlite_sequence WHERE name = 'liandong_orders'`,
	).Scan(&orderSequence))
	assert.EqualValues(t, 100, productSequence)
	assert.EqualValues(t, 200, orderSequence)

	result, err := db.Exec(`
		INSERT INTO liandong_products (
		  business_type, goods_type, name, goods_key, quota_amount,
		  expected_amount_minor, currency, inventory_mode,
		  inventory_capacity, thumbnail_version
		) VALUES (
		  'quota', 'card', 'Next product', 'next-product', 1,
		  1, 'CNY', 'unlimited', 0, 0
		)
	`)
	require.NoError(t, err)
	nextID, err := result.LastInsertId()
	require.NoError(t, err)
	assert.EqualValues(t, 101, nextID)
}

func TestSQLiteUnsafeSchemaIsNotModified(t *testing.T) {
	tests := []struct {
		name   string
		change string
	}{
		{
			name:   "custom column",
			change: `ALTER TABLE "liandong_products" ADD COLUMN "custom_value" text;`,
		},
		{
			name:   "partial migration",
			change: `ALTER TABLE "liandong_products" ADD COLUMN "goods_type" varchar(32);`,
		},
		{
			name: "trigger",
			change: `
				CREATE TRIGGER liandong_products_audit
				AFTER UPDATE ON liandong_products
				BEGIN
				  SELECT 1;
				END;
			`,
		},
		{
			name: "incoming foreign key",
			change: `
				CREATE TABLE product_references (
				  id integer PRIMARY KEY,
				  product_id integer REFERENCES liandong_products(id)
				);
			`,
		},
		{
			name: "partial index",
			change: `
				DROP INDEX idx_liandong_products_business_type;
				CREATE INDEX idx_liandong_products_business_type
				ON liandong_products(business_type)
				WHERE enabled = 1;
			`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, closeDB, _ := openTestSQLite(t)
			defer closeDB()
			execSQLite(t, db, legacySQLiteSchema+legacySQLiteData+test.change)

			state, reason, err := inspectSchema(db, databaseSQLite)
			require.NoError(t, err)
			assert.Equal(t, stateUnsafe, state)
			assert.NotEmpty(t, reason)
			require.Error(t, migrateDatabase(db, databaseSQLite, testDDLDir(t)))

			var count int
			require.NoError(t, db.QueryRow(
				`SELECT COUNT(*) FROM liandong_products WHERE id = 7 AND name = 'Legacy Product'`,
			).Scan(&count))
			assert.Equal(t, 1, count)
			present, err := tableExists(
				db,
				databaseSQLite,
				"liandong_product_inventory_codes",
			)
			require.NoError(t, err)
			assert.False(t, present)
		})
	}
}

func TestSQLiteTargetRejectsMissingPrimaryKey(t *testing.T) {
	db, closeDB, _ := openTestSQLite(t)
	defer closeDB()
	require.NoError(t, migrateDatabase(db, databaseSQLite, testDDLDir(t)))
	execSQLite(t, db, `
		DROP INDEX idx_liandong_user_operation_leases_expires_at;
		ALTER TABLE liandong_user_operation_leases
		  RENAME TO liandong_user_operation_leases_old;
		CREATE TABLE liandong_user_operation_leases (
		  user_id integer NOT NULL,
		  token char(32) NOT NULL,
		  expires_at bigint NOT NULL,
		  updated_at bigint
		);
		CREATE INDEX idx_liandong_user_operation_leases_expires_at
		  ON liandong_user_operation_leases(expires_at);
		DROP TABLE liandong_user_operation_leases_old;
	`)

	state, reason, err := inspectSchema(db, databaseSQLite)
	require.NoError(t, err)
	assert.Equal(t, stateUnsafe, state)
	assert.Contains(t, reason, "primary key")
	require.Error(t, verifyTargetSchema(db, databaseSQLite))
}

func TestSQLiteMigrationErrorRollsBack(t *testing.T) {
	db, closeDB, config := openTestSQLite(t)
	execSQLite(t, db, legacySQLiteSchema+legacySQLiteData)

	ddl, err := os.ReadFile(filepath.Join(testDDLDir(t), "liandong-payment.sqlite.sql"))
	require.NoError(t, err)
	broken := strings.Replace(
		string(ddl),
		"COMMIT;",
		"INSERT INTO missing_patch_table VALUES (1);\nCOMMIT;",
		1,
	)
	brokenDDLDir := copySQLiteDDLBundle(t)
	require.NoError(t, os.WriteFile(
		filepath.Join(brokenDDLDir, "liandong-payment.sqlite.sql"),
		[]byte(broken),
		0o600,
	))

	require.Error(t, migrateDatabase(db, databaseSQLite, brokenDDLDir))
	closeDB()

	reopened, reopenedClose, err := openDatabase(config)
	require.NoError(t, err)
	defer reopenedClose()
	state, reason, err := inspectSchema(reopened, databaseSQLite)
	require.NoError(t, err)
	assert.Equal(t, stateLegacy, state)
	assert.Empty(t, reason)
	history, err := readMigrationHistory(reopened, databaseSQLite)
	require.NoError(t, err)
	require.NotNil(t, history)
	assert.Equal(t, migrationStateDirty, history.state)
	assert.NotEmpty(t, history.errorMessage)
	var count int
	require.NoError(t, reopened.QueryRow(
		`SELECT COUNT(*) FROM liandong_orders WHERE id = 9 AND local_trade_no = 'LD-LEGACY'`,
	).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestSQLiteMigrationRejectsChangedDDLChecksum(t *testing.T) {
	db, closeDB, _ := openTestSQLite(t)
	defer closeDB()
	require.NoError(t, migrateDatabase(db, databaseSQLite, testDDLDir(t)))

	changedDDLDir := copySQLiteDDLBundle(t)
	path := filepath.Join(changedDDLDir, "liandong-payment.sqlite-fresh.sql")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	data = append(data, []byte("\n-- changed after release\n")...)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	_, _, err = inspectMigrationHistory(
		db,
		databaseSQLite,
		changedDDLDir,
		stateTarget,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
	require.Error(t, migrateDatabase(db, databaseSQLite, changedDDLDir))
	require.NoError(t, verifyTargetSchema(db, databaseSQLite))
}

func TestBackupSQLiteCreatesIndependentValidCopy(t *testing.T) {
	db, closeDB, config := openTestSQLite(t)
	execSQLite(t, db, legacySQLiteSchema+legacySQLiteData)
	closeDB()

	output := filepath.Join(t.TempDir(), "database.sqlite")
	require.NoError(t, backupDatabase(config, output))
	assert.FileExists(t, output)

	backupDB, err := sql.Open("sqlite", output)
	require.NoError(t, err)
	defer backupDB.Close()
	var integrity string
	require.NoError(t, backupDB.QueryRow("PRAGMA integrity_check").Scan(&integrity))
	assert.Equal(t, "ok", strings.ToLower(strings.TrimSpace(integrity)))
	var name string
	require.NoError(t, backupDB.QueryRow(
		`SELECT name FROM liandong_products WHERE id = 7`,
	).Scan(&name))
	assert.Equal(t, "Legacy Product", name)
}

func TestRequireLiandongDisabled(t *testing.T) {
	db, closeDB, _ := openTestSQLite(t)
	defer closeDB()
	execSQLite(t, db, `
		CREATE TABLE options ("key" text PRIMARY KEY, "value" text);
		INSERT INTO options ("key", "value") VALUES
		  ('LiandongEnabled', 'false'),
		  ('LiandongCreateEnabled', 'true');
	`)

	err := requireLiandongDisabled(db, databaseSQLite)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LiandongCreateEnabled")
	require.NoError(t, func() error {
		_, err := db.Exec(
			`UPDATE options SET "value" = 'false' WHERE "key" = 'LiandongCreateEnabled'`,
		)
		return err
	}())
	require.NoError(t, requireLiandongDisabled(db, databaseSQLite))
}

func TestOpenDatabaseRejectsMissingSQLiteFile(t *testing.T) {
	config := &databaseConfig{
		kind:       databaseSQLite,
		sqlitePath: filepath.Join(t.TempDir(), "missing.sqlite"),
	}
	_, _, err := openDatabase(config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	assert.NoFileExists(t, config.sqlitePath)
}

func TestResolveDatabaseConfigPreservesPasswordWhitespace(t *testing.T) {
	t.Setenv("PATCH_DB_TYPE", databaseMySQL)
	t.Setenv("PATCH_DB_HOST", "127.0.0.1")
	t.Setenv("PATCH_DB_PORT", "3306")
	t.Setenv("PATCH_DB_NAME", "new_api")
	t.Setenv("PATCH_DB_USER", "patch_user")
	t.Setenv("PATCH_DB_PASSWORD", " secret with spaces ")
	t.Setenv("PATCH_SQL_DSN", "")
	t.Setenv("SQL_DSN", "")

	config, err := resolveDatabaseConfig(t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, config.mysql)
	assert.Equal(t, " secret with spaces ", config.mysql.Passwd)
}

func TestCommandEnvironmentWithoutDatabaseSecrets(t *testing.T) {
	t.Setenv("PATCH_DB_PASSWORD", "patch-secret")
	t.Setenv("PATCH_SQL_DSN", "patch-dsn-secret")
	t.Setenv("SQL_DSN", "application-dsn-secret")
	t.Setenv("MYSQL_PWD", "mysql-secret")
	t.Setenv("PGPASSWORD", "postgres-secret")
	t.Setenv("PATCH_TEST_VISIBLE", "visible")

	environment := commandEnvironmentWithoutDatabaseSecrets()
	joined := strings.Join(environment, "\n")
	assert.NotContains(t, joined, "patch-secret")
	assert.NotContains(t, joined, "patch-dsn-secret")
	assert.NotContains(t, joined, "application-dsn-secret")
	assert.NotContains(t, joined, "mysql-secret")
	assert.NotContains(t, joined, "postgres-secret")
	assert.Contains(t, joined, "PATCH_TEST_VISIBLE=visible")
}

func TestVerifyPostgresCharacterVaryingMigrationTypes(t *testing.T) {
	productColumns := map[string]columnInfo{
		"goods_type": {
			dataType: "character varying",
			length:   32,
		},
		"inventory_mode": {
			dataType: "character varying",
			length:   32,
		},
		"inventory_capacity": {
			dataType: "bigint",
		},
		"thumbnail_version": {
			dataType: "bigint",
		},
	}
	orderColumns := map[string]columnInfo{
		"inventory_code_id": {
			dataType: "bigint",
		},
		"expires_at": {
			dataType: "bigint",
		},
		"closed_reason": {
			dataType: "character varying",
			length:   64,
		},
		"late_payment": {
			dataType: "boolean",
		},
	}

	require.NoError(t, verifyNewColumnTypes(databasePostgres, productColumns, true))
	require.NoError(t, verifyNewColumnTypes(databasePostgres, orderColumns, false))
}

func TestVerifyNewTableColumnContracts(t *testing.T) {
	db, closeDB, _ := openTestSQLite(t)
	defer closeDB()
	require.NoError(t, migrateDatabase(db, databaseSQLite, testDDLDir(t)))

	for table := range newTableColumnContracts {
		columns, err := readColumns(db, databaseSQLite, table)
		require.NoError(t, err)
		require.NoError(
			t,
			verifyNewTableColumnContracts(databaseSQLite, table, columns),
		)
	}

	tests := []struct {
		name   string
		table  string
		column string
		change func(columnInfo) columnInfo
	}{
		{
			name:   "thumbnail blob type",
			table:  "liandong_product_thumbnails",
			column: "data",
			change: func(info columnInfo) columnInfo {
				info.dataType = "text"
				return info
			},
		},
		{
			name:   "thumbnail content type nullability",
			table:  "liandong_product_thumbnails",
			column: "content_type",
			change: func(info columnInfo) columnInfo {
				info.nullable = true
				return info
			},
		},
		{
			name:   "inventory code length",
			table:  "liandong_product_inventory_codes",
			column: "code",
			change: func(info columnInfo) columnInfo {
				info.dataType = "char(64)"
				return info
			},
		},
		{
			name:   "inventory product nullability",
			table:  "liandong_product_inventory_codes",
			column: "product_id",
			change: func(info columnInfo) columnInfo {
				info.nullable = true
				return info
			},
		},
		{
			name:   "lease expiry nullability",
			table:  "liandong_user_operation_leases",
			column: "expires_at",
			change: func(info columnInfo) columnInfo {
				info.nullable = true
				return info
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			columns, err := readColumns(db, databaseSQLite, test.table)
			require.NoError(t, err)
			columns[test.column] = test.change(columns[test.column])
			require.Error(
				t,
				verifyNewTableColumnContracts(
					databaseSQLite,
					test.table,
					columns,
				),
			)
		})
	}
}

func TestCompareIndexDefinitionRejectsChangedContract(t *testing.T) {
	expected := indexDefinition{
		columns: []string{"product_id", "status"},
	}
	tests := []struct {
		name   string
		actual indexDefinition
	}{
		{
			name: "missing",
		},
		{
			name: "wrong column order",
			actual: indexDefinition{
				columns: []string{"status", "product_id"},
			},
		},
		{
			name: "unexpected unique",
			actual: indexDefinition{
				columns: []string{"product_id", "status"},
				unique:  true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := compareIndexDefinition(
				"liandong_product_inventory_codes",
				"idx_liandong_inventory_product_status",
				expected,
				test.actual,
			)
			require.Error(t, err)
		})
	}
}

func openTestSQLite(t *testing.T) (*sql.DB, func(), *databaseConfig) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "database.sqlite")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	config := &databaseConfig{
		kind:       databaseSQLite,
		appDir:     filepath.Dir(path),
		sqlitePath: path,
	}
	db, closeDB, err := openDatabase(config)
	require.NoError(t, err)
	return db, closeDB, config
}

func execSQLite(t *testing.T, db *sql.DB, statements string) {
	t.Helper()
	_, err := db.Exec(statements)
	require.NoError(t, err)
}

func testDDLDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "DDL"))
	require.NoError(t, err)
	return path
}

func copySQLiteDDLBundle(t *testing.T) string {
	t.Helper()
	target := t.TempDir()
	for _, filename := range []string{
		"liandong-payment.sqlite-fresh.sql",
		"liandong-payment.sqlite.sql",
	} {
		data, err := os.ReadFile(filepath.Join(testDDLDir(t), filename))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(target, filename), data, 0o600))
	}
	return target
}

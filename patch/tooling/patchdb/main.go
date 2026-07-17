package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	_ "modernc.org/sqlite"
)

const (
	databaseSQLite   = "sqlite"
	databaseMySQL    = "mysql"
	databasePostgres = "postgres"

	stateFresh             = "fresh"
	stateLegacy            = "legacy"
	stateTarget            = "target"
	stateCompatiblePartial = "compatible-partial"
	stateUnsafe            = "unsafe"

	patchID               = "new-api-20260716"
	migrationHistoryTable = "new_api_patch_history"
	migrationStateDirty   = "dirty"
	migrationStateSuccess = "success"
	migrationLockName     = "new-api:liandong:20260716"
	migrationLockKey      = int64(2026071601)
)

type indexDefinition struct {
	columns             []string
	unique              bool
	partial             bool
	expression          bool
	descending          bool
	nonDefaultCollation bool
	prefix              bool
	valid               bool
}

var (
	postgresSchemaPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)
	liandongTables        = []string{
		"liandong_products",
		"liandong_orders",
		"liandong_product_thumbnails",
		"liandong_product_inventory_codes",
		"liandong_user_operation_leases",
	}
	legacyProductColumns = []string{
		"id", "business_type", "name", "goods_key", "quota_amount", "plan_id",
		"expected_amount_minor", "currency", "enabled", "sort_order", "created_by",
		"updated_by", "created_at", "updated_at",
	}
	legacyOrderColumns = []string{
		"id", "local_trade_no", "provider_trade_no", "user_id", "product_id",
		"product_name_snapshot", "business_type", "target_id", "goods_key_snapshot",
		"contact_snapshot", "j_uuid_snapshot", "expected_amount_minor",
		"currency_snapshot", "fulfillment_snapshot", "payment_status",
		"fulfillment_status", "last_check_at", "next_check_at", "check_deadline_at",
		"check_count", "consecutive_error_count", "check_lock_until",
		"provider_summary", "last_error", "paid_at", "fulfilled_at", "created_at",
		"updated_at",
	}
	targetProductColumns = appendCopy(legacyProductColumns,
		"goods_type", "inventory_mode", "inventory_capacity", "thumbnail_version")
	targetOrderColumns = appendCopy(legacyOrderColumns,
		"inventory_code_id", "expires_at", "closed_reason", "late_payment")
	requiredNewTableColumns = map[string][]string{
		"liandong_product_thumbnails": {
			"product_id", "content_type", "data", "width", "height", "size",
			"version", "created_at", "updated_at",
		},
		"liandong_product_inventory_codes": {
			"id", "product_id", "code", "name", "status", "reserved_order_id",
			"reserved_trade_no", "reserved_user_id", "reserved_at", "consumed_at",
			"disabled_at", "created_by", "created_at", "updated_at",
		},
		"liandong_user_operation_leases": {
			"user_id", "token", "expires_at", "updated_at",
		},
	}
	legacyIndexes = map[string]map[string]indexDefinition{
		"liandong_products": {
			"idx_liandong_products_goods_key": {
				columns: []string{"goods_key"},
				unique:  true,
			},
			"idx_liandong_products_business_type": {
				columns: []string{"business_type"},
			},
			"idx_liandong_products_plan_id": {
				columns: []string{"plan_id"},
			},
		},
		"liandong_orders": {
			"idx_liandong_orders_local_trade_no": {
				columns: []string{"local_trade_no"},
				unique:  true,
			},
			"idx_liandong_orders_provider_trade_no": {
				columns: []string{"provider_trade_no"},
				unique:  true,
			},
			"idx_liandong_orders_contact_snapshot": {
				columns: []string{"contact_snapshot"},
				unique:  true,
			},
			"idx_liandong_orders_user_id": {
				columns: []string{"user_id"},
			},
			"idx_liandong_orders_product_id": {
				columns: []string{"product_id"},
			},
			"idx_liandong_orders_business_type": {
				columns: []string{"business_type"},
			},
			"idx_liandong_orders_target_id": {
				columns: []string{"target_id"},
			},
			"idx_liandong_orders_payment_status": {
				columns: []string{"payment_status"},
			},
			"idx_liandong_orders_fulfillment_status": {
				columns: []string{"fulfillment_status"},
			},
			"idx_liandong_orders_next_check_at": {
				columns: []string{"next_check_at"},
			},
			"idx_liandong_orders_check_deadline_at": {
				columns: []string{"check_deadline_at"},
			},
			"idx_liandong_orders_check_lock_until": {
				columns: []string{"check_lock_until"},
			},
			"idx_liandong_orders_created_at": {
				columns: []string{"created_at"},
			},
		},
	}
	targetIndexes = map[string]map[string]indexDefinition{
		"liandong_products": {
			"idx_liandong_products_goods_key": {
				columns: []string{"goods_key"},
				unique:  true,
			},
			"idx_liandong_products_business_type": {
				columns: []string{"business_type"},
			},
			"idx_liandong_products_goods_type": {
				columns: []string{"goods_type"},
			},
			"idx_liandong_products_plan_id": {
				columns: []string{"plan_id"},
			},
			"idx_liandong_products_inventory_mode": {
				columns: []string{"inventory_mode"},
			},
		},
		"liandong_orders": {
			"idx_liandong_orders_local_trade_no": {
				columns: []string{"local_trade_no"},
				unique:  true,
			},
			"idx_liandong_orders_provider_trade_no": {
				columns: []string{"provider_trade_no"},
				unique:  true,
			},
			"idx_liandong_orders_contact_snapshot": {
				columns: []string{"contact_snapshot"},
				unique:  true,
			},
			"idx_liandong_orders_user_id": {
				columns: []string{"user_id"},
			},
			"idx_liandong_orders_product_id": {
				columns: []string{"product_id"},
			},
			"idx_liandong_orders_business_type": {
				columns: []string{"business_type"},
			},
			"idx_liandong_orders_target_id": {
				columns: []string{"target_id"},
			},
			"idx_liandong_orders_inventory_code_id": {
				columns: []string{"inventory_code_id"},
			},
			"idx_liandong_orders_payment_status": {
				columns: []string{"payment_status"},
			},
			"idx_liandong_orders_fulfillment_status": {
				columns: []string{"fulfillment_status"},
			},
			"idx_liandong_orders_next_check_at": {
				columns: []string{"next_check_at"},
			},
			"idx_liandong_orders_check_deadline_at": {
				columns: []string{"check_deadline_at"},
			},
			"idx_liandong_orders_check_lock_until": {
				columns: []string{"check_lock_until"},
			},
			"idx_liandong_orders_expires_at": {
				columns: []string{"expires_at"},
			},
			"idx_liandong_orders_closed_reason": {
				columns: []string{"closed_reason"},
			},
			"idx_liandong_orders_late_payment": {
				columns: []string{"late_payment"},
			},
			"idx_liandong_orders_created_at": {
				columns: []string{"created_at"},
			},
		},
		"liandong_product_inventory_codes": {
			"idx_liandong_product_inventory_codes_code": {
				columns: []string{"code"},
				unique:  true,
			},
			"idx_liandong_inventory_product_status": {
				columns: []string{"product_id", "status"},
			},
			"idx_liandong_product_inventory_codes_reserved_order_id": {
				columns: []string{"reserved_order_id"},
			},
			"idx_liandong_product_inventory_codes_reserved_trade_no": {
				columns: []string{"reserved_trade_no"},
			},
			"idx_liandong_product_inventory_codes_reserved_user_id": {
				columns: []string{"reserved_user_id"},
			},
		},
		"liandong_user_operation_leases": {
			"idx_liandong_user_operation_leases_expires_at": {
				columns: []string{"expires_at"},
			},
		},
	}
)

type databaseConfig struct {
	kind       string
	appDir     string
	sqlitePath string
	mysql      *mysqlDriver.Config
	postgres   *pgx.ConnConfig
	sslMode    string
	schema     string
}

type columnInfo struct {
	dataType      string
	length        int64
	nullable      bool
	defaultValue  string
	primaryKey    bool
	autoIncrement bool
	hidden        bool
}

type columnContract struct {
	dataTypes     []string
	length        int64
	nullable      bool
	primaryKey    bool
	autoIncrement bool
}

type databaseExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type connectionExecutor struct {
	conn *sql.Conn
}

type migrationHistory struct {
	patchID      string
	checksum     string
	state        string
	databaseType string
	startedAt    int64
	finishedAt   int64
	errorMessage string
}

type migrationPlan struct {
	state    string
	checksum string
	filename string
	ddl      []byte
	history  *migrationHistory
}

func (c connectionExecutor) Exec(query string, args ...any) (sql.Result, error) {
	return c.conn.ExecContext(context.Background(), query, args...)
}

func (c connectionExecutor) ExecContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	return c.conn.ExecContext(ctx, query, args...)
}

func (c connectionExecutor) Query(query string, args ...any) (*sql.Rows, error) {
	return c.conn.QueryContext(context.Background(), query, args...)
}

func (c connectionExecutor) QueryContext(
	ctx context.Context,
	query string,
	args ...any,
) (*sql.Rows, error) {
	return c.conn.QueryContext(ctx, query, args...)
}

func (c connectionExecutor) QueryRow(query string, args ...any) *sql.Row {
	return c.conn.QueryRowContext(context.Background(), query, args...)
}

func (c connectionExecutor) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) *sql.Row {
	return c.conn.QueryRowContext(ctx, query, args...)
}

var newTableColumnContracts = map[string]map[string]columnContract{
	"liandong_product_thumbnails": {
		"product_id": {
			dataTypes:  []string{"bigint", "integer", "int"},
			primaryKey: true,
		},
		"content_type": {
			dataTypes: []string{"varchar", "varchar(32)", "character varying"},
			length:    32,
		},
		"data": {
			dataTypes: []string{"blob", "longblob", "bytea"},
			nullable:  true,
		},
		"width": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
		"height": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
		"size": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
		"version": {
			dataTypes: []string{"bigint", "integer", "int"},
		},
		"created_at": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
		"updated_at": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
	},
	"liandong_product_inventory_codes": {
		"id": {
			dataTypes:     []string{"bigint", "integer", "int"},
			primaryKey:    true,
			autoIncrement: true,
		},
		"product_id": {
			dataTypes: []string{"bigint", "integer", "int"},
		},
		"code": {
			dataTypes: []string{"char", "char(32)", "character"},
			length:    32,
		},
		"name": {
			dataTypes: []string{"varchar", "varchar(128)", "character varying"},
			length:    128,
		},
		"status": {
			dataTypes: []string{"varchar", "varchar(32)", "character varying"},
			length:    32,
		},
		"reserved_order_id": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
		"reserved_trade_no": {
			dataTypes: []string{"varchar", "varchar(128)", "character varying"},
			length:    128,
			nullable:  true,
		},
		"reserved_user_id": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
		"reserved_at": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
		"consumed_at": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
		"disabled_at": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
		"created_by": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
		"created_at": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
		"updated_at": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
	},
	"liandong_user_operation_leases": {
		"user_id": {
			dataTypes:  []string{"bigint", "integer", "int"},
			primaryKey: true,
		},
		"token": {
			dataTypes: []string{"char", "char(32)", "character"},
			length:    32,
		},
		"expires_at": {
			dataTypes: []string{"bigint", "integer", "int"},
		},
		"updated_at": {
			dataTypes: []string{"bigint", "integer", "int"},
			nullable:  true,
		},
	},
}

func main() {
	if len(os.Args) < 2 {
		exitError(errors.New("usage: patchdb <identity|inspect|switches|backup|migrate|verify> [options]"))
	}

	command := os.Args[1]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	appDir := flags.String("app-dir", ".", "application directory")
	ddlDir := flags.String("ddl-dir", "", "DDL directory")
	output := flags.String("output", "", "backup output path")
	if err := flags.Parse(os.Args[2:]); err != nil {
		exitError(err)
	}

	absoluteAppDir, err := filepath.Abs(*appDir)
	if err != nil {
		exitError(fmt.Errorf("resolve application directory: %w", err))
	}
	config, err := resolveDatabaseConfig(absoluteAppDir)
	if err != nil {
		exitError(err)
	}

	switch command {
	case "identity":
		identity, err := identifyDatabase(config)
		if err != nil {
			exitError(err)
		}
		fmt.Printf(
			"DB_TYPE=%s\nDB_SUMMARY=%s\nDB_FINGERPRINT=%x\n",
			config.kind,
			identity,
			sha256.Sum256([]byte(identity)),
		)
	case "inspect":
		db, closeDB, err := openDatabase(config)
		if err != nil {
			exitError(err)
		}
		defer closeDB()
		state, reason, err := inspectSchema(db, config.kind)
		if err != nil {
			exitError(err)
		}
		fmt.Printf("DB_TYPE=%s\nSCHEMA_STATE=%s\n", config.kind, state)
		if reason != "" {
			fmt.Printf("SCHEMA_REASON=%s\n", reason)
		}
		if strings.TrimSpace(*ddlDir) != "" {
			history, checksum, err := inspectMigrationHistory(
				db,
				config.kind,
				*ddlDir,
				state,
			)
			if err != nil {
				exitError(err)
			}
			historyState := "missing"
			if history != nil {
				historyState = history.state
			}
			fmt.Printf(
				"PATCH_HISTORY_STATE=%s\nPATCH_HISTORY_CHECKSUM=%s\n",
				historyState,
				checksum,
			)
		}
	case "switches":
		db, closeDB, err := openDatabase(config)
		if err != nil {
			exitError(err)
		}
		defer closeDB()
		if err := requireLiandongDisabled(db, config.kind); err != nil {
			exitError(err)
		}
		fmt.Println("LIANDONG_SWITCHES=disabled")
	case "backup":
		if strings.TrimSpace(*output) == "" {
			exitError(errors.New("--output is required"))
		}
		if err := backupDatabase(config, *output); err != nil {
			exitError(err)
		}
		fmt.Printf("BACKUP_PATH=%s\n", *output)
	case "migrate":
		if strings.TrimSpace(*ddlDir) == "" {
			exitError(errors.New("--ddl-dir is required"))
		}
		db, closeDB, err := openDatabase(config)
		if err != nil {
			exitError(err)
		}
		defer closeDB()
		if err := requireLiandongDisabled(db, config.kind); err != nil {
			exitError(err)
		}
		if err := migrateDatabase(db, config.kind, *ddlDir); err != nil {
			exitError(err)
		}
		fmt.Println("MIGRATION=complete")
	case "verify":
		if strings.TrimSpace(*ddlDir) == "" {
			exitError(errors.New("--ddl-dir is required"))
		}
		db, closeDB, err := openDatabase(config)
		if err != nil {
			exitError(err)
		}
		defer closeDB()
		if err := verifyTargetSchema(db, config.kind); err != nil {
			exitError(err)
		}
		if err := requireSuccessfulMigrationHistory(
			db,
			config.kind,
			*ddlDir,
		); err != nil {
			exitError(err)
		}
		fmt.Println("SCHEMA_VERIFY=ok")
	default:
		exitError(fmt.Errorf("unknown command %q", command))
	}
}

func appendCopy(values []string, extra ...string) []string {
	result := make([]string, 0, len(values)+len(extra))
	result = append(result, values...)
	result = append(result, extra...)
	return result
}

func exitError(err error) {
	fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
	os.Exit(1)
}

func resolveDatabaseConfig(appDir string) (*databaseConfig, error) {
	envFile := filepath.Join(appDir, ".env")
	fileValues := map[string]string{}
	if values, err := godotenv.Read(envFile); err == nil {
		fileValues = values
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", envFile, err)
	}

	fdPassword, fdPasswordProvided, err := passwordFromSecureInput()
	if err != nil {
		return nil, err
	}
	value := func(name string) string {
		if name == "PATCH_DB_PASSWORD" && fdPasswordProvided {
			return fdPassword
		}
		if current, ok := os.LookupEnv(name); ok {
			if name == "PATCH_DB_PASSWORD" {
				return current
			}
			return strings.TrimSpace(current)
		}
		if name == "PATCH_DB_PASSWORD" {
			return fileValues[name]
		}
		return strings.TrimSpace(fileValues[name])
	}
	override := func(patchName, appName string) string {
		if current := value(patchName); current != "" {
			return current
		}
		return value(appName)
	}

	kind := strings.ToLower(value("PATCH_DB_TYPE"))
	dsn := override("PATCH_SQL_DSN", "SQL_DSN")
	if kind == "" || kind == "auto" {
		switch {
		case dsn == "", strings.HasPrefix(strings.ToLower(dsn), "local"):
			kind = databaseSQLite
		case strings.HasPrefix(strings.ToLower(dsn), "postgres://"),
			strings.HasPrefix(strings.ToLower(dsn), "postgresql://"):
			kind = databasePostgres
		default:
			kind = databaseMySQL
		}
	}

	config := &databaseConfig{kind: kind, appDir: appDir}
	switch kind {
	case databaseSQLite:
		sqlitePath := override("PATCH_SQLITE_PATH", "SQLITE_PATH")
		if sqlitePath == "" {
			sqlitePath = "one-api.db"
		}
		if queryIndex := strings.IndexByte(sqlitePath, '?'); queryIndex >= 0 {
			sqlitePath = sqlitePath[:queryIndex]
		}
		if !filepath.IsAbs(sqlitePath) {
			sqlitePath = filepath.Join(appDir, sqlitePath)
		}
		config.sqlitePath = filepath.Clean(sqlitePath)
	case databaseMySQL:
		mysqlConfig, err := resolveMySQLConfig(value, dsn)
		if err != nil {
			return nil, err
		}
		config.mysql = mysqlConfig
	case databasePostgres:
		postgresConfig, sslMode, schema, err := resolvePostgresConfig(value, dsn)
		if err != nil {
			return nil, err
		}
		config.postgres = postgresConfig
		config.sslMode = sslMode
		config.schema = schema
	default:
		return nil, fmt.Errorf("unsupported database type %q", kind)
	}
	return config, nil
}

func passwordFromSecureInput() (string, bool, error) {
	rawFD := strings.TrimSpace(os.Getenv("PATCH_DB_PASSWORD_FD"))
	stdinMode := strings.TrimSpace(os.Getenv("PATCH_DB_PASSWORD_STDIN"))
	if rawFD != "" && stdinMode != "" {
		return "", false, errors.New(
			"PATCH_DB_PASSWORD_FD and PATCH_DB_PASSWORD_STDIN cannot be used together",
		)
	}
	if stdinMode != "" {
		if stdinMode != "1" {
			return "", false, errors.New("PATCH_DB_PASSWORD_STDIN must be 1")
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 64*1024))
		if err != nil {
			return "", false, fmt.Errorf("read database password from stdin: %w", err)
		}
		password := strings.TrimSuffix(string(data), "\n")
		password = strings.TrimSuffix(password, "\r")
		return password, true, nil
	}
	if rawFD == "" {
		return "", false, nil
	}
	fd, err := strconv.Atoi(rawFD)
	if err != nil || fd < 3 {
		return "", false, errors.New("PATCH_DB_PASSWORD_FD must be an inherited descriptor >= 3")
	}
	file := os.NewFile(uintptr(fd), "patch-db-password")
	if file == nil {
		return "", false, errors.New("PATCH_DB_PASSWORD_FD is not available")
	}
	data, err := io.ReadAll(io.LimitReader(file, 64*1024))
	if err != nil {
		return "", false, fmt.Errorf("read database password descriptor: %w", err)
	}
	password := string(data)
	password = strings.TrimSuffix(password, "\n")
	password = strings.TrimSuffix(password, "\r")
	return password, true, nil
}

func resolveMySQLConfig(value func(string) string, dsn string) (*mysqlDriver.Config, error) {
	if value("PATCH_DB_HOST") != "" || value("PATCH_DB_NAME") != "" {
		host := value("PATCH_DB_HOST")
		if host == "" {
			host = "127.0.0.1"
		}
		port := value("PATCH_DB_PORT")
		if port == "" {
			port = "3306"
		}
		if value("PATCH_DB_NAME") == "" || value("PATCH_DB_USER") == "" {
			return nil, errors.New("MySQL requires PATCH_DB_NAME and PATCH_DB_USER")
		}
		config := mysqlDriver.NewConfig()
		config.User = value("PATCH_DB_USER")
		config.Passwd = value("PATCH_DB_PASSWORD")
		config.Net = "tcp"
		config.Addr = net.JoinHostPort(host, port)
		config.DBName = value("PATCH_DB_NAME")
		config.ParseTime = true
		config.MultiStatements = true
		config.Params = map[string]string{"charset": "utf8mb4"}
		return config, nil
	}
	if dsn == "" {
		return nil, errors.New("MySQL SQL_DSN is empty")
	}
	config, err := mysqlDriver.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse MySQL SQL_DSN: %w", err)
	}
	config.ParseTime = true
	config.MultiStatements = true
	return config, nil
}

func resolvePostgresConfig(
	value func(string) string,
	dsn string,
) (*pgx.ConnConfig, string, string, error) {
	sslMode := value("PATCH_DB_SSLMODE")
	if sslMode == "" {
		sslMode = "prefer"
	}
	if value("PATCH_DB_HOST") != "" || value("PATCH_DB_NAME") != "" {
		host := value("PATCH_DB_HOST")
		if host == "" {
			host = "127.0.0.1"
		}
		port := value("PATCH_DB_PORT")
		if port == "" {
			port = "5432"
		}
		if value("PATCH_DB_NAME") == "" || value("PATCH_DB_USER") == "" {
			return nil, "", "", errors.New("PostgreSQL requires PATCH_DB_NAME and PATCH_DB_USER")
		}
		connectionURL := &url.URL{
			Scheme: "postgresql",
			Host:   net.JoinHostPort(host, port),
			Path:   "/" + value("PATCH_DB_NAME"),
			User:   url.UserPassword(value("PATCH_DB_USER"), value("PATCH_DB_PASSWORD")),
		}
		query := connectionURL.Query()
		query.Set("sslmode", sslMode)
		connectionURL.RawQuery = query.Encode()
		dsn = connectionURL.String()
	} else if dsn == "" {
		return nil, "", "", errors.New("PostgreSQL SQL_DSN is empty")
	} else if parsed, err := url.Parse(dsn); err == nil {
		if mode := parsed.Query().Get("sslmode"); mode != "" {
			sslMode = mode
		}
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, "", "", fmt.Errorf("parse PostgreSQL SQL_DSN: %w", err)
	}
	schema := strings.TrimSpace(value("PATCH_DB_SCHEMA"))
	if schema == "" {
		schema = strings.TrimSpace(config.RuntimeParams["search_path"])
	}
	if schema == "" {
		schema = "public"
	}
	if !postgresSchemaPattern.MatchString(schema) {
		return nil, "", "", fmt.Errorf(
			"PostgreSQL schema %q must match %s",
			schema,
			postgresSchemaPattern.String(),
		)
	}
	if config.RuntimeParams == nil {
		config.RuntimeParams = map[string]string{}
	}
	config.RuntimeParams["search_path"] = schema
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return config, sslMode, schema, nil
}

func openDatabase(config *databaseConfig) (*sql.DB, func(), error) {
	var (
		db  *sql.DB
		err error
	)
	switch config.kind {
	case databaseSQLite:
		info, statErr := os.Lstat(config.sqlitePath)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return nil, func() {}, fmt.Errorf(
					"SQLite database does not exist: %s",
					config.sqlitePath,
				)
			}
			return nil, func() {}, fmt.Errorf("inspect SQLite database: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, func() {}, fmt.Errorf(
				"SQLite database must be a regular non-symlink file: %s",
				config.sqlitePath,
			)
		}
		db, err = sql.Open("sqlite", config.sqlitePath)
	case databaseMySQL:
		db, err = sql.Open("mysql", config.mysql.FormatDSN())
	case databasePostgres:
		db, err = sql.Open("pgx", stdlib.RegisterConnConfig(config.postgres))
	default:
		err = fmt.Errorf("unsupported database type %q", config.kind)
	}
	if err != nil {
		return nil, func() {}, err
	}
	closeDB := func() {
		_ = db.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		closeDB()
		return nil, func() {}, fmt.Errorf("connect to %s database: %w", config.kind, err)
	}
	if config.kind == databaseSQLite {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 30000"); err != nil {
			closeDB()
			return nil, func() {}, fmt.Errorf("configure SQLite busy timeout: %w", err)
		}
	}
	return db, closeDB, nil
}

func identifyDatabase(config *databaseConfig) (string, error) {
	db, closeDB, err := openDatabase(config)
	if err != nil {
		return "", err
	}
	defer closeDB()

	switch config.kind {
	case databaseSQLite:
		var path string
		rows, err := db.Query("PRAGMA database_list")
		if err != nil {
			return "", fmt.Errorf("read SQLite database list: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sequenceID int
			var name, candidate string
			if err := rows.Scan(&sequenceID, &name, &candidate); err != nil {
				return "", err
			}
			if name == "main" {
				path = candidate
			}
		}
		if path == "" {
			path = config.sqlitePath
		}
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("sqlite|path=%s", filepath.Clean(absolutePath)), nil
	case databaseMySQL:
		var databaseName, hostname, version string
		var port int
		if err := db.QueryRow(
			"SELECT DATABASE(), @@hostname, @@port, VERSION()",
		).Scan(&databaseName, &hostname, &port, &version); err != nil {
			return "", fmt.Errorf("read MySQL identity: %w", err)
		}
		return fmt.Sprintf(
			"mysql|host=%s|port=%d|database=%s|user=%s|version=%s",
			hostname,
			port,
			databaseName,
			config.mysql.User,
			version,
		), nil
	case databasePostgres:
		var databaseName, schema, version string
		var address sql.NullString
		var port sql.NullInt64
		if err := db.QueryRow(`
			SELECT current_database(), current_schema(),
			       COALESCE(inet_server_addr()::text, ''), inet_server_port(), version()
		`).Scan(&databaseName, &schema, &address, &port, &version); err != nil {
			return "", fmt.Errorf("read PostgreSQL identity: %w", err)
		}
		if schema != config.schema {
			return "", fmt.Errorf(
				"PostgreSQL selected schema is %q, expected %q",
				schema,
				config.schema,
			)
		}
		return fmt.Sprintf(
			"postgres|host=%s|port=%d|database=%s|schema=%s|user=%s|version=%s",
			address.String,
			port.Int64,
			databaseName,
			schema,
			config.postgres.User,
			version,
		), nil
	default:
		return "", fmt.Errorf("unsupported database type %q", config.kind)
	}
}

func inspectSchema(db databaseExecutor, kind string) (string, string, error) {
	exists := make(map[string]bool, len(liandongTables))
	existingCount := 0
	for _, table := range liandongTables {
		present, err := tableExists(db, kind, table)
		if err != nil {
			return "", "", err
		}
		exists[table] = present
		if present {
			existingCount++
		}
	}
	if existingCount == 0 {
		return stateFresh, "", nil
	}
	if !exists["liandong_products"] || !exists["liandong_orders"] {
		return stateUnsafe, "liandong_products and liandong_orders must either both exist or both be absent", nil
	}

	productColumns, err := readColumns(db, kind, "liandong_products")
	if err != nil {
		return "", "", err
	}
	orderColumns, err := readColumns(db, kind, "liandong_orders")
	if err != nil {
		return "", "", err
	}
	if !containsColumns(productColumns, legacyProductColumns) ||
		!containsColumns(orderColumns, legacyOrderColumns) {
		return stateUnsafe, "legacy product or order columns are incomplete", nil
	}

	hasProductTargets := containsColumns(productColumns, targetProductColumns)
	hasOrderTargets := containsColumns(orderColumns, targetOrderColumns)
	newTablesPresent := exists["liandong_product_thumbnails"] &&
		exists["liandong_product_inventory_codes"] &&
		exists["liandong_user_operation_leases"]

	if !hasAnyColumn(productColumns, []string{
		"goods_type", "inventory_mode", "inventory_capacity", "thumbnail_version",
	}) && !hasAnyColumn(orderColumns, []string{
		"inventory_code_id", "expires_at", "closed_reason", "late_payment",
	}) && existingCount == 2 {
		if kind == databaseSQLite {
			if len(productColumns) != len(legacyProductColumns) ||
				len(orderColumns) != len(legacyOrderColumns) {
				return stateUnsafe, "SQLite legacy tables contain custom or unexpected columns", nil
			}
			if err := verifySQLiteLegacySchemaSafety(db); err != nil {
				return stateUnsafe, err.Error(), nil
			}
		}
		if err := verifyLegacyIndexes(db, kind); err != nil {
			return stateUnsafe, err.Error(), nil
		}
		return stateLegacy, "", nil
	}

	if hasProductTargets && hasOrderTargets && newTablesPresent {
		if err := verifyTargetSchema(db, kind); err == nil {
			return stateTarget, "", nil
		} else if kind == databaseSQLite {
			return stateUnsafe, err.Error(), nil
		}
	}
	if kind != databaseSQLite {
		compatible, err := compatiblePartialSchema(
			db,
			kind,
			exists,
			productColumns,
			orderColumns,
		)
		if err != nil {
			return "", "", err
		}
		if compatible {
			return stateCompatiblePartial, "a previous MySQL/PostgreSQL migration can be resumed", nil
		}
	}
	return stateUnsafe, "schema is neither the supported legacy state nor the target state", nil
}

func compatiblePartialSchema(
	db databaseExecutor,
	kind string,
	exists map[string]bool,
	productColumns map[string]columnInfo,
	orderColumns map[string]columnInfo,
) (bool, error) {
	if !containsColumns(productColumns, legacyProductColumns) ||
		!containsColumns(orderColumns, legacyOrderColumns) {
		return false, nil
	}
	if !onlyExpectedColumns(productColumns, targetProductColumns) ||
		!onlyExpectedColumns(orderColumns, targetOrderColumns) {
		return false, nil
	}
	if err := verifyPresentMigrationColumnTypes(kind, productColumns, true); err != nil {
		return false, nil
	}
	if err := verifyPresentMigrationColumnTypes(kind, orderColumns, false); err != nil {
		return false, nil
	}
	for table, required := range requiredNewTableColumns {
		if !exists[table] {
			continue
		}
		columns, err := readColumns(db, kind, table)
		if err != nil {
			return false, err
		}
		if !containsColumns(columns, required) || !onlyExpectedColumns(columns, required) {
			return false, nil
		}
		if err := verifyNewTableColumnContracts(kind, table, columns); err != nil {
			return false, nil
		}
	}
	if err := verifyPresentIndexDefinitions(db, kind, targetIndexes); err != nil {
		return false, nil
	}
	return true, nil
}

func verifyLegacyIndexes(db databaseExecutor, kind string) error {
	for table, expected := range legacyIndexes {
		actual, err := readIndexDefinitions(db, kind, table)
		if err != nil {
			return err
		}
		for name, definition := range expected {
			if err := compareIndexDefinition(table, name, definition, actual[name]); err != nil {
				return fmt.Errorf("legacy schema does not match the supported baseline: %w", err)
			}
		}
		if kind == databaseSQLite && len(actual) != len(expected) {
			return fmt.Errorf(
				"legacy schema does not match the supported baseline: %s contains unexpected indexes",
				table,
			)
		}
	}
	return nil
}

func verifySQLiteLegacySchemaSafety(db databaseExecutor) error {
	checks := []struct {
		query  string
		reason string
	}{
		{
			query: `
				SELECT COUNT(*)
				FROM sqlite_master
				WHERE type = 'trigger'
				  AND (
				    tbl_name IN ('liandong_products', 'liandong_orders')
				    OR lower(COALESCE(sql, '')) LIKE '%liandong_products%'
				    OR lower(COALESCE(sql, '')) LIKE '%liandong_orders%'
				  )
			`,
			reason: "SQLite legacy schema contains triggers that would not survive table reconstruction",
		},
		{
			query: `
				SELECT COUNT(*)
				FROM sqlite_master
				WHERE type = 'view'
				  AND (
				    lower(COALESCE(sql, '')) LIKE '%liandong_products%'
				    OR lower(COALESCE(sql, '')) LIKE '%liandong_orders%'
				  )
			`,
			reason: "SQLite legacy schema contains dependent views",
		},
		{
			query: `
				SELECT COUNT(*)
				FROM sqlite_master
				WHERE type = 'index'
				  AND tbl_name IN ('liandong_products', 'liandong_orders')
				  AND name LIKE 'sqlite_autoindex_%'
			`,
			reason: "SQLite legacy schema contains table-level UNIQUE constraints",
		},
		{
			query: `
				SELECT COUNT(*)
				FROM sqlite_master
				WHERE type = 'table'
				  AND name IN ('liandong_products', 'liandong_orders')
				  AND (
				    lower(COALESCE(sql, '')) LIKE '% check(%'
				    OR lower(COALESCE(sql, '')) LIKE '% check (%'
				    OR lower(COALESCE(sql, '')) LIKE '% generated %'
				  )
			`,
			reason: "SQLite legacy schema contains CHECK or generated-column definitions",
		},
		{
			query: `
				SELECT
				  (SELECT COUNT(*) FROM pragma_foreign_key_list('liandong_products')) +
				  (SELECT COUNT(*) FROM pragma_foreign_key_list('liandong_orders'))
			`,
			reason: "SQLite legacy schema contains foreign keys on Liandong tables",
		},
		{
			query: `
				SELECT COUNT(*)
				FROM sqlite_master AS schema_object
				JOIN pragma_foreign_key_list(schema_object.name) AS foreign_key
				WHERE schema_object.type = 'table'
				  AND foreign_key."table" IN ('liandong_products', 'liandong_orders')
			`,
			reason: "SQLite legacy schema is referenced by foreign keys",
		},
	}
	for _, check := range checks {
		var count int64
		if err := db.QueryRow(check.query).Scan(&count); err != nil {
			return fmt.Errorf("inspect SQLite legacy schema safety: %w", err)
		}
		if count != 0 {
			return errors.New(check.reason)
		}
	}
	return nil
}

func verifyTargetSchema(db databaseExecutor, kind string) error {
	requiredColumns := map[string][]string{
		"liandong_products": targetProductColumns,
		"liandong_orders":   targetOrderColumns,
	}
	for table, columns := range requiredNewTableColumns {
		requiredColumns[table] = columns
	}
	for table, required := range requiredColumns {
		present, err := tableExists(db, kind, table)
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("required table %s is missing", table)
		}
		columns, err := readColumns(db, kind, table)
		if err != nil {
			return err
		}
		if !containsColumns(columns, required) {
			return fmt.Errorf("required columns are missing from %s", table)
		}
		if _, ok := newTableColumnContracts[table]; ok {
			if err := verifyNewTableColumnContracts(kind, table, columns); err != nil {
				return err
			}
		}
	}

	productColumns, err := readColumns(db, kind, "liandong_products")
	if err != nil {
		return err
	}
	for _, name := range []string{
		"goods_type", "inventory_mode", "inventory_capacity", "thumbnail_version",
	} {
		if productColumns[name].nullable {
			return fmt.Errorf("liandong_products.%s must be NOT NULL", name)
		}
	}
	if err := verifyNewColumnTypes(kind, productColumns, true); err != nil {
		return err
	}
	orderColumns, err := readColumns(db, kind, "liandong_orders")
	if err != nil {
		return err
	}
	if err := verifyNewColumnTypes(kind, orderColumns, false); err != nil {
		return err
	}
	for _, name := range []string{
		"inventory_code_id", "expires_at", "closed_reason", "late_payment",
	} {
		if orderColumns[name].nullable {
			return fmt.Errorf("liandong_orders.%s must be NOT NULL", name)
		}
	}
	if err := verifyTargetPrimaryKeys(kind, requiredColumns, db); err != nil {
		return err
	}

	for table, expected := range targetIndexes {
		actual, err := readIndexDefinitions(db, kind, table)
		if err != nil {
			return err
		}
		for name, definition := range expected {
			if err := compareIndexDefinition(table, name, definition, actual[name]); err != nil {
				return err
			}
		}
		if kind == databaseSQLite && len(actual) != len(expected) {
			return fmt.Errorf("%s contains unexpected indexes", table)
		}
	}

	var nullCount int64
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM liandong_products
		WHERE goods_type IS NULL
		   OR inventory_mode IS NULL
		   OR inventory_capacity IS NULL
		   OR thumbnail_version IS NULL
	`).Scan(&nullCount); err != nil {
		return fmt.Errorf("check product migration values: %w", err)
	}
	if nullCount != 0 {
		return fmt.Errorf("liandong_products contains %d rows with NULL migration values", nullCount)
	}
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM liandong_orders
		WHERE inventory_code_id IS NULL
		   OR expires_at IS NULL
		   OR closed_reason IS NULL
		   OR late_payment IS NULL
	`).Scan(&nullCount); err != nil {
		return fmt.Errorf("check order migration values: %w", err)
	}
	if nullCount != 0 {
		return fmt.Errorf("liandong_orders contains %d rows with NULL migration values", nullCount)
	}

	if kind == databaseSQLite {
		var integrity string
		if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
			return fmt.Errorf("run SQLite integrity_check: %w", err)
		}
		if strings.ToLower(strings.TrimSpace(integrity)) != "ok" {
			return fmt.Errorf("SQLite integrity_check failed: %s", integrity)
		}
		rows, err := db.Query("PRAGMA foreign_key_check")
		if err != nil {
			return fmt.Errorf("run SQLite foreign_key_check: %w", err)
		}
		defer rows.Close()
		if rows.Next() {
			return errors.New("SQLite foreign_key_check reported violations")
		}
	}
	return nil
}

func verifyTargetPrimaryKeys(
	kind string,
	requiredColumns map[string][]string,
	db databaseExecutor,
) error {
	expected := map[string]struct {
		column        string
		autoIncrement bool
	}{
		"liandong_products":                {column: "id", autoIncrement: true},
		"liandong_orders":                  {column: "id", autoIncrement: true},
		"liandong_product_thumbnails":      {column: "product_id"},
		"liandong_product_inventory_codes": {column: "id", autoIncrement: true},
		"liandong_user_operation_leases":   {column: "user_id"},
	}
	for table := range requiredColumns {
		columns, err := readColumns(db, kind, table)
		if err != nil {
			return err
		}
		contract := expected[table]
		info, ok := columns[contract.column]
		if !ok || !info.primaryKey {
			return fmt.Errorf("%s.%s must be the primary key", table, contract.column)
		}
		if info.autoIncrement != contract.autoIncrement {
			requirement := "must not auto-increment"
			if contract.autoIncrement {
				requirement = "must auto-increment"
			}
			return fmt.Errorf("%s.%s %s", table, contract.column, requirement)
		}
	}
	return nil
}

func verifyNewColumnTypes(kind string, columns map[string]columnInfo, product bool) error {
	expected := map[string][]string{}
	if product {
		switch kind {
		case databaseSQLite:
			expected = map[string][]string{
				"goods_type":         {"varchar"},
				"inventory_mode":     {"varchar"},
				"inventory_capacity": {"integer"},
				"thumbnail_version":  {"bigint"},
			}
		case databaseMySQL:
			expected = map[string][]string{
				"goods_type":         {"varchar"},
				"inventory_mode":     {"varchar"},
				"inventory_capacity": {"bigint"},
				"thumbnail_version":  {"bigint"},
			}
		case databasePostgres:
			expected = map[string][]string{
				"goods_type":         {"character varying"},
				"inventory_mode":     {"character varying"},
				"inventory_capacity": {"bigint"},
				"thumbnail_version":  {"bigint"},
			}
		}
	} else {
		switch kind {
		case databaseSQLite:
			expected = map[string][]string{
				"inventory_code_id": {"integer"},
				"expires_at":        {"bigint"},
				"closed_reason":     {"varchar"},
				"late_payment":      {"numeric"},
			}
		case databaseMySQL:
			expected = map[string][]string{
				"inventory_code_id": {"bigint"},
				"expires_at":        {"bigint"},
				"closed_reason":     {"varchar"},
				"late_payment":      {"tinyint"},
			}
		case databasePostgres:
			expected = map[string][]string{
				"inventory_code_id": {"bigint"},
				"expires_at":        {"bigint"},
				"closed_reason":     {"character varying"},
				"late_payment":      {"boolean"},
			}
		}
	}
	if len(expected) == 0 {
		return fmt.Errorf("unsupported database type %q", kind)
	}
	for name, allowed := range expected {
		actual := strings.ToLower(columns[name].dataType)
		matched := false
		for _, candidate := range allowed {
			if actual == candidate {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s has unexpected type %s on %s", name, actual, kind)
		}
	}
	if product {
		if columns["goods_type"].length != 32 {
			return errors.New("goods_type must have length 32")
		}
		if columns["inventory_mode"].length != 32 {
			return errors.New("inventory_mode must have length 32")
		}
	} else if columns["closed_reason"].length != 64 {
		return errors.New("closed_reason must have length 64")
	}
	return nil
}

func verifyNewTableColumnContracts(
	kind string,
	table string,
	columns map[string]columnInfo,
) error {
	contracts, ok := newTableColumnContracts[table]
	if !ok {
		return fmt.Errorf("no column contract is defined for %s", table)
	}
	if len(columns) != len(contracts) {
		return fmt.Errorf("%s contains missing or unexpected columns", table)
	}
	for name, contract := range contracts {
		actual, ok := columns[name]
		if !ok {
			return fmt.Errorf("required column %s.%s is missing", table, name)
		}
		actualType := strings.ToLower(strings.TrimSpace(actual.dataType))
		typeMatched := false
		for _, allowed := range contract.dataTypes {
			if actualType == allowed {
				typeMatched = true
				break
			}
		}
		if !typeMatched {
			return fmt.Errorf(
				"%s.%s has unexpected type %s on %s",
				table,
				name,
				actualType,
				kind,
			)
		}
		if !typeAllowedForDialect(kind, actualType) {
			return fmt.Errorf(
				"%s.%s has type %s that is not valid for %s",
				table,
				name,
				actualType,
				kind,
			)
		}
		if contract.length > 0 &&
			actual.length != contract.length {
			return fmt.Errorf(
				"%s.%s must have length %d",
				table,
				name,
				contract.length,
			)
		}
		if actual.nullable != contract.nullable {
			requirement := "NOT NULL"
			if contract.nullable {
				requirement = "nullable"
			}
			return fmt.Errorf("%s.%s must be %s", table, name, requirement)
		}
		if actual.primaryKey != contract.primaryKey {
			requirement := "must not be a primary key"
			if contract.primaryKey {
				requirement = "must be the primary key"
			}
			return fmt.Errorf("%s.%s %s", table, name, requirement)
		}
		if actual.autoIncrement != contract.autoIncrement {
			requirement := "must not auto-increment"
			if contract.autoIncrement {
				requirement = "must auto-increment"
			}
			return fmt.Errorf("%s.%s %s", table, name, requirement)
		}
		if actual.hidden {
			return fmt.Errorf("%s.%s must not be a hidden/generated column", table, name)
		}
	}
	return nil
}

func typeAllowedForDialect(kind string, dataType string) bool {
	allowed := map[string]map[string]bool{
		databaseSQLite: {
			"integer": true,
			"bigint":  true,
			"varchar": true,
			"char":    true,
			"blob":    true,
		},
		databaseMySQL: {
			"bigint":   true,
			"varchar":  true,
			"char":     true,
			"longblob": true,
		},
		databasePostgres: {
			"bigint":            true,
			"character varying": true,
			"character":         true,
			"bytea":             true,
		},
	}
	return allowed[kind][dataType]
}

func verifyPresentMigrationColumnTypes(
	kind string,
	columns map[string]columnInfo,
	product bool,
) error {
	names := []string{
		"inventory_code_id",
		"expires_at",
		"closed_reason",
		"late_payment",
	}
	if product {
		names = []string{
			"goods_type",
			"inventory_mode",
			"inventory_capacity",
			"thumbnail_version",
		}
	}
	present := make(map[string]columnInfo, len(names))
	for _, name := range names {
		if info, ok := columns[name]; ok {
			present[name] = info
		}
	}
	if len(present) == 0 {
		return nil
	}
	for _, name := range names {
		if _, ok := present[name]; !ok {
			present[name] = migrationColumnFallbackInfo(kind, name)
		}
	}
	return verifyNewColumnTypes(kind, present, product)
}

func migrationColumnFallbackInfo(kind string, name string) columnInfo {
	if name == "goods_type" || name == "inventory_mode" {
		dataType := "varchar"
		if kind == databasePostgres {
			dataType = "character varying"
		}
		return columnInfo{dataType: dataType, length: 32}
	}
	if name == "closed_reason" {
		dataType := "varchar"
		if kind == databasePostgres {
			dataType = "character varying"
		}
		return columnInfo{dataType: dataType, length: 64}
	}
	if name == "late_payment" {
		switch kind {
		case databaseSQLite:
			return columnInfo{dataType: "numeric"}
		case databaseMySQL:
			return columnInfo{dataType: "tinyint"}
		default:
			return columnInfo{dataType: "boolean"}
		}
	}
	if kind == databaseSQLite && name == "inventory_code_id" {
		return columnInfo{dataType: "integer"}
	}
	return columnInfo{dataType: "bigint"}
}

func onlyExpectedColumns(columns map[string]columnInfo, expected []string) bool {
	if len(columns) > len(expected) {
		return false
	}
	allowed := make(map[string]bool, len(expected))
	for _, name := range expected {
		allowed[name] = true
	}
	for name := range columns {
		if !allowed[name] {
			return false
		}
	}
	return true
}

func verifyPresentIndexDefinitions(
	db databaseExecutor,
	kind string,
	expected map[string]map[string]indexDefinition,
) error {
	for table, definitions := range expected {
		actual, err := readIndexDefinitions(db, kind, table)
		if err != nil {
			return err
		}
		for name, definition := range definitions {
			if current, ok := actual[name]; ok {
				if err := compareIndexDefinition(table, name, definition, current); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func compareIndexDefinition(
	table string,
	name string,
	expected indexDefinition,
	actual indexDefinition,
) error {
	if len(actual.columns) == 0 {
		return fmt.Errorf("required index %s is missing from %s", name, table)
	}
	if expected.unique != actual.unique ||
		strings.Join(expected.columns, ",") != strings.Join(actual.columns, ",") ||
		!actual.valid ||
		actual.partial ||
		actual.expression ||
		actual.descending ||
		actual.nonDefaultCollation ||
		actual.prefix {
		return fmt.Errorf(
			"index %s on %s has an unexpected definition",
			name,
			table,
		)
	}
	return nil
}

func requireLiandongDisabled(db databaseExecutor, kind string) error {
	present, err := tableExists(db, kind, "options")
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	keys := []string{
		"LiandongEnabled",
		"LiandongCreateEnabled",
		"LiandongReconcileEnabled",
		"LiandongFulfillEnabled",
		"LiandongIframeEnabled",
	}
	placeholders := make([]string, len(keys))
	args := make([]any, len(keys))
	for index, key := range keys {
		if kind == databasePostgres {
			placeholders[index] = "$" + strconv.Itoa(index+1)
		} else {
			placeholders[index] = "?"
		}
		args[index] = key
	}
	query := fmt.Sprintf(
		"SELECT key, value FROM options WHERE key IN (%s)",
		strings.Join(placeholders, ","),
	)
	rows, err := db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("read Liandong switches: %w", err)
	}
	defer rows.Close()
	var enabled []string
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on":
			enabled = append(enabled, key)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(enabled) != 0 {
		sort.Strings(enabled)
		return fmt.Errorf("Liandong switches must be disabled before deployment: %s", strings.Join(enabled, ", "))
	}
	return nil
}

func migrateDatabase(db *sql.DB, kind string, ddlDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve migration connection: %w", err)
	}
	defer conn.Close()
	executor := connectionExecutor{conn: conn}
	releaseLock, err := acquireMigrationLock(ctx, conn, kind)
	if err != nil {
		return err
	}
	defer releaseLock()
	if err := requireLiandongDisabled(executor, kind); err != nil {
		return err
	}
	plan, err := prepareMigrationPlan(executor, kind, ddlDir)
	if err != nil {
		return err
	}
	if plan.state == stateTarget {
		if err := verifyTargetSchema(executor, kind); err != nil {
			return err
		}
		if plan.history != nil && plan.history.state == migrationStateSuccess {
			return nil
		}
		startedAt := time.Now().Unix()
		if plan.history != nil && plan.history.startedAt > 0 {
			startedAt = plan.history.startedAt
		}
		return writeMigrationHistory(
			executor,
			kind,
			plan.checksum,
			migrationStateSuccess,
			startedAt,
			time.Now().Unix(),
			"",
		)
	}

	startedAt := time.Now().Unix()
	if plan.history != nil && plan.history.startedAt > 0 {
		startedAt = plan.history.startedAt
	}
	if err := writeMigrationHistory(
		executor,
		kind,
		plan.checksum,
		migrationStateDirty,
		startedAt,
		0,
		"",
	); err != nil {
		return err
	}

	if kind == databasePostgres {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("start PostgreSQL migration transaction: %w", err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(
			ctx,
			"SET LOCAL lock_timeout = '30s'; SET LOCAL statement_timeout = '20min'",
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("configure PostgreSQL migration timeouts: %w", err)
		}
		if err := executeMigrationPlan(ctx, tx, kind, plan); err != nil {
			_ = tx.Rollback()
			_ = writeMigrationHistory(
				executor,
				kind,
				plan.checksum,
				migrationStateDirty,
				startedAt,
				0,
				err.Error(),
			)
			return err
		}
		if err := writeMigrationHistory(
			tx,
			kind,
			plan.checksum,
			migrationStateSuccess,
			startedAt,
			time.Now().Unix(),
			"",
		); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit PostgreSQL migration: %w", err)
		}
		return nil
	}
	if kind == databaseMySQL {
		if _, err := conn.ExecContext(
			ctx,
			"SET SESSION lock_wait_timeout = 30, innodb_lock_wait_timeout = 30",
		); err != nil {
			return fmt.Errorf("configure MySQL migration timeouts: %w", err)
		}
	}
	if err := executeMigrationPlan(ctx, executor, kind, plan); err != nil {
		if kind == databaseSQLite {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
		_ = writeMigrationHistory(
			executor,
			kind,
			plan.checksum,
			migrationStateDirty,
			startedAt,
			0,
			err.Error(),
		)
		return err
	}
	return writeMigrationHistory(
		executor,
		kind,
		plan.checksum,
		migrationStateSuccess,
		startedAt,
		time.Now().Unix(),
		"",
	)
}

func prepareMigrationPlan(
	db databaseExecutor,
	kind string,
	ddlDir string,
) (*migrationPlan, error) {
	checksum, err := migrationBundleChecksum(kind, ddlDir)
	if err != nil {
		return nil, err
	}
	state, reason, err := inspectSchema(db, kind)
	if err != nil {
		return nil, err
	}
	if state == stateUnsafe {
		return nil, fmt.Errorf("refusing to migrate unsafe schema: %s", reason)
	}
	if state == stateCompatiblePartial && kind == databaseSQLite {
		return nil, errors.New("SQLite partial migration cannot be resumed")
	}
	if err := ensureMigrationHistoryTable(db, kind); err != nil {
		return nil, err
	}
	history, err := readMigrationHistory(db, kind)
	if err != nil {
		return nil, err
	}
	if err := validateMigrationHistory(history, checksum, state, kind); err != nil {
		return nil, err
	}
	plan := &migrationPlan{
		state:    state,
		checksum: checksum,
		history:  history,
	}
	if state == stateTarget {
		return plan, nil
	}

	var filename string
	switch kind {
	case databaseSQLite:
		if state == stateFresh {
			filename = "liandong-payment.sqlite-fresh.sql"
		} else if state == stateLegacy {
			filename = "liandong-payment.sqlite.sql"
		}
	case databaseMySQL:
		filename = "liandong-payment.mysql.sql"
	case databasePostgres:
		filename = "liandong-payment.postgresql.sql"
	}
	if filename == "" {
		return nil, fmt.Errorf("no DDL is available for %s state %s", kind, state)
	}
	data, err := os.ReadFile(filepath.Join(ddlDir, filename))
	if err != nil {
		return nil, fmt.Errorf("read DDL %s: %w", filename, err)
	}
	plan.filename = filename
	plan.ddl = data
	return plan, nil
}

func executeMigrationPlan(
	ctx context.Context,
	db databaseExecutor,
	kind string,
	plan *migrationPlan,
) error {
	if _, err := db.ExecContext(ctx, string(plan.ddl)); err != nil {
		return fmt.Errorf("execute DDL %s: %w", plan.filename, err)
	}
	if err := verifyTargetSchema(db, kind); err != nil {
		return fmt.Errorf("post-migration verification failed: %w", err)
	}
	return nil
}

func migrationBundleChecksum(kind string, ddlDir string) (string, error) {
	var filenames []string
	switch kind {
	case databaseSQLite:
		filenames = []string{
			"liandong-payment.sqlite-fresh.sql",
			"liandong-payment.sqlite.sql",
		}
	case databaseMySQL:
		filenames = []string{"liandong-payment.mysql.sql"}
	case databasePostgres:
		filenames = []string{"liandong-payment.postgresql.sql"}
	default:
		return "", fmt.Errorf("unsupported database type %q", kind)
	}
	hasher := sha256.New()
	for _, filename := range filenames {
		data, err := os.ReadFile(filepath.Join(ddlDir, filename))
		if err != nil {
			return "", fmt.Errorf("read DDL %s: %w", filename, err)
		}
		_, _ = io.WriteString(hasher, filename)
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(data)
		_, _ = hasher.Write([]byte{0})
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func ensureMigrationHistoryTable(db databaseExecutor, kind string) error {
	var statement string
	switch kind {
	case databaseSQLite:
		statement = `
			CREATE TABLE IF NOT EXISTS new_api_patch_history (
			  patch_id varchar(64) PRIMARY KEY,
			  ddl_checksum char(64) NOT NULL,
			  state varchar(16) NOT NULL,
			  database_type varchar(16) NOT NULL,
			  started_at bigint NOT NULL,
			  finished_at bigint NOT NULL,
			  error_message varchar(512) NOT NULL
			)
		`
	case databaseMySQL:
		statement = `
			CREATE TABLE IF NOT EXISTS new_api_patch_history (
			  patch_id varchar(64) NOT NULL,
			  ddl_checksum char(64) NOT NULL,
			  state varchar(16) NOT NULL,
			  database_type varchar(16) NOT NULL,
			  started_at bigint NOT NULL,
			  finished_at bigint NOT NULL,
			  error_message varchar(512) NOT NULL,
			  PRIMARY KEY (patch_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
		`
	case databasePostgres:
		statement = `
			CREATE TABLE IF NOT EXISTS new_api_patch_history (
			  patch_id varchar(64) PRIMARY KEY,
			  ddl_checksum char(64) NOT NULL,
			  state varchar(16) NOT NULL,
			  database_type varchar(16) NOT NULL,
			  started_at bigint NOT NULL,
			  finished_at bigint NOT NULL,
			  error_message varchar(512) NOT NULL
			)
		`
	default:
		return fmt.Errorf("unsupported database type %q", kind)
	}
	if _, err := db.Exec(statement); err != nil {
		return fmt.Errorf("create %s: %w", migrationHistoryTable, err)
	}
	return nil
}

func readMigrationHistory(
	db databaseExecutor,
	kind string,
) (*migrationHistory, error) {
	present, err := tableExists(db, kind, migrationHistoryTable)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	placeholder := "?"
	if kind == databasePostgres {
		placeholder = "$1"
	}
	row := db.QueryRow(fmt.Sprintf(`
		SELECT patch_id, ddl_checksum, state, database_type,
		       started_at, finished_at, error_message
		FROM new_api_patch_history
		WHERE patch_id = %s
	`, placeholder), patchID)
	history := &migrationHistory{}
	if err := row.Scan(
		&history.patchID,
		&history.checksum,
		&history.state,
		&history.databaseType,
		&history.startedAt,
		&history.finishedAt,
		&history.errorMessage,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", migrationHistoryTable, err)
	}
	return history, nil
}

func writeMigrationHistory(
	db databaseExecutor,
	kind string,
	checksum string,
	state string,
	startedAt int64,
	finishedAt int64,
	errorMessage string,
) error {
	if len(errorMessage) > 512 {
		errorMessage = errorMessage[:512]
	}
	args := []any{
		patchID,
		checksum,
		state,
		kind,
		startedAt,
		finishedAt,
		errorMessage,
	}
	var statement string
	switch kind {
	case databaseSQLite:
		statement = `
			INSERT INTO new_api_patch_history (
			  patch_id, ddl_checksum, state, database_type,
			  started_at, finished_at, error_message
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(patch_id) DO UPDATE SET
			  ddl_checksum = excluded.ddl_checksum,
			  state = excluded.state,
			  database_type = excluded.database_type,
			  started_at = excluded.started_at,
			  finished_at = excluded.finished_at,
			  error_message = excluded.error_message
		`
	case databaseMySQL:
		statement = `
			INSERT INTO new_api_patch_history (
			  patch_id, ddl_checksum, state, database_type,
			  started_at, finished_at, error_message
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
			  ddl_checksum = VALUES(ddl_checksum),
			  state = VALUES(state),
			  database_type = VALUES(database_type),
			  started_at = VALUES(started_at),
			  finished_at = VALUES(finished_at),
			  error_message = VALUES(error_message)
		`
	case databasePostgres:
		statement = `
			INSERT INTO new_api_patch_history (
			  patch_id, ddl_checksum, state, database_type,
			  started_at, finished_at, error_message
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (patch_id) DO UPDATE SET
			  ddl_checksum = EXCLUDED.ddl_checksum,
			  state = EXCLUDED.state,
			  database_type = EXCLUDED.database_type,
			  started_at = EXCLUDED.started_at,
			  finished_at = EXCLUDED.finished_at,
			  error_message = EXCLUDED.error_message
		`
	default:
		return fmt.Errorf("unsupported database type %q", kind)
	}
	if _, err := db.Exec(statement, args...); err != nil {
		return fmt.Errorf("write %s: %w", migrationHistoryTable, err)
	}
	return nil
}

func validateMigrationHistory(
	history *migrationHistory,
	checksum string,
	schemaState string,
	kind string,
) error {
	if history == nil {
		return nil
	}
	if history.databaseType != kind {
		return fmt.Errorf(
			"%s database type is %q, expected %q",
			migrationHistoryTable,
			history.databaseType,
			kind,
		)
	}
	if history.checksum != checksum {
		return fmt.Errorf(
			"%s checksum mismatch for %s",
			migrationHistoryTable,
			patchID,
		)
	}
	switch history.state {
	case migrationStateDirty:
		return nil
	case migrationStateSuccess:
		if schemaState != stateTarget {
			return fmt.Errorf(
				"%s records success but schema state is %s",
				migrationHistoryTable,
				schemaState,
			)
		}
		return nil
	default:
		return fmt.Errorf(
			"%s contains unsupported state %q",
			migrationHistoryTable,
			history.state,
		)
	}
}

func inspectMigrationHistory(
	db databaseExecutor,
	kind string,
	ddlDir string,
	schemaState string,
) (*migrationHistory, string, error) {
	checksum, err := migrationBundleChecksum(kind, ddlDir)
	if err != nil {
		return nil, "", err
	}
	history, err := readMigrationHistory(db, kind)
	if err != nil {
		return nil, "", err
	}
	if err := validateMigrationHistory(history, checksum, schemaState, kind); err != nil {
		return nil, "", err
	}
	return history, checksum, nil
}

func requireSuccessfulMigrationHistory(
	db databaseExecutor,
	kind string,
	ddlDir string,
) error {
	history, _, err := inspectMigrationHistory(
		db,
		kind,
		ddlDir,
		stateTarget,
	)
	if err != nil {
		return err
	}
	if history == nil {
		return fmt.Errorf("%s has no record for %s", migrationHistoryTable, patchID)
	}
	if history.state != migrationStateSuccess {
		return fmt.Errorf(
			"%s state is %s, expected %s",
			migrationHistoryTable,
			history.state,
			migrationStateSuccess,
		)
	}
	return nil
}

func backupDatabase(config *databaseConfig, output string) error {
	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absoluteOutput), 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if _, err := os.Stat(absoluteOutput); err == nil {
		return fmt.Errorf("backup output already exists: %s", absoluteOutput)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if config.kind != databaseSQLite {
		db, closeDB, err := openDatabase(config)
		if err != nil {
			return err
		}
		defer closeDB()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		conn, err := db.Conn(ctx)
		if err != nil {
			return fmt.Errorf("reserve backup connection: %w", err)
		}
		defer conn.Close()
		executor := connectionExecutor{conn: conn}
		releaseLock, err := acquireMigrationLock(ctx, conn, config.kind)
		if err != nil {
			return err
		}
		defer releaseLock()
		if config.kind == databaseMySQL {
			if err := verifyMySQLBackupEngines(executor); err != nil {
				return err
			}
		}
	}
	switch config.kind {
	case databaseSQLite:
		return backupSQLite(config, absoluteOutput)
	case databaseMySQL:
		return backupMySQL(config, absoluteOutput)
	case databasePostgres:
		return backupPostgres(config, absoluteOutput)
	default:
		return fmt.Errorf("unsupported database type %q", config.kind)
	}
}

func acquireMigrationLock(
	ctx context.Context,
	conn *sql.Conn,
	kind string,
) (func(), error) {
	switch kind {
	case databaseSQLite:
		return func() {}, nil
	case databaseMySQL:
		var acquired sql.NullInt64
		if err := conn.QueryRowContext(
			ctx,
			"SELECT GET_LOCK(?, 30)",
			migrationLockName,
		).Scan(&acquired); err != nil {
			return nil, fmt.Errorf("acquire MySQL migration lock: %w", err)
		}
		if !acquired.Valid || acquired.Int64 != 1 {
			return nil, errors.New("timed out acquiring MySQL migration lock")
		}
		return func() {
			var released sql.NullInt64
			_ = conn.QueryRowContext(
				context.Background(),
				"SELECT RELEASE_LOCK(?)",
				migrationLockName,
			).Scan(&released)
		}, nil
	case databasePostgres:
		deadline := time.Now().Add(30 * time.Second)
		for {
			var acquired bool
			if err := conn.QueryRowContext(
				ctx,
				"SELECT pg_try_advisory_lock($1)",
				migrationLockKey,
			).Scan(&acquired); err != nil {
				return nil, fmt.Errorf("acquire PostgreSQL migration lock: %w", err)
			}
			if acquired {
				return func() {
					var released bool
					_ = conn.QueryRowContext(
						context.Background(),
						"SELECT pg_advisory_unlock($1)",
						migrationLockKey,
					).Scan(&released)
				}, nil
			}
			if time.Now().After(deadline) {
				return nil, errors.New("timed out acquiring PostgreSQL migration lock")
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	default:
		return nil, fmt.Errorf("unsupported database type %q", kind)
	}
}

func verifyMySQLBackupEngines(db databaseExecutor) error {
	rows, err := db.Query(`
		SELECT table_name, COALESCE(engine, '')
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		  AND table_type = 'BASE TABLE'
		  AND COALESCE(engine, '') <> 'InnoDB'
		ORDER BY table_name
	`)
	if err != nil {
		return fmt.Errorf("inspect MySQL table engines: %w", err)
	}
	defer rows.Close()
	var unsupported []string
	for rows.Next() {
		var table, engine string
		if err := rows.Scan(&table, &engine); err != nil {
			return err
		}
		unsupported = append(unsupported, table+"("+engine+")")
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(unsupported) != 0 {
		return fmt.Errorf(
			"mysqldump --single-transaction requires InnoDB tables; unsupported: %s",
			strings.Join(unsupported, ", "),
		)
	}
	return nil
}

func backupSQLite(config *databaseConfig, output string) error {
	db, closeDB, err := openDatabase(config)
	if err != nil {
		return err
	}
	escapedOutput := strings.ReplaceAll(output, "'", "''")
	if _, err := db.Exec("VACUUM INTO '" + escapedOutput + "'"); err != nil {
		closeDB()
		_ = os.Remove(output)
		return fmt.Errorf("create SQLite backup: %w", err)
	}
	closeDB()
	if err := os.Chmod(output, 0o600); err != nil {
		return err
	}
	backupDB, err := sql.Open("sqlite", output)
	if err != nil {
		return err
	}
	defer backupDB.Close()
	var integrity string
	if err := backupDB.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("verify SQLite backup: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(integrity)) != "ok" {
		return fmt.Errorf("SQLite backup integrity_check failed: %s", integrity)
	}
	return nil
}

func backupMySQL(config *databaseConfig, output string) error {
	binary, err := exec.LookPath("mysqldump")
	if err != nil {
		return errors.New("mysqldump is required for MySQL backup")
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	defaultsFile, cleanupDefaults, err := createMySQLDefaultsFile(
		filepath.Dir(output),
		config.mysql.Passwd,
	)
	if err != nil {
		_ = os.Remove(output)
		return err
	}
	defer cleanupDefaults()
	args := []string{
		"--defaults-extra-file=" + defaultsFile,
		"--single-transaction",
		"--quick",
		"--routines",
		"--triggers",
		"--events",
		"--hex-blob",
		"--set-gtid-purged=OFF",
		"--default-character-set=utf8mb4",
		"--user=" + config.mysql.User,
	}
	switch config.mysql.Net {
	case "unix":
		args = append(args, "--socket="+config.mysql.Addr)
	default:
		host, port, splitErr := net.SplitHostPort(config.mysql.Addr)
		if splitErr != nil {
			host = config.mysql.Addr
			port = "3306"
		}
		args = append(args, "--protocol=tcp", "--host="+host, "--port="+port)
	}
	args = append(args, config.mysql.DBName)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = commandEnvironmentWithoutDatabaseSecrets()
	command.Stdout = file
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		_ = file.Close()
		_ = os.Remove(output)
		return fmt.Errorf("mysqldump failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := file.Sync(); err != nil {
		_ = os.Remove(output)
		return fmt.Errorf("sync MySQL backup: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		_ = os.Remove(output)
		return errors.New("mysqldump produced an empty backup")
	}
	return nil
}

func backupPostgres(config *databaseConfig, output string) error {
	pgDump, err := exec.LookPath("pg_dump")
	if err != nil {
		return errors.New("pg_dump is required for PostgreSQL backup")
	}
	pgRestore, err := exec.LookPath("pg_restore")
	if err != nil {
		return errors.New("pg_restore is required to verify PostgreSQL backup")
	}
	passwordFile, cleanupPassword, err := createPostgresPasswordFile(
		filepath.Dir(output),
		config,
	)
	if err != nil {
		return err
	}
	defer cleanupPassword()
	args := []string{
		"--format=custom",
		"--no-owner",
		"--no-acl",
		"--lock-wait-timeout=30s",
		"--schema=" + config.schema,
		"--file=" + output,
		"--host=" + config.postgres.Host,
		"--port=" + strconv.Itoa(int(config.postgres.Port)),
		"--username=" + config.postgres.User,
		config.postgres.Database,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, pgDump, args...)
	command.Env = append(
		commandEnvironmentWithoutDatabaseSecrets(),
		"PGPASSFILE="+passwordFile,
		"PGSSLMODE="+config.sslMode,
	)
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		_ = os.Remove(output)
		return fmt.Errorf("pg_dump failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := os.Chmod(output, 0o600); err != nil {
		return err
	}
	verifyContext, cancelVerify := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelVerify()
	verify := exec.CommandContext(verifyContext, pgRestore, "--list", output)
	verify.Env = append(
		commandEnvironmentWithoutDatabaseSecrets(),
		"PGSSLMODE="+config.sslMode,
	)
	if outputBytes, err := verify.CombinedOutput(); err != nil {
		_ = os.Remove(output)
		return fmt.Errorf("pg_restore verification failed: %w: %s", err, strings.TrimSpace(string(outputBytes)))
	}
	return nil
}

func createMySQLDefaultsFile(directory string, password string) (string, func(), error) {
	file, err := os.CreateTemp(directory, ".patchdb-mysql-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create MySQL credential file: %w", err)
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("secure MySQL credential file: %w", err)
	}
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
	).Replace(password)
	if _, err := fmt.Fprintf(file, "[client]\npassword=\"%s\"\n", escaped); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("write MySQL credential file: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("sync MySQL credential file: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close MySQL credential file: %w", err)
	}
	return file.Name(), cleanup, nil
}

func createPostgresPasswordFile(
	directory string,
	config *databaseConfig,
) (string, func(), error) {
	file, err := os.CreateTemp(directory, ".patchdb-pgpass-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create PostgreSQL password file: %w", err)
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("secure PostgreSQL password file: %w", err)
	}
	escape := func(value string) string {
		return strings.NewReplacer(`\`, `\\`, `:`, `\:`).Replace(value)
	}
	if _, err := fmt.Fprintf(
		file,
		"%s:%d:%s:%s:%s\n",
		escape(config.postgres.Host),
		config.postgres.Port,
		escape(config.postgres.Database),
		escape(config.postgres.User),
		escape(config.postgres.Password),
	); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("write PostgreSQL password file: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("sync PostgreSQL password file: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close PostgreSQL password file: %w", err)
	}
	return file.Name(), cleanup, nil
}

func commandEnvironmentWithoutDatabaseSecrets() []string {
	sensitive := map[string]struct{}{
		"PATCH_DB_PASSWORD": {},
		"PATCH_SQL_DSN":     {},
		"SQL_DSN":           {},
		"MYSQL_PWD":         {},
		"PGPASSWORD":        {},
	}
	environment := os.Environ()
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found {
			if _, excluded := sensitive[name]; excluded {
				continue
			}
		}
		result = append(result, entry)
	}
	return result
}

func tableExists(db databaseExecutor, kind string, table string) (bool, error) {
	var count int
	var err error
	switch kind {
	case databaseSQLite:
		err = db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&count)
	case databaseMySQL:
		err = db.QueryRow(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?",
			table,
		).Scan(&count)
	case databasePostgres:
		err = db.QueryRow(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1",
			table,
		).Scan(&count)
	default:
		return false, fmt.Errorf("unsupported database type %q", kind)
	}
	if err != nil {
		return false, fmt.Errorf("check table %s: %w", table, err)
	}
	return count > 0, nil
}

func readColumns(db databaseExecutor, kind string, table string) (map[string]columnInfo, error) {
	var (
		rows                *sql.Rows
		err                 error
		sqliteAutoIncrement bool
	)
	switch kind {
	case databaseSQLite:
		var createSQL sql.NullString
		if err := db.QueryRow(
			"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&createSQL); err != nil {
			return nil, fmt.Errorf("read SQLite table definition for %s: %w", table, err)
		}
		sqliteAutoIncrement = strings.Contains(
			strings.ToLower(createSQL.String),
			"autoincrement",
		)
		rows, err = db.Query("PRAGMA table_xinfo(" + quoteSQLiteIdentifier(table) + ")")
	case databaseMySQL:
		rows, err = db.Query(`
			SELECT
			  column_name,
			  data_type,
			  COALESCE(character_maximum_length, 0),
			  is_nullable,
			  COALESCE(column_default, ''),
			  column_key,
			  extra
			FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ?
		`, table)
	case databasePostgres:
		rows, err = db.Query(`
			SELECT
			  columns.column_name,
			  columns.data_type,
			  COALESCE(columns.character_maximum_length, 0),
			  columns.is_nullable,
			  COALESCE(columns.column_default, ''),
			  EXISTS (
			    SELECT 1
			    FROM pg_class AS table_class
			    JOIN pg_namespace AS table_namespace
			      ON table_namespace.oid = table_class.relnamespace
			    JOIN pg_index AS primary_index
			      ON primary_index.indrelid = table_class.oid
			     AND primary_index.indisprimary
			    JOIN unnest(primary_index.indkey) AS primary_column(attnum)
			      ON TRUE
			    JOIN pg_attribute AS table_column
			      ON table_column.attrelid = table_class.oid
			     AND table_column.attnum = primary_column.attnum
			    WHERE table_namespace.nspname = current_schema()
			      AND table_class.relname = $1
			      AND table_column.attname = columns.column_name
			  )
			FROM information_schema.columns AS columns
			WHERE columns.table_schema = current_schema()
			  AND columns.table_name = $1
		`, table)
	default:
		return nil, fmt.Errorf("unsupported database type %q", kind)
	}
	if err != nil {
		return nil, fmt.Errorf("read columns for %s: %w", table, err)
	}
	defer rows.Close()
	result := map[string]columnInfo{}
	for rows.Next() {
		if kind == databaseSQLite {
			var (
				cid        int
				name       string
				dataType   string
				notNull    int
				defaultV   sql.NullString
				primaryKey int
				hidden     int
			)
			if err := rows.Scan(
				&cid,
				&name,
				&dataType,
				&notNull,
				&defaultV,
				&primaryKey,
				&hidden,
			); err != nil {
				return nil, err
			}
			normalizedType, length := normalizeDeclaredType(dataType)
			result[strings.ToLower(name)] = columnInfo{
				dataType:      normalizedType,
				length:        length,
				nullable:      notNull == 0 && primaryKey == 0,
				defaultValue:  defaultV.String,
				primaryKey:    primaryKey != 0,
				autoIncrement: primaryKey != 0 && sqliteAutoIncrement,
				hidden:        hidden != 0,
			}
			continue
		}
		var name, dataType, nullable, defaultValue string
		var length int64
		info := columnInfo{}
		if kind == databaseMySQL {
			var columnKey, extra string
			if err := rows.Scan(
				&name,
				&dataType,
				&length,
				&nullable,
				&defaultValue,
				&columnKey,
				&extra,
			); err != nil {
				return nil, err
			}
			info.primaryKey = strings.EqualFold(columnKey, "PRI")
			info.autoIncrement = strings.Contains(strings.ToLower(extra), "auto_increment")
		} else {
			var primaryKey bool
			if err := rows.Scan(
				&name,
				&dataType,
				&length,
				&nullable,
				&defaultValue,
				&primaryKey,
			); err != nil {
				return nil, err
			}
			info.primaryKey = primaryKey
			info.autoIncrement = strings.HasPrefix(
				strings.ToLower(strings.TrimSpace(defaultValue)),
				"nextval(",
			)
		}
		info.dataType = strings.ToLower(strings.TrimSpace(dataType))
		info.length = length
		info.nullable = strings.EqualFold(nullable, "YES")
		info.defaultValue = defaultValue
		result[strings.ToLower(name)] = info
	}
	return result, rows.Err()
}

func normalizeDeclaredType(value string) (string, int64) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	open := strings.LastIndexByte(normalized, '(')
	closeIndex := strings.LastIndexByte(normalized, ')')
	if open < 0 || closeIndex <= open+1 {
		return normalized, 0
	}
	length, err := strconv.ParseInt(normalized[open+1:closeIndex], 10, 64)
	if err != nil {
		return normalized, 0
	}
	return normalized[:open], length
}

func readIndexes(db databaseExecutor, kind string, table string) ([]string, error) {
	definitions, err := readIndexDefinitions(db, kind, table)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(definitions))
	for name := range definitions {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func readIndexDefinitions(
	db databaseExecutor,
	kind string,
	table string,
) (map[string]indexDefinition, error) {
	result := map[string]indexDefinition{}
	switch kind {
	case databaseSQLite:
		rows, err := db.Query("PRAGMA index_list(" + quoteSQLiteIdentifier(table) + ")")
		if err != nil {
			return nil, fmt.Errorf("read indexes for %s: %w", table, err)
		}
		type sqliteIndex struct {
			name    string
			unique  bool
			partial bool
		}
		var indexes []sqliteIndex
		for rows.Next() {
			var (
				sequence int
				name     string
				unique   int
				origin   string
				partial  int
			)
			if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
				return nil, err
			}
			indexes = append(indexes, sqliteIndex{
				name:    name,
				unique:  unique != 0,
				partial: partial != 0,
			})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		for _, index := range indexes {
			columnRows, err := db.Query(
				"PRAGMA index_xinfo(" + quoteSQLiteIdentifier(index.name) + ")",
			)
			if err != nil {
				return nil, fmt.Errorf("read index %s: %w", index.name, err)
			}
			var columns []string
			for columnRows.Next() {
				var (
					columnSequence int
					columnID       int
					columnName     sql.NullString
					descending     int
					collation      sql.NullString
					keyColumn      int
				)
				if err := columnRows.Scan(
					&columnSequence,
					&columnID,
					&columnName,
					&descending,
					&collation,
					&keyColumn,
				); err != nil {
					_ = columnRows.Close()
					return nil, err
				}
				if keyColumn == 0 {
					continue
				}
				definition := result[index.name]
				if columnID == -2 || !columnName.Valid {
					definition.expression = true
				} else {
					columns = append(columns, strings.ToLower(columnName.String))
				}
				definition.descending = definition.descending || descending != 0
				definition.nonDefaultCollation = definition.nonDefaultCollation ||
					(collation.Valid && !strings.EqualFold(collation.String, "BINARY"))
				result[index.name] = definition
			}
			if err := columnRows.Err(); err != nil {
				_ = columnRows.Close()
				return nil, err
			}
			_ = columnRows.Close()
			definition := result[index.name]
			definition.columns = columns
			definition.unique = index.unique
			definition.partial = index.partial
			definition.valid = true
			result[index.name] = definition
		}
		return result, nil
	case databaseMySQL:
		rows, err := db.Query(`
			SELECT
			  index_name,
			  non_unique,
			  seq_in_index,
			  column_name,
			  sub_part,
			  collation,
			  index_type
			FROM information_schema.statistics
			WHERE table_schema = DATABASE()
			  AND table_name = ?
			  AND index_name <> 'PRIMARY'
			ORDER BY index_name, seq_in_index
		`, table)
		if err != nil {
			return nil, fmt.Errorf("read indexes for %s: %w", table, err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				name       string
				nonUnique  int
				sequence   int
				columnName sql.NullString
				subPart    sql.NullInt64
				collation  sql.NullString
				indexType  string
			)
			if err := rows.Scan(
				&name,
				&nonUnique,
				&sequence,
				&columnName,
				&subPart,
				&collation,
				&indexType,
			); err != nil {
				return nil, err
			}
			definition := result[name]
			definition.unique = nonUnique == 0
			definition.prefix = definition.prefix || subPart.Valid
			definition.descending = definition.descending ||
				(collation.Valid && strings.EqualFold(collation.String, "D"))
			definition.valid = strings.EqualFold(indexType, "BTREE")
			if columnName.Valid {
				definition.columns = append(
					definition.columns,
					strings.ToLower(columnName.String),
				)
			}
			result[name] = definition
		}
		return result, rows.Err()
	case databasePostgres:
		rows, err := db.Query(`
			SELECT
			  index_class.relname,
			  index_meta.indisunique,
			  index_meta.indisvalid,
			  index_meta.indisready,
			  index_meta.indislive,
			  index_meta.indpred IS NOT NULL,
			  index_meta.indexprs IS NOT NULL,
			  access_method.amname,
			  index_column.ordinality,
			  table_column.attname
			FROM pg_class AS table_class
			JOIN pg_namespace AS table_namespace
			  ON table_namespace.oid = table_class.relnamespace
			JOIN pg_index AS index_meta
			  ON index_meta.indrelid = table_class.oid
			JOIN pg_class AS index_class
			  ON index_class.oid = index_meta.indexrelid
			JOIN pg_am AS access_method
			  ON access_method.oid = index_class.relam
			JOIN unnest(index_meta.indkey) WITH ORDINALITY
			  AS index_column(attnum, ordinality)
			  ON TRUE
			LEFT JOIN pg_attribute AS table_column
			  ON table_column.attrelid = table_class.oid
			 AND table_column.attnum = index_column.attnum
			WHERE table_namespace.nspname = current_schema()
			  AND table_class.relname = $1
			  AND NOT index_meta.indisprimary
			ORDER BY index_class.relname, index_column.ordinality
		`, table)
		if err != nil {
			return nil, fmt.Errorf("read indexes for %s: %w", table, err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				name       string
				unique     bool
				valid      bool
				ready      bool
				live       bool
				partial    bool
				expression bool
				access     string
				sequence   int
				columnName sql.NullString
			)
			if err := rows.Scan(
				&name,
				&unique,
				&valid,
				&ready,
				&live,
				&partial,
				&expression,
				&access,
				&sequence,
				&columnName,
			); err != nil {
				return nil, err
			}
			definition := result[name]
			definition.unique = unique
			definition.valid = valid && ready && live && strings.EqualFold(access, "btree")
			definition.partial = partial
			definition.expression = expression
			if columnName.Valid {
				definition.columns = append(
					definition.columns,
					strings.ToLower(columnName.String),
				)
			}
			result[name] = definition
		}
		return result, rows.Err()
	default:
		return nil, fmt.Errorf("unsupported database type %q", kind)
	}
}

func containsColumns(columns map[string]columnInfo, required []string) bool {
	for _, name := range required {
		if _, ok := columns[name]; !ok {
			return false
		}
	}
	return true
}

func hasAnyColumn(columns map[string]columnInfo, names []string) bool {
	for _, name := range names {
		if _, ok := columns[name]; ok {
			return true
		}
	}
	return false
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func readFirstLine(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	return "", scanner.Err()
}

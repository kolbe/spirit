package checksum

import (
	"context"
	"database/sql"
	"os"
	"testing"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/sirupsen/logrus"

	"github.com/stretchr/testify/assert"

	"github.com/squareup/spirit/pkg/repl"
	"github.com/squareup/spirit/pkg/table"
)

func dsn() string {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		return "msandbox:msandbox@tcp(127.0.0.1:8030)/test"
	}
	return dsn
}

func runSQL(t *testing.T, stmt string) {
	db, err := sql.Open("mysql", dsn())
	assert.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(stmt)
	assert.NoError(t, err)
}

func TestBasicChecksum(t *testing.T) {
	runSQL(t, "DROP TABLE IF EXISTS t1, t2, _t1_chkpnt")
	runSQL(t, "CREATE TABLE t1 (a INT NOT NULL, b INT, c INT, PRIMARY KEY (a))")
	runSQL(t, "CREATE TABLE t2 (a INT NOT NULL, b INT, c INT, PRIMARY KEY (a))")
	runSQL(t, "CREATE TABLE _t1_chkpnt (a INT)") // for binlog advancement
	runSQL(t, "INSERT INTO t1 VALUES (1, 2, 3)")
	runSQL(t, "INSERT INTO t2 VALUES (1, 2, 3)")

	db, err := sql.Open("mysql", dsn())
	assert.NoError(t, err)

	t1 := table.NewTableInfo(db, "test", "t1")
	assert.NoError(t, t1.SetInfo(context.TODO()))
	t2 := table.NewTableInfo(db, "test", "t2")
	assert.NoError(t, t2.SetInfo(context.TODO()))
	logger := logrus.New()

	cfg, err := mysql.ParseDSN(dsn())
	assert.NoError(t, err)
	feed := repl.NewClient(db, cfg.Addr, t1, t2, cfg.User, cfg.Passwd, &repl.ClientConfig{
		Logger:      logger,
		Concurrency: 4,
		BatchSize:   10000,
	})
	assert.NoError(t, feed.Run())

	checker, err := NewChecker(db, t1, t2, feed, NewCheckerDefaultConfig())
	assert.NoError(t, err)

	assert.Nil(t, checker.recentValue)
	assert.Equal(t, "TBD", checker.RecentValue())
	assert.NoError(t, checker.Run(context.Background()))
	assert.Equal(t, "1", checker.RecentValue())
}

func TestBasicValidation(t *testing.T) {
	runSQL(t, "DROP TABLE IF EXISTS t1, t2, _t1_chkpnt")
	runSQL(t, "CREATE TABLE t1 (a INT NOT NULL, b INT, c INT, PRIMARY KEY (a))")
	runSQL(t, "CREATE TABLE t2 (a INT NOT NULL, b INT, c INT, PRIMARY KEY (a))")
	runSQL(t, "CREATE TABLE _t1_chkpnt (a INT)") // for binlog advancement
	runSQL(t, "INSERT INTO t1 VALUES (1, 2, 3)")
	runSQL(t, "INSERT INTO t2 VALUES (1, 2, 3)")

	db, err := sql.Open("mysql", dsn())
	assert.NoError(t, err)

	t1 := table.NewTableInfo(db, "test", "t1")
	assert.NoError(t, t1.SetInfo(context.TODO()))
	t2 := table.NewTableInfo(db, "test", "t2")
	assert.NoError(t, t2.SetInfo(context.TODO()))
	logger := logrus.New()

	cfg, err := mysql.ParseDSN(dsn())
	assert.NoError(t, err)
	feed := repl.NewClient(db, cfg.Addr, t1, t2, cfg.User, cfg.Passwd, &repl.ClientConfig{
		Logger:      logger,
		Concurrency: 4,
		BatchSize:   10000,
	})
	assert.NoError(t, feed.Run())

	_, err = NewChecker(db, nil, t2, feed, NewCheckerDefaultConfig())
	assert.EqualError(t, err, "table and newTable must be non-nil")
	_, err = NewChecker(db, t1, nil, feed, NewCheckerDefaultConfig())
	assert.EqualError(t, err, "table and newTable must be non-nil")
	_, err = NewChecker(db, t1, t2, feed, NewCheckerDefaultConfig())
	assert.NoError(t, err)
	_, err = NewChecker(db, t1, t2, nil, NewCheckerDefaultConfig()) // no feed
	assert.EqualError(t, err, "feed must be non-nil")
}

func TestCorruptChecksum(t *testing.T) {
	runSQL(t, "DROP TABLE IF EXISTS t1, t2, _t1_chkpnt")
	runSQL(t, "CREATE TABLE t1 (a INT NOT NULL, b INT, c INT, PRIMARY KEY (a))")
	runSQL(t, "CREATE TABLE t2 (a INT NOT NULL, b INT, c INT, PRIMARY KEY (a))")
	runSQL(t, "CREATE TABLE _t1_chkpnt (a INT)") // for binlog advancement
	runSQL(t, "INSERT INTO t1 VALUES (1, 2, 3)")
	runSQL(t, "INSERT INTO t2 VALUES (1, 2, 3)")
	runSQL(t, "INSERT INTO t2 VALUES (2, 2, 3)") // corrupt

	db, err := sql.Open("mysql", dsn())
	assert.NoError(t, err)

	t1 := table.NewTableInfo(db, "test", "t1")
	assert.NoError(t, t1.SetInfo(context.TODO()))
	t2 := table.NewTableInfo(db, "test", "t2")
	assert.NoError(t, t2.SetInfo(context.TODO()))
	logger := logrus.New()

	cfg, err := mysql.ParseDSN(dsn())
	assert.NoError(t, err)
	feed := repl.NewClient(db, cfg.Addr, t1, t2, cfg.User, cfg.Passwd, &repl.ClientConfig{
		Logger:      logger,
		Concurrency: 4,
		BatchSize:   10000,
	})
	assert.NoError(t, feed.Run())

	checker, err := NewChecker(db, t1, t2, feed, NewCheckerDefaultConfig())
	assert.NoError(t, err)
	err = checker.Run(context.Background())
	assert.ErrorContains(t, err, "checksum mismatch")
}

func TestBoundaryCases(t *testing.T) {
	runSQL(t, "DROP TABLE IF EXISTS t1, t2")
	runSQL(t, "CREATE TABLE t1 (a INT NOT NULL, b FLOAT, c VARCHAR(255), PRIMARY KEY (a))")
	runSQL(t, "CREATE TABLE t2 (a INT NOT NULL, b FLOAT, c VARCHAR(255), PRIMARY KEY (a))")
	runSQL(t, "INSERT INTO t1 VALUES (1, 2.2, '')")   // null vs empty string
	runSQL(t, "INSERT INTO t2 VALUES (1, 2.2, NULL)") // should not compare

	db, err := sql.Open("mysql", dsn())
	assert.NoError(t, err)

	t1 := table.NewTableInfo(db, "test", "t1")
	assert.NoError(t, t1.SetInfo(context.TODO()))
	t2 := table.NewTableInfo(db, "test", "t2")
	assert.NoError(t, t2.SetInfo(context.TODO()))
	logger := logrus.New()

	cfg, err := mysql.ParseDSN(dsn())
	assert.NoError(t, err)
	feed := repl.NewClient(db, cfg.Addr, t1, t2, cfg.User, cfg.Passwd, &repl.ClientConfig{
		Logger:      logger,
		Concurrency: 4,
		BatchSize:   10000,
	})
	assert.NoError(t, feed.Run())

	checker, err := NewChecker(db, t1, t2, feed, NewCheckerDefaultConfig())
	assert.NoError(t, err)
	assert.Error(t, checker.Run(context.Background()))

	// UPDATE t1 to also be NULL
	runSQL(t, "UPDATE t1 SET c = NULL")
	checker, err = NewChecker(db, t1, t2, feed, NewCheckerDefaultConfig())
	assert.NoError(t, err)
	assert.NoError(t, checker.Run(context.Background()))
}

// sqliteprobe runs modernc.org/sqlite, the pure-Go SQLite that Go programs
// reach for when they want a database without cgo. Its libc is translated
// from C and issues bare syscalls with Linux-shaped arguments (fcntl record
// locks, mmap of the WAL index, pread/pwrite), a path the standard library
// never takes, so a translation the stdlib suites cannot see shows up here.
//
// It prints "ok <name>" per check and "ok all" last; the first failure
// prints "FAIL <name>: <why>" and exits 1.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func fail(name, format string, args ...any) {
	fmt.Printf("FAIL %s: %s\n", name, fmt.Sprintf(format, args...))
	os.Exit(1)
}

func must(name string, err error) {
	if err != nil {
		fail(name, "%v", err)
	}
	fmt.Println("ok " + name)
}

func main() {
	dir, err := os.MkdirTemp("", "sqliteprobe")
	if err != nil {
		fail("mkdirtemp", "%v", err)
	}
	defer os.RemoveAll(dir)

	// busy_timeout(0): a lock conflict answers SQLITE_BUSY at once instead
	// of waiting, so the exclusion check below reads the answer directly.
	dsn := filepath.Join(dir, "probe.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(0)"
	a, err := sql.Open("sqlite", dsn)
	if err != nil {
		fail("open", "%v", err)
	}
	defer a.Close()
	a.SetMaxOpenConns(1)
	must("open", a.Ping())

	var mode string
	if err := a.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		fail("wal", "%v", err)
	}
	if mode != "wal" {
		fail("wal", "journal_mode is %q, want wal", mode)
	}
	fmt.Println("ok wal")

	_, err = a.Exec("CREATE TABLE t (n INTEGER NOT NULL)")
	must("create", err)
	_, err = a.Exec("INSERT INTO t (n) VALUES (1), (2), (3)")
	must("insert", err)

	// A second handle is a second connection and a second descriptor on
	// the same file: it reads through the WAL the first one wrote.
	b, err := sql.Open("sqlite", dsn)
	if err != nil {
		fail("reopen", "%v", err)
	}
	defer b.Close()
	b.SetMaxOpenConns(1)
	var sum int
	if err := b.QueryRow("SELECT sum(n) FROM t").Scan(&sum); err != nil {
		fail("readback", "%v", err)
	}
	if sum != 6 {
		fail("readback", "sum(n) = %d, want 6", sum)
	}
	fmt.Println("ok readback")

	// The write lock excludes: while one connection holds BEGIN IMMEDIATE,
	// the other is refused with SQLITE_BUSY, and is let in once it commits.
	ctx := context.Background()
	ca, err := a.Conn(ctx)
	if err != nil {
		fail("writelock", "%v", err)
	}
	defer ca.Close()
	if _, err := ca.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		fail("writelock", "%v", err)
	}
	_, err = b.Exec("BEGIN IMMEDIATE")
	if err == nil {
		fail("exclusion", "a second connection took the write lock while the first held it")
	}
	if !strings.Contains(err.Error(), "database is locked") {
		fail("exclusion", "second BEGIN IMMEDIATE gave %v, want SQLITE_BUSY", err)
	}
	fmt.Println("ok exclusion")
	if _, err := ca.ExecContext(ctx, "COMMIT"); err != nil {
		fail("release", "%v", err)
	}
	cb, err := b.Conn(ctx)
	if err != nil {
		fail("release", "%v", err)
	}
	defer cb.Close()
	if _, err := cb.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		fail("release", "the write lock was not handed over after COMMIT: %v", err)
	}
	_, err = cb.ExecContext(ctx, "COMMIT")
	must("release", err)

	fmt.Println("ok all")
}

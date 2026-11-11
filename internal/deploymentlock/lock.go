// Package deploymentlock serializes SaxBase writes across Goose and object phases.
package deploymentlock

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"saxbase/internal/dialect"
)

type Reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func Acquire(ctx context.Context, db *sql.DB, name dialect.Name) (*sql.Conn, func() error, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	if name == dialect.Postgres {
		return acquirePostgres(ctx, conn)
	}
	if name != dialect.SQLServer {
		conn.Close()
		return nil, nil, fmt.Errorf("unsupported database dialect %q", name)
	}
	var code int
	err = conn.QueryRowContext(ctx, `DECLARE @result int; EXEC @result=sys.sp_getapplock @Resource=N'SaxBase.objects', @LockMode='Exclusive', @LockOwner='Session', @LockTimeout=30000; SELECT @result;`).Scan(&code)
	if err != nil || code < 0 {
		if err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
		if err == nil {
			err = fmt.Errorf("deployment lock failed (code %d)", code)
		}
		return nil, nil, err
	}
	release := func() error {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var code int
		err := conn.QueryRowContext(cleanup, `DECLARE @result int; EXEC @result=sys.sp_releaseapplock @Resource=N'SaxBase.objects', @LockOwner='Session'; SELECT @result;`).Scan(&code)
		if err == nil && code < 0 {
			err = fmt.Errorf("release deployment lock failed (code %d)", code)
		}
		if err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		closeErr := conn.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	return conn, release, nil
}

const postgresLockKey int64 = 0x53617842617365

func acquirePostgres(ctx context.Context, conn *sql.Conn) (*sql.Conn, func() error, error) {
	lockCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := conn.ExecContext(lockCtx, "SELECT pg_advisory_lock($1)", postgresLockKey); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		conn.Close()
		return nil, nil, fmt.Errorf("deployment lock failed: %w", err)
	}
	release := func() error {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		err := conn.QueryRowContext(cleanup, "SELECT pg_advisory_unlock($1)", postgresLockKey).Scan(&unlocked)
		if err == nil && !unlocked {
			err = errors.New("release deployment lock failed: lock was not held")
		}
		if err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		closeErr := conn.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	return conn, release, nil
}

func Invalidate(ctx context.Context, db Reader, name dialect.Name) error {
	switch name {
	case dialect.SQLServer:
		_, err := db.ExecContext(ctx, "IF OBJECT_ID(N'dbo.saxbase_releases', N'U') IS NOT NULL UPDATE dbo.saxbase_releases SET is_current=0")
		return err
	case dialect.Postgres:
		var table sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT to_regclass('public.saxbase_releases')").Scan(&table); err != nil || !table.Valid {
			return err
		}
		_, err := db.ExecContext(ctx, "UPDATE public.saxbase_releases SET is_current=false")
		return err
	default:
		return fmt.Errorf("unsupported database dialect %q", name)
	}
}

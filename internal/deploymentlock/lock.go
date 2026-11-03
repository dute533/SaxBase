// Package deploymentlock serializes SaxBase writes across Goose and object phases.
package deploymentlock

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"
)

type Reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func Acquire(ctx context.Context, db *sql.DB) (*sql.Conn, func() error, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
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

func Invalidate(ctx context.Context, db Reader) error {
	_, err := db.ExecContext(ctx, "IF OBJECT_ID(N'dbo.saxbase_releases', N'U') IS NOT NULL UPDATE dbo.saxbase_releases SET is_current=0")
	return err
}

package objects

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"saxbase/internal/dialect"
)

type objectDialect struct {
	name               dialect.Name
	initializeReleases string
	selectFingerprint  string
	updateFingerprint  string
	insertRelease      string
	selectHistory      string
	selectSnapshot     string
	clearCurrent       string
	setCurrent         string
	selectCurrent      string
}

func objectDialectFor(name dialect.Name) (objectDialect, error) {
	switch name {
	case dialect.SQLServer:
		return objectDialect{
			name: name,
			initializeReleases: `IF OBJECT_ID(N'dbo.saxbase_releases', N'U') IS NULL
 BEGIN
 CREATE TABLE dbo.saxbase_releases (
 version varchar(39) NOT NULL CONSTRAINT PK_saxbase_releases PRIMARY KEY,
 fingerprint char(64) NOT NULL,
 is_current bit NOT NULL CONSTRAINT DF_saxbase_releases_is_current DEFAULT 0
 );
 CREATE UNIQUE INDEX UX_saxbase_releases_current
 ON dbo.saxbase_releases(is_current) WHERE is_current=1;
 END;`,
			selectFingerprint: "SELECT fingerprint FROM dbo.saxbase_releases WHERE version=@version",
			updateFingerprint: "UPDATE dbo.saxbase_releases SET fingerprint=@fingerprint WHERE version=@version",
			insertRelease:     "INSERT INTO dbo.saxbase_releases(version,fingerprint) VALUES(@version,@fingerprint)",
			selectHistory:     "SELECT version FROM dbo.saxbase_releases",
			selectSnapshot:    "SELECT version, fingerprint FROM dbo.saxbase_releases WHERE version=@version",
			clearCurrent:      "UPDATE dbo.saxbase_releases SET is_current=0",
			setCurrent:        "UPDATE dbo.saxbase_releases SET is_current=1 WHERE version=@version",
			selectCurrent:     "SELECT version FROM dbo.saxbase_releases WHERE is_current=1",
		}, nil
	case dialect.Postgres:
		return objectDialect{
			name: name,
			initializeReleases: `CREATE TABLE IF NOT EXISTS public.saxbase_releases (
 version varchar(39) PRIMARY KEY,
 fingerprint char(64) NOT NULL,
 is_current boolean NOT NULL DEFAULT false
 );
 CREATE UNIQUE INDEX IF NOT EXISTS ux_saxbase_releases_current
 ON public.saxbase_releases(is_current) WHERE is_current=true;`,
			selectFingerprint: "SELECT fingerprint FROM public.saxbase_releases WHERE version=$1",
			updateFingerprint: "UPDATE public.saxbase_releases SET fingerprint=$1 WHERE version=$2",
			insertRelease:     "INSERT INTO public.saxbase_releases(version,fingerprint) VALUES($1,$2)",
			selectHistory:     "SELECT version FROM public.saxbase_releases",
			selectSnapshot:    "SELECT version, fingerprint FROM public.saxbase_releases WHERE version=$1",
			clearCurrent:      "UPDATE public.saxbase_releases SET is_current=false",
			setCurrent:        "UPDATE public.saxbase_releases SET is_current=true WHERE version=$1",
			selectCurrent:     "SELECT version FROM public.saxbase_releases WHERE is_current=true",
		}, nil
	default:
		return objectDialect{}, fmt.Errorf("unsupported database dialect %q", name)
	}
}

func (d objectDialect) versionArg(version string) []any {
	if d.name == dialect.SQLServer {
		return []any{sql.Named("version", version)}
	}
	return []any{version}
}

func (d objectDialect) fingerprintArgs(fingerprint, version string) []any {
	if d.name == dialect.SQLServer {
		return []any{sql.Named("fingerprint", fingerprint), sql.Named("version", version)}
	}
	return []any{fingerprint, version}
}

func (d objectDialect) insertArgs(version, fingerprint string) []any {
	if d.name == dialect.SQLServer {
		return []any{sql.Named("version", version), sql.Named("fingerprint", fingerprint)}
	}
	return []any{version, fingerprint}
}

func (d objectDialect) historyExists(ctx context.Context, db reader) (bool, error) {
	switch d.name {
	case dialect.SQLServer:
		var id sql.NullInt64
		if err := db.QueryRowContext(ctx, "SELECT OBJECT_ID(N'dbo.saxbase_releases', N'U')").Scan(&id); err != nil {
			return false, err
		}
		return id.Valid, nil
	case dialect.Postgres:
		var table sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT to_regclass('public.saxbase_releases')").Scan(&table); err != nil {
			return false, err
		}
		return table.Valid, nil
	default:
		return false, fmt.Errorf("unsupported database dialect %q", d.name)
	}
}

func (d objectDialect) quote(identifier string) string {
	if d.name == dialect.SQLServer {
		return "[" + strings.ReplaceAll(identifier, "]", "]]") + "]"
	}
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func (d objectDialect) drop(id identity) string {
	return "DROP " + id.kind + " IF EXISTS " + d.quote(id.schema) + "." + d.quote(id.name) + ";"
}

func (d objectDialect) restore(definition string) string {
	return restoreDefinition(definition, d.name)
}

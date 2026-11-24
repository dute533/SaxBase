package objects

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"saxbase/internal/dialect"
)

// ParseDiffLine splits a diff line into op ("+", "-", "~"), tag ("table",
// "column", "module", ...) and identity ("schema.table.column", ...).
func ParseDiffLine(line string) (op, tag, identity string) {
	if len(line) < 3 {
		return "", "", line
	}
	if line[1] == ' ' && line[0] == '~' {
		// "~ schema.name type definition changed" (space-separated)
		fields := strings.Fields(line[2:])
		if len(fields) > 0 {
			return "~", "module", fields[0]
		}
		return "~", "module", ""
	}
	rest := line[2:]
	parts := strings.Split(rest, "\t")
	tag = parts[0]
	if len(parts) > 1 {
		identity = parts[1] // just the name; the remaining fields are values
	}
	return line[:1], tag, identity
}

// RestoreDefinition makes a stored CREATE statement repeatable for the dialect.
func RestoreDefinition(definition string, name dialect.Name) string {
	return restoreDefinition(definition, name)
}

// Correction is one alignment step: a label plus one or more SQL statements.
type Correction struct {
	Label       string
	Statements  []string
	Destructive bool // may drop or truncate data
}

// BuildCorrection generates the statements that align the live database to the
// release description, using scratch (the replayed release database) as the
// source of truth for DDL. diff is the described-vs-live diff (managed modules
// already filtered out); fileStates maps managed object paths to drift states.
// Purely added live objects (op "+") are left alone: unmanaged by SaxBase.
func BuildCorrection(ctx context.Context, scratch *sql.DB, name dialect.Name, diff []string, files []File, fileStates map[string]string) ([]Correction, error) {
	d, err := objectDialectFor(name)
	if err != nil {
		return nil, err
	}
	var out []Correction
	for _, f := range files {
		if fileStates[f.Path] != "ok" && !f.Delete {
			out = append(out, Correction{Label: f.Path, Statements: []string{RestoreDefinition(f.SQL, name)}})
		}
	}
	added := map[string]bool{}
	for _, line := range diff {
		op, tag, id := ParseDiffLine(line)
		if op == "+" {
			added[tag+" "+id] = true
		}
	}
	done := map[string]bool{}
	for _, line := range diff {
		op, tag, id := ParseDiffLine(line)
		if op == "+" {
			continue // exists only in live: unmanaged
		}
		if done[op+tag+id] {
			continue
		}
		done[op+tag+id] = true
		switch tag {
		case "module":
			defs, err := moduleDefs(ctx, scratch, d, id)
			if err != nil {
				return nil, err
			}
			out = append(out, Correction{Label: "module " + id, Statements: defs})
		case "table":
			schema, table := splitQual(id)
			stmts, err := tableCreate(ctx, scratch, d, schema, table)
			if err != nil {
				return nil, err
			}
			for _, s := range stmts {
				out = append(out, Correction{Label: "table " + id, Statements: []string{s}, Destructive: true})
			}
		case "column":
			c, err := alignColumn(ctx, scratch, d, id, added["column "+id])
			if err != nil {
				return nil, err
			}
			out = append(out, *c)
		case "index", "key", "check", "fk":
			c, err := alignConstraint(ctx, scratch, d, tag, id)
			if err != nil {
				return nil, err
			}
			if c != nil {
				out = append(out, *c)
			}
		}
	}
	return out, nil
}

func splitQual(identity string) (schema, name string) {
	parts := strings.SplitN(identity, ".", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "dbo", identity
}

func queryArgs(d objectDialect, args ...any) []any {
	if d.name == dialect.Postgres {
		return args
	}
	names := []string{"p1", "p2", "p3"}
	named := make([]any, 0, len(args))
	for i, a := range args {
		named = append(named, sql.Named(names[i], a))
	}
	return named
}

// moduleDefs returns repeatable statements to (re)create a module from the
// scratch DB: CREATE OR ALTER on SQL Server; on postgres CREATE OR REPLACE for
// views/functions and DROP + CREATE for triggers.
func moduleDefs(ctx context.Context, db *sql.DB, d objectDialect, qualName string) ([]string, error) {
	schema, name := splitQual(qualName)
	var args []any
	if d.name == dialect.Postgres {
		args = []any{schema, name}
	} else {
		args = []any{sql.Named("schema", schema), sql.Named("name", name)}
	}
	rows, err := db.QueryContext(ctx, d.selectModuleDefs, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var def string
	for rows.Next() {
		if err := rows.Scan(&def); err != nil {
			return nil, err
		}
		break
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if def == "" {
		return nil, fmt.Errorf("module %s not found in replayed release", qualName)
	}
	if d.name == dialect.Postgres {
		lower := strings.ToLower(strings.TrimSpace(def))
		switch {
		case strings.HasPrefix(lower, "create trigger"):
			// "CREATE TRIGGER name ... ON schema.table ..." — extract the table
			fields := strings.Fields(def)
			table := ""
			for i, f := range fields {
				if strings.EqualFold(f, "ON") && i+1 < len(fields) {
					table = fields[i+1]
				}
			}
			return []string{"DROP TRIGGER IF EXISTS " + name + " ON " + table + ";", def}, nil
		case !strings.HasPrefix(lower, "create"):
			return []string{"CREATE OR REPLACE VIEW " + qualName + " AS\n" + def}, nil
		default:
			return []string{def}, nil // pg_get_functiondef is already CREATE OR REPLACE
		}
	}
	return []string{restoreDefinition(def, d.name)}, nil
}

func tableOf(identity string) string {
	parts := strings.Split(identity, ".")
	if len(parts) >= 2 {
		return strings.Join(parts[:len(parts)-1], ".")
	}
	return identity
}

// tableCreate returns CREATE TABLE plus constraints and indexes from scratch.
func tableCreate(ctx context.Context, db *sql.DB, d objectDialect, schema, table string) ([]string, error) {
	cols, err := columnDefs(ctx, db, d, schema, table, "")
	if err != nil {
		return nil, err
	}
	if cols == "" {
		return nil, fmt.Errorf("table %s.%s not found in replayed release", schema, table)
	}
	qual := schema + "." + table
	stmts := []string{"CREATE TABLE " + qual + " (\n  " + cols + "\n)"}
	cons, err := constraintDDLs(ctx, db, d, schema, table)
	if err != nil {
		return nil, err
	}
	stmts = append(stmts, cons...)
	idxs, err := indexDDLs(ctx, db, d, schema, table)
	if err != nil {
		return nil, err
	}
	return append(stmts, idxs...), nil
}

// columnDefs renders all columns (or one, when col != "") as "name type ...".
func columnDefs(ctx context.Context, db *sql.DB, d objectDialect, schema, table, col string) (string, error) {
	var query string
	var args []any
	if d.name == dialect.Postgres {
		colsel := ""
		args = queryArgs(d, schema, table)
		if col != "" {
			colsel = " AND a.attname = $3"
			args = append(args, col)
		}
		query = `SELECT string_agg(col, E',\n  ') FROM (
			SELECT a.attname || ' ' || format_type(a.atttypid, a.atttypmod)
			  || CASE WHEN a.attnotnull THEN ' NOT NULL' ELSE '' END
			  || CASE a.attidentity WHEN 'a' THEN ' GENERATED ALWAYS AS IDENTITY' WHEN 'd' THEN ' GENERATED BY DEFAULT AS IDENTITY' ELSE '' END
			  || COALESCE(' DEFAULT ' || pg_get_expr(d.adbin, d.adrelid), '') AS col
			FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
			WHERE a.attrelid = ($1 || '.' || $2)::regclass AND a.attnum > 0 AND NOT a.attisdropped` + colsel + `
			ORDER BY a.attnum) x`
	} else {
		colsel := ""
		args = []any{sql.Named("p1", schema), sql.Named("p2", table)}
		if col != "" {
			colsel = " AND c.name = @p3"
			args = append(args, sql.Named("p3", col))
		}
		query = `SELECT STRING_AGG(col, ',
  ') WITHIN GROUP (ORDER BY ord) FROM (
			SELECT c.name COLLATE DATABASE_DEFAULT + ' ' + ty.name COLLATE DATABASE_DEFAULT + CASE WHEN ty.name IN ('varchar','char','varbinary','binary') THEN '(' + IIF(c.max_length=-1,'MAX',CAST(c.max_length AS varchar(10))) + ')'
			  WHEN ty.name IN ('nvarchar','nchar') THEN '(' + IIF(c.max_length=-1,'MAX',CAST(c.max_length/2 AS varchar(10))) + ')'
			  WHEN ty.name IN ('decimal','numeric') THEN '(' + CAST(c.precision AS varchar(10)) + ',' + CAST(c.scale AS varchar(10)) + ')' ELSE '' END
			  || CASE WHEN c.is_identity = 1 THEN ' IDENTITY(1,1)' ELSE '' END
			  || CASE WHEN c.is_nullable = 0 THEN ' NOT NULL' ELSE ' NULL' END
			  || COALESCE(' DEFAULT ' + dc.definition COLLATE DATABASE_DEFAULT, '') AS col, c.column_id AS ord
			FROM sys.columns c JOIN sys.types ty ON ty.user_type_id = c.user_type_id
			LEFT JOIN sys.default_constraints dc ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
			WHERE c.object_id = OBJECT_ID(@p1 + '.' + @p2)` + colsel + `) x`
	}
	var out string
	err := db.QueryRowContext(ctx, query, args...).Scan(&out)
	return out, err
}

func constraintDDLs(ctx context.Context, db *sql.DB, d objectDialect, schema, table string) ([]string, error) {
	var query string
	if d.name == dialect.Postgres {
		query = `SELECT 'ALTER TABLE ' || $1 || '.' || $2 || ' ADD CONSTRAINT ' || con.conname || ' ' || pg_get_constraintdef(con.oid)
			FROM pg_constraint con WHERE con.conrelid = ($1 || '.' || $2)::regclass ORDER BY con.conname`
	} else {
		query = `SELECT 'ALTER TABLE ' + @p1 + '.' + @p2 + ' ADD CONSTRAINT ' + kc.name COLLATE DATABASE_DEFAULT + ' ' +
			CASE kc.type WHEN 'PK' THEN 'PRIMARY KEY' WHEN 'UQ' THEN 'UNIQUE' ELSE kc.type END + ' (' +
			STRING_AGG(c.name COLLATE DATABASE_DEFAULT, ',') WITHIN GROUP (ORDER BY ic.key_ordinal) + ')'
			FROM sys.key_constraints kc
			JOIN sys.index_columns ic ON ic.object_id = kc.parent_object_id AND ic.index_id = kc.unique_index_id
			JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
			WHERE kc.parent_object_id = OBJECT_ID(@p1 + '.' + @p2) GROUP BY kc.name, kc.type
			UNION ALL
			SELECT 'ALTER TABLE ' + @p1 + '.' + @p2 + ' ADD CONSTRAINT ' + fk.name COLLATE DATABASE_DEFAULT + ' FOREIGN KEY (' +
			STRING_AGG(pc.name COLLATE DATABASE_DEFAULT, ',') WITHIN GROUP (ORDER BY fkc.constraint_column_id) + ') REFERENCES ' +
			OBJECT_SCHEMA_NAME(fk.referenced_object_id) COLLATE DATABASE_DEFAULT + '.' + OBJECT_NAME(fk.referenced_object_id) COLLATE DATABASE_DEFAULT + ' (' +
			STRING_AGG(rc.name COLLATE DATABASE_DEFAULT, ',') WITHIN GROUP (ORDER BY fkc.constraint_column_id) + ')'
			FROM sys.foreign_keys fk JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
			JOIN sys.columns pc ON pc.object_id = fk.parent_object_id AND pc.column_id = fkc.parent_column_id
			JOIN sys.columns rc ON rc.object_id = fk.referenced_object_id AND rc.column_id = fkc.referenced_column_id
			WHERE fk.parent_object_id = OBJECT_ID(@p1 + '.' + @p2) GROUP BY fk.name, fk.referenced_object_id
			UNION ALL
			SELECT 'ALTER TABLE ' + @p1 + '.' + @p2 + ' ADD CONSTRAINT ' + cc.name COLLATE DATABASE_DEFAULT + ' CHECK ' + cc.definition
			FROM sys.check_constraints cc WHERE cc.parent_object_id = OBJECT_ID(@p1 + '.' + @p2)`
	}
	return queryStrings(ctx, db, query, queryArgs(d, schema, table)...)
}

// constraintNamesForColumn lists constraints that depend on a column (SQL Server
// blocks ALTER COLUMN while they exist).
func constraintNamesForColumn(ctx context.Context, db *sql.DB, d objectDialect, schema, table, col string) ([]string, error) {
	if d.name == dialect.Postgres {
		return nil, nil
	}
	return queryStrings(ctx, db, `SELECT kc.name FROM sys.key_constraints kc
		JOIN sys.index_columns ic ON ic.object_id = kc.parent_object_id AND ic.index_id = kc.unique_index_id
		JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
		WHERE kc.parent_object_id = OBJECT_ID(@p1 + '.' + @p2) AND c.name = @p3
		UNION
		SELECT name FROM sys.default_constraints WHERE parent_object_id = OBJECT_ID(@p1 + '.' + @p2) AND parent_column_id = (SELECT column_id FROM sys.columns WHERE object_id = OBJECT_ID(@p1 + '.' + @p2) AND name = @p3)
		UNION
		SELECT name FROM sys.check_constraints WHERE parent_object_id = OBJECT_ID(@p1 + '.' + @p2) AND parent_column_id = (SELECT column_id FROM sys.columns WHERE object_id = OBJECT_ID(@p1 + '.' + @p2) AND name = @p3)
		UNION
		SELECT fk.name FROM sys.foreign_keys fk JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
		JOIN sys.columns c ON c.object_id = fk.parent_object_id AND c.column_id = fkc.parent_column_id
		WHERE fk.parent_object_id = OBJECT_ID(@p1 + '.' + @p2) AND c.name = @p3`,
		sql.Named("p1", schema), sql.Named("p2", table), sql.Named("p3", col))
}

func indexDDLs(ctx context.Context, db *sql.DB, d objectDialect, schema, table string) ([]string, error) {
	var query string
	if d.name == dialect.Postgres {
		query = `SELECT indexdef FROM pg_indexes WHERE schemaname = $1 AND tablename = $2
			AND indexname NOT IN (SELECT conname FROM pg_constraint WHERE conrelid = ($1 || '.' || $2)::regclass) ORDER BY indexname`
	} else {
		query = `SELECT 'CREATE ' + IIF(i.is_unique = 1, 'UNIQUE ', '') + 'INDEX ' + i.name COLLATE DATABASE_DEFAULT + ' ON ' + @p1 + '.' + @p2 + ' (' +
			STRING_AGG(c.name COLLATE DATABASE_DEFAULT + CASE WHEN ic.is_descending_key = 1 THEN ' DESC' ELSE '' END, ',') WITHIN GROUP (ORDER BY ic.key_ordinal) + ')' +
			ISNULL(' WHERE ' + i.filter_definition, '')
			FROM sys.indexes i JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id AND ic.is_included_column = 0
			JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
			WHERE i.object_id = OBJECT_ID(@p1 + '.' + @p2) AND i.index_id > 0
			AND NOT EXISTS (SELECT 1 FROM sys.key_constraints kc WHERE kc.parent_object_id = i.object_id AND kc.unique_index_id = i.index_id)
			GROUP BY i.name, i.is_unique, i.filter_definition`
	}
	return queryStrings(ctx, db, query, queryArgs(d, schema, table)...)
}

func queryStrings(ctx context.Context, db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// alignColumn builds ADD COLUMN (missing in live) or ALTER COLUMN (modified,
// dropping dependent constraints first on SQL Server) from the scratch def.
func alignColumn(ctx context.Context, db *sql.DB, d objectDialect, identity string, modified bool) (*Correction, error) {
	parts := strings.Split(identity, ".")
	col := parts[len(parts)-1]
	table := strings.Join(parts[:len(parts)-1], ".")
	schema, tbl := splitQual(table)
	def, err := columnDefs(ctx, db, d, schema, tbl, col)
	if err != nil {
		return nil, fmt.Errorf("column %s: %w", identity, err)
	}
	if !modified {
		return &Correction{Label: "column " + identity, Statements: []string{"ALTER TABLE " + table + " ADD COLUMN " + def + ";"}}, nil
	}
	if d.name == dialect.Postgres {
		var typ string
		if err := db.QueryRowContext(ctx, `SELECT format_type(a.atttypid, a.atttypmod) FROM pg_attribute a WHERE a.attrelid = ($1 || '.' || $2)::regclass AND a.attname = $3 AND NOT a.attisdropped`, schema, tbl, col).Scan(&typ); err != nil {
			return nil, err
		}
		return &Correction{Label: "column " + identity, Statements: []string{"ALTER TABLE " + table + " ALTER COLUMN " + col + " TYPE " + typ + ";"}, Destructive: true}, nil
	}
	// SQL Server: drop constraints depending on the column, alter, re-add them.
	names, err := constraintNamesForColumn(ctx, db, d, schema, tbl, col)
	if err != nil {
		return nil, err
	}
	var stmts []string
	for _, name := range names {
		stmts = append(stmts, "ALTER TABLE "+table+" DROP CONSTRAINT IF EXISTS "+name+";")
	}
	stmts = append(stmts, "ALTER TABLE "+table+" ALTER COLUMN "+def+";")
	cons, err := constraintDDLs(ctx, db, d, schema, tbl)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		for _, c := range cons {
			if strings.Contains(c, "CONSTRAINT "+name+" ") {
				stmts = append(stmts, c+";")
			}
		}
	}
	return &Correction{Label: "column " + identity, Statements: stmts, Destructive: true}, nil
}

// alignConstraint rebuilds a drifted/missing constraint or index from scratch.
func alignConstraint(ctx context.Context, db *sql.DB, d objectDialect, tag, identity string) (*Correction, error) {
	table := tableOf(identity)
	if tag == "fk" {
		var qual string
		var q string
		if d.name == dialect.Postgres {
			q = `SELECT n.nspname || '.' || c.relname FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE con.conname = $1`
		} else {
			q = `SELECT OBJECT_SCHEMA_NAME(fk.parent_object_id) COLLATE DATABASE_DEFAULT + '.' + OBJECT_NAME(fk.parent_object_id) COLLATE DATABASE_DEFAULT FROM sys.foreign_keys fk WHERE fk.name = @p1`
		}
		if err := db.QueryRowContext(ctx, q, queryArgs(d, identity)...).Scan(&qual); err != nil {
			return nil, fmt.Errorf("fk %s not found in replayed release: %w", identity, err)
		}
		table = qual
	}
	schema, tbl := splitQual(table)
	cname := identity
	if tag != "fk" {
		parts := strings.Split(identity, ".")
		cname = parts[len(parts)-1]
	}
	var drop, add string
	switch tag {
	case "index":
		if d.name == dialect.Postgres {
			drop = "DROP INDEX IF EXISTS " + identity
		} else {
			drop = "DROP INDEX IF EXISTS " + cname + " ON " + table
		}
		defs, err := indexDDLs(ctx, db, d, schema, tbl)
		if err != nil {
			return nil, err
		}
		for _, def := range defs {
			if strings.Contains(def, cname) {
				add = def
			}
		}
	default: // key, check, fk
		drop = "ALTER TABLE " + table + " DROP CONSTRAINT IF EXISTS " + cname
		defs, err := constraintDDLs(ctx, db, d, schema, tbl)
		if err != nil {
			return nil, err
		}
		for _, def := range defs {
			if strings.Contains(def, "CONSTRAINT "+cname+" ") {
				add = def
			}
		}
	}
	if add == "" {
		return nil, fmt.Errorf("%s %s not found in replayed release", tag, identity)
	}
	return &Correction{Label: tag + " " + identity, Statements: []string{drop + ";", add + ";"}, Destructive: tag != "index"}, nil
}

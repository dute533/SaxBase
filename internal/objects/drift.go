package objects

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"saxbase/internal/dialect"
)

// DriftReport compares the live database against the recorded release state.
type DriftReport struct {
	SchemaStored  string
	SchemaCurrent string
	SchemaDiff    []string // human-readable changes from SchemaStored to SchemaCurrent
	Objects       []Status
}

// Drifted reports whether the schema fingerprint or any managed object differs.
func (r DriftReport) Drifted() bool {
	if r.SchemaStored != r.SchemaCurrent {
		return true
	}
	for _, row := range r.Objects {
		if row.State != "ok" {
			return true
		}
	}
	return false
}

var orAlterRe = regexp.MustCompile(`(?i)^\s*CREATE\s+OR\s+ALTER\s+`)

// NormalizeDefinition canonicalizes an object definition for comparison: it
// strips the repeatable OR ALTER verb, collapses whitespace and lowercases.
func NormalizeDefinition(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	s = orAlterRe.ReplaceAllString(s, "CREATE ")
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

const schemaFingerprintKey = "schema_fingerprint"

// bookkeeping tables are SaxBase/Goose metadata, not user schema.
const bookkeepingFilter = "NOT (s.name = 'dbo' AND t.name IN ('saxbase_schema_version','saxbase_releases','saxbase_meta','migrations'))"

type fingerprintQuery struct{ tag, query string }

var fingerprintQueries = []fingerprintQuery{
	{"schema", `SELECT name FROM sys.schemas WHERE name NOT IN ('sys','INFORMATION_SCHEMA','guest') AND name NOT LIKE 'db\_%' ESCAPE '\' ORDER BY name`},
	{"table", `SELECT s.name + '.' + t.name FROM sys.tables t JOIN sys.schemas s ON s.schema_id = t.schema_id WHERE ` + bookkeepingFilter + ` ORDER BY 1`},
	{"column", `SELECT s.name + '.' + t.name + '.' + c.name, ty.name, c.max_length, c.precision, c.scale, c.is_nullable, c.is_identity, ISNULL(dc.definition, ''), ISNULL(cc.definition, '')
	FROM sys.columns c
	JOIN sys.tables t ON t.object_id = c.object_id
	JOIN sys.schemas s ON s.schema_id = t.schema_id
	JOIN sys.types ty ON ty.user_type_id = c.user_type_id
	LEFT JOIN sys.default_constraints dc ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
	LEFT JOIN sys.computed_columns cc ON cc.object_id = c.object_id AND cc.column_id = c.column_id
	WHERE ` + bookkeepingFilter + ` ORDER BY 1, c.column_id`},
	{"index", `SELECT s.name + '.' + t.name + '.' + i.name, i.is_unique, i.type_desc, ISNULL(i.filter_definition, ''), STRING_AGG(c.name + CASE WHEN ic.is_descending_key = 1 THEN ' DESC' ELSE '' END, ',') WITHIN GROUP (ORDER BY ic.key_ordinal)
	FROM sys.indexes i
	JOIN sys.tables t ON t.object_id = i.object_id
	JOIN sys.schemas s ON s.schema_id = t.schema_id
	JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id AND ic.is_included_column = 0
	JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
	WHERE i.index_id > 0 AND ` + bookkeepingFilter + `
	GROUP BY s.name, t.name, i.name, i.is_unique, i.type_desc, i.filter_definition ORDER BY 1`},
	{"key", `SELECT s.name + '.' + t.name + '.' + kc.name, kc.type_desc, STRING_AGG(c.name, ',') WITHIN GROUP (ORDER BY ic.key_ordinal)
	FROM sys.key_constraints kc
	JOIN sys.tables t ON t.object_id = kc.parent_object_id
	JOIN sys.schemas s ON s.schema_id = t.schema_id
	JOIN sys.index_columns ic ON ic.object_id = kc.parent_object_id AND ic.index_id = kc.unique_index_id
	JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
	WHERE ` + bookkeepingFilter + `
	GROUP BY s.name, t.name, kc.name, kc.type_desc ORDER BY 1`},
	{"fk", `SELECT fk.name, OBJECT_SCHEMA_NAME(fk.parent_object_id) + '.' + OBJECT_NAME(fk.parent_object_id), OBJECT_SCHEMA_NAME(fk.referenced_object_id) + '.' + OBJECT_NAME(fk.referenced_object_id), STRING_AGG(pc.name + '->' + rc.name, ',') WITHIN GROUP (ORDER BY fkc.constraint_column_id)
	FROM sys.foreign_keys fk
	JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
	JOIN sys.columns pc ON pc.object_id = fk.parent_object_id AND pc.column_id = fkc.parent_column_id
	JOIN sys.columns rc ON rc.object_id = fk.referenced_object_id AND rc.column_id = fkc.referenced_column_id
	GROUP BY fk.name, fk.parent_object_id, fk.referenced_object_id ORDER BY 1`},
	{"check", `SELECT s.name + '.' + t.name + '.' + cc.name, cc.definition
	FROM sys.check_constraints cc
	JOIN sys.tables t ON t.object_id = cc.parent_object_id
	JOIN sys.schemas s ON s.schema_id = t.schema_id
	WHERE ` + bookkeepingFilter + ` ORDER BY 1`},
	{"module", `SELECT s.name + '.' + o.name, o.type, m.definition
	FROM sys.sql_modules m
	JOIN sys.objects o ON o.object_id = m.object_id
	JOIN sys.schemas s ON s.schema_id = o.schema_id
	WHERE o.type IN ('V','P','FN','TF','IF','TR') ORDER BY 1`},
}

// fingerprint renders the canonical schema and module metadata of the database,
// one tagged line per catalog row. Module definitions are stored as a hash so
// the text stays small; diffFingerprints reports them as "definition changed".
func fingerprint(ctx context.Context, db reader) (string, error) {
	var b strings.Builder
	for _, q := range fingerprintQueries {
		rows, err := db.QueryContext(ctx, q.query)
		if err != nil {
			return "", err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return "", err
			}
			parts := make([]string, len(cols))
			for i, v := range vals {
				switch value := v.(type) {
				case nil:
					parts[i] = ""
				case string:
					parts[i] = NormalizeDefinition(value)
				case []byte:
					parts[i] = NormalizeDefinition(string(value))
				default:
					parts[i] = fmt.Sprintf("%v", value)
				}
			}
			if q.tag == "module" {
				parts[len(parts)-1] = hashDefinition(parts[len(parts)-1])
			}
			fmt.Fprintln(&b, q.tag+"\t"+strings.Join(parts, "\t"))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", err
		}
		rows.Close()
	}
	return b.String(), nil
}

func hashDefinition(s string) string {
	sum := sha256.Sum256([]byte(NormalizeDefinition(s)))
	return hex.EncodeToString(sum[:])
}

func lineCounts(s string) map[string]int {
	m := map[string]int{}
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			m[line]++
		}
	}
	return m
}

// moduleIdentity returns "name type" of a module fingerprint line.
func moduleIdentity(line string) string {
	fields := strings.Split(line, "\t")
	if len(fields) >= 3 {
		return fields[1] + " " + fields[2]
	}
	return line
}

// diffFingerprints renders the changes from stored to current as sorted lines:
// "+ ..." added, "- ..." removed, "~ <module> definition changed".
func diffFingerprints(stored, current string) []string {
	oldCount := lineCounts(stored)
	newCount := lineCounts(current)
	var diff []string
	for line := range newCount {
		if oldCount[line] > 0 || !strings.HasPrefix(line, "module\t") {
			continue
		}
		id := moduleIdentity(line)
		for old := range oldCount {
			if oldCount[old] > 0 && strings.HasPrefix(old, "module\t") && moduleIdentity(old) == id {
				oldCount[old]--
				newCount[line]--
				diff = append(diff, "~ "+id+" definition changed")
				break
			}
		}
	}
	for line, n := range newCount {
		for ; n > 0; n-- {
			if oldCount[line] > 0 {
				oldCount[line]--
			} else {
				diff = append(diff, "+ "+line)
			}
		}
	}
	for line, n := range oldCount {
		for ; n > 0; n-- {
			diff = append(diff, "- "+line)
		}
	}
	sort.Strings(diff)
	return diff
}

// recordFingerprint stores the current schema fingerprint. Called inside the
// release transaction so the recorded state always matches what was applied.
func (s *store) recordFingerprint(ctx context.Context, db interface {
	reader
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) error {
	if s.noFingerprint || s.dialect.name != dialect.SQLServer {
		return nil
	}
	text, err := fingerprint(ctx, db)
	if err != nil {
		return fmt.Errorf("compute schema fingerprint: %w", err)
	}
	if _, err := db.ExecContext(ctx, s.dialect.upsertMeta, s.dialect.metaArgs(schemaFingerprintKey, text)...); err != nil {
		return fmt.Errorf("store schema fingerprint: %w", err)
	}
	return nil
}

// Drift compares the live database against the given managed object files and
// the schema fingerprint recorded at apply time.
func (s *store) Drift(ctx context.Context, files []File) (DriftReport, error) {
	if s.dialect.name != dialect.SQLServer {
		return DriftReport{}, fmt.Errorf("drift detection is not implemented for dialect %q", s.dialect.name)
	}
	current, err := fingerprint(ctx, s.db)
	if err != nil {
		return DriftReport{}, err
	}
	var stored sql.NullString
	err = s.db.QueryRowContext(ctx, s.dialect.selectMeta, s.dialect.metaArgs(schemaFingerprintKey, "")...).Scan(&stored)
	if err != nil && err != sql.ErrNoRows {
		return DriftReport{}, err
	}
	report := DriftReport{SchemaStored: stored.String, SchemaCurrent: current, Objects: []Status{}}
	if stored.Valid && stored.String != current {
		report.SchemaDiff = diffFingerprints(stored.String, current)
	}
	for _, file := range files {
		if file.Delete {
			continue
		}
		id, err := objectIdentity(file.SQL)
		if err != nil {
			return DriftReport{}, fmt.Errorf("%s: %w", file.Path, err)
		}
		var definition sql.NullString
		err = s.db.QueryRowContext(ctx,
			`SELECT m.definition FROM sys.sql_modules m JOIN sys.objects o ON o.object_id = m.object_id JOIN sys.schemas s ON s.schema_id = o.schema_id
			 WHERE s.name = @schema AND o.name = @name AND o.type IN ('V','P','FN','TF','IF')`,
			sql.Named("schema", id.schema), sql.Named("name", id.name)).Scan(&definition)
		state := "ok"
		switch {
		case err == sql.ErrNoRows:
			state = "missing"
		case err != nil:
			return DriftReport{}, err
		case NormalizeDefinition(definition.String) != NormalizeDefinition(file.SQL):
			state = "drifted"
		}
		report.Objects = append(report.Objects, Status{Path: file.Path, State: state, Checksum: file.Checksum})
	}
	sort.Slice(report.Objects, func(i, j int) bool { return report.Objects[i].Path < report.Objects[j].Path })
	return report, nil
}

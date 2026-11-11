package objects

import (
	"fmt"
	"strings"
	"unicode"

	"saxbase/internal/dialect"
)

type identity struct{ kind, schema, name string }

func (i identity) key() string { return strings.ToLower(i.schema + "\x00" + i.name) }

// Read only the DDL header, never interpolate raw SQL into a DROP statement.
// Schema qualification is required because a login's default schema can change.
func objectIdentity(definition string) (identity, error) {
	l := headerLexer{input: []rune(strings.TrimPrefix(definition, "\ufeff"))}
	keyword := func(want string) error {
		token, quoted, err := l.next()
		if err != nil {
			return err
		}
		if quoted || !strings.EqualFold(token, want) {
			return fmt.Errorf("expected %s in object definition", want)
		}
		return nil
	}
	if err := keyword("CREATE"); err != nil {
		return identity{}, err
	}
	kind, quoted, err := l.next()
	if err != nil {
		return identity{}, err
	}
	if !quoted && strings.EqualFold(kind, "OR") {
		replacement, replacementQuoted, err := l.next()
		if err != nil || replacementQuoted || (!strings.EqualFold(replacement, "ALTER") && !strings.EqualFold(replacement, "REPLACE")) {
			return identity{}, fmt.Errorf("expected ALTER or REPLACE in object definition")
		}
		kind, quoted, err = l.next()
		if err != nil {
			return identity{}, err
		}
	}
	kind = strings.ToUpper(kind)
	if kind == "PROC" {
		kind = "PROCEDURE"
	}
	if quoted || (kind != "VIEW" && kind != "PROCEDURE" && kind != "FUNCTION") {
		return identity{}, fmt.Errorf("rollback supports CREATE [OR ALTER|REPLACE] VIEW, PROCEDURE or FUNCTION")
	}
	schema, _, err := l.next()
	if err != nil || schema == "" || schema == "." {
		return identity{}, fmt.Errorf("invalid object schema")
	}
	if err := keyword("."); err != nil {
		return identity{}, fmt.Errorf("rollback requires a schema-qualified object name: %w", err)
	}
	name, _, err := l.next()
	if err != nil || name == "" || name == "." {
		return identity{}, fmt.Errorf("invalid object name")
	}
	token, _, err := l.next()
	if err != nil {
		return identity{}, err
	}
	if token == "." {
		return identity{}, fmt.Errorf("cross-database object names are not supported")
	}
	return identity{kind: kind, schema: schema, name: name}, nil
}

type headerLexer struct {
	input []rune
	pos   int
}

// Historical files may use CREATE rather than a repeatable DDL form. Make only
// the verb repeatable; preserve the stored source/checksum and object body.
func restoreDefinition(definition string, name dialect.Name) string {
	l := headerLexer{input: []rune(strings.TrimPrefix(definition, "\ufeff"))}
	_, _, _ = l.next()
	afterCreate := l.pos
	next, _, _ := l.next()
	if strings.EqualFold(next, "OR") {
		return definition
	}
	replacement := " OR ALTER"
	if name == dialect.Postgres {
		replacement = " OR REPLACE"
	}
	return string(l.input[:afterCreate]) + replacement + string(l.input[afterCreate:])
}

func (l *headerLexer) next() (string, bool, error) {
	for l.pos < len(l.input) {
		if unicode.IsSpace(l.input[l.pos]) {
			l.pos++
			continue
		}
		if l.pos+1 < len(l.input) && l.input[l.pos] == '-' && l.input[l.pos+1] == '-' {
			for l.pos < len(l.input) && l.input[l.pos] != '\n' {
				l.pos++
			}
			continue
		}
		if l.pos+1 < len(l.input) && l.input[l.pos] == '/' && l.input[l.pos+1] == '*' {
			l.pos += 2
			depth := 1
			for l.pos < len(l.input) && depth > 0 {
				if l.pos+1 < len(l.input) && l.input[l.pos] == '/' && l.input[l.pos+1] == '*' {
					depth++
					l.pos += 2
				} else if l.pos+1 < len(l.input) && l.input[l.pos] == '*' && l.input[l.pos+1] == '/' {
					depth--
					l.pos += 2
				} else {
					l.pos++
				}
			}
			if depth != 0 {
				return "", false, fmt.Errorf("unterminated SQL comment")
			}
			continue
		}
		break
	}
	if l.pos == len(l.input) {
		return "", false, nil
	}
	c := l.input[l.pos]
	l.pos++
	if c == '[' || c == '"' {
		end := c
		if c == '[' {
			end = ']'
		}
		var token strings.Builder
		for l.pos < len(l.input) {
			r := l.input[l.pos]
			l.pos++
			if r == end {
				if l.pos < len(l.input) && l.input[l.pos] == end {
					token.WriteRune(end)
					l.pos++
					continue
				}
				return token.String(), true, nil
			}
			token.WriteRune(r)
		}
		return "", false, fmt.Errorf("unterminated quoted identifier")
	}
	start := l.pos - 1
	if unicode.IsLetter(c) || c == '_' || c == '#' || c == '@' {
		for l.pos < len(l.input) {
			r := l.input[l.pos]
			if !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_@$#", r)) {
				break
			}
			l.pos++
		}
	}
	return string(l.input[start:l.pos]), false, nil
}

func snapshotFiles(snapshot Snapshot) ([]File, []identity, error) {
	files := make([]File, 0, len(snapshot.Objects))
	ids := make([]identity, 0, len(snapshot.Objects))
	seen := map[string]bool{}
	for _, object := range snapshot.Objects {
		id, err := objectIdentity(object.SQL)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", object.Path, err)
		}
		if seen[id.key()] {
			return nil, nil, fmt.Errorf("duplicate database object in manifest: %s.%s", id.schema, id.name)
		}
		seen[id.key()] = true
		files = append(files, File{Path: object.Path, SQL: object.SQL, Checksum: object.Checksum})
		ids = append(ids, id)
	}
	return files, ids, validateFiles(files)
}

func validateObjectTransition(files, baseline []File) error {
	state := make(map[string]File, len(baseline)+len(files))
	for _, file := range baseline {
		if !file.Delete {
			state[file.Path] = file
		}
	}
	for _, file := range files {
		if file.Delete {
			delete(state, file.Path)
		} else {
			state[file.Path] = file
		}
	}
	seen := make(map[string]string, len(state))
	for path, file := range state {
		id, err := objectIdentity(file.SQL)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if previous, ok := seen[id.key()]; ok {
			return fmt.Errorf("duplicate database object %s.%s in %s and %s", id.schema, id.name, previous, path)
		}
		seen[id.key()] = path
	}
	return nil
}

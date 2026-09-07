package objects

import (
	"fmt"
	"strings"
	"unicode"
)

type identity struct{ kind, schema, name string }

func (i identity) key() string { return strings.ToLower(i.schema + "\x00" + i.name) }
func (i identity) drop() string {
	return "DROP " + i.kind + " IF EXISTS [" + strings.ReplaceAll(i.schema, "]", "]]") + "].[" + strings.ReplaceAll(i.name, "]", "]]") + "];"
}

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
		if err := keyword("ALTER"); err != nil {
			return identity{}, err
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
		return identity{}, fmt.Errorf("rollback supports CREATE [OR ALTER] VIEW, PROCEDURE or FUNCTION")
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

// Historical files may use CREATE rather than CREATE OR ALTER. Make only the
// DDL verb repeatable; preserve the stored source/checksum and the object's body.
func restoreDefinition(definition string) string {
	l := headerLexer{input: []rune(strings.TrimPrefix(definition, "\ufeff"))}
	_, _, _ = l.next()
	afterCreate := l.pos
	next, _, _ := l.next()
	if strings.EqualFold(next, "OR") {
		return definition
	}
	return string(l.input[:afterCreate]) + " OR ALTER" + string(l.input[afterCreate:])
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
	if snapshot.ObjectCount != len(snapshot.Objects) {
		return nil, nil, fmt.Errorf("release %s has an incomplete file set", snapshot.Version)
	}
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

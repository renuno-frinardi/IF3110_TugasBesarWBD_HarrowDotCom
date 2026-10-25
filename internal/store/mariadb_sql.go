package store

import (
	"errors"
	"strings"
)

func MariaDBStatements(sqlText string) ([]string, error) {
	var statements []string
	var current strings.Builder
	var quote byte
	for i := 0; i < len(sqlText); i++ {
		c := sqlText[i]
		if c == '\\' {
			return nil, errors.New("Backslashes are not supported, use two single quotes for an apostrophe")
		}
		if quote != 0 {
			current.WriteByte(c)
			if c == quote {
				if i+1 < len(sqlText) && sqlText[i+1] == quote {
					i++
					current.WriteByte(quote)
				} else {
					quote = 0
				}
			}
			continue
		}
		if c == '#' || (c == '-' && i+2 < len(sqlText) && sqlText[i+1] == '-' && sqlText[i+2] <= ' ') {
			for i < len(sqlText) && sqlText[i] != '\n' {
				i++
			}
			current.WriteByte(' ')
			continue
		}
		if c == '/' && i+1 < len(sqlText) && sqlText[i+1] == '*' {
			// MariaDB bisa menjalankan SQL di dalam komentar /*! ... */.
			return nil, errors.New("Use -- comments for MariaDB SQL")
		}
		switch c {
		case '\'', '"', '`':
			quote = c
			current.WriteByte(c)
		case ';':
			if text := strings.TrimSpace(current.String()); text != "" {
				statements = append(statements, text)
			}
			current.Reset()
		default:
			current.WriteByte(c)
		}
	}
	if quote != 0 {
		return nil, errors.New("SQL quote is not closed")
	}
	if text := strings.TrimSpace(current.String()); text != "" {
		statements = append(statements, text)
	}
	if len(statements) == 0 {
		return nil, errors.New("SQL is required")
	}
	return statements, nil
}

// Perintah lain, termasuk DDL dan COMMIT manual, tidak masuk transaksi migration.
func ValidateMariaDBStatements(statements []string) error {
	for _, statement := range statements {
		switch strings.ToUpper(strings.Fields(statement)[0]) {
		case "SELECT", "INSERT", "UPDATE", "DELETE":
		default:
			return errors.New("MariaDB migrations support SELECT, INSERT, UPDATE, and DELETE; CREATE/ALTER/DROP cannot be rolled back")
		}
	}
	return nil
}

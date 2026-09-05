// Package migrations embeds the SQL migration files so goose can read them
// from within the compiled binary.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

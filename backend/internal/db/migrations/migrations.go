package migrations

import (
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

func Up(url string) error {
	conn, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("migrasyon bağlantısı: %w", err)
	}
	defer conn.Close()
	goose.SetBaseFS(files)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(conn, ".")
}
